package exec

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sync"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

// runtimeCleanupGrace bounds kernel-accounting and pipe-drain failures after a node has already exhausted its deadline.
const runtimeCleanupGrace = 5 * time.Second

// runWithDeadline assigns a suspended runtime to a non-breakaway job before it can spawn providers, so a deadline owns the complete ordinary process tree.
func runWithDeadline(ctx context.Context, cmd *exec.Cmd) error {
	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return fmt.Errorf("creating runtime job: %w", err)
	}
	defer func() { _ = windows.CloseHandle(job) }()
	limits := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{}
	limits.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
	if _, err := windows.SetInformationJobObject(job, windows.JobObjectExtendedLimitInformation, uintptr(unsafe.Pointer(&limits)), uint32(unsafe.Sizeof(limits))); err != nil {
		return fmt.Errorf("protecting runtime job: %w", err)
	}
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.CreationFlags |= windows.CREATE_SUSPENDED
	stopOutput := protectRuntimeOutput(cmd)
	defer stopOutput()
	// Pipe draining must not end while a wrapper's descendants still have time left to run.
	cmd.WaitDelay = runtimeCleanupGrace
	if deadline, ok := ctx.Deadline(); ok {
		cmd.WaitDelay += max(time.Until(deadline), 0)
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	abort := func(cause error) error {
		_ = cmd.Process.Kill()
		return waitForRuntime(ctx, cmd, job, runtimeCleanupGrace, cause)
	}
	process, err := windows.OpenProcess(windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE|windows.PROCESS_SUSPEND_RESUME, false, uint32(cmd.Process.Pid))
	if err != nil {
		return abort(fmt.Errorf("opening suspended runtime: %w", err))
	}
	defer func() { _ = windows.CloseHandle(process) }()
	assignErr := windows.AssignProcessToJobObject(job, process)
	if assignErr != nil {
		return abort(fmt.Errorf("assigning runtime job: %w; remove incompatible host job restrictions or omit node timeouts", assignErr))
	}
	if err := ctx.Err(); err != nil {
		return abort(err)
	}
	if err := resumeRuntime(process); err != nil {
		return abort(err)
	}
	return waitForRuntime(ctx, cmd, job, runtimeCleanupGrace, nil)
}

// waitForRuntime bounds shutdown even if termination fails or job accounting never acknowledges exited processes.
func waitForRuntime(ctx context.Context, cmd *exec.Cmd, job windows.Handle, grace time.Duration, initialErr error) error {
	processes := map[uint32]windows.Handle{}
	defer func() {
		for _, process := range processes {
			_ = windows.CloseHandle(process)
		}
	}()
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	ticker := time.NewTicker(20 * time.Millisecond)
	defer ticker.Stop()
	var timer *time.Timer
	defer func() {
		if timer != nil {
			timer.Stop()
		}
	}()
	var expired <-chan time.Time
	var waitErr, shutdownErr error
	waited, stopping := false, false
	cancel := ctx.Done()
	stop := func(cause error) {
		shutdownErr = errors.Join(shutdownErr, cause)
		if stopping {
			return
		}
		stopping = true
		cancel = nil
		timer = time.NewTimer(grace)
		expired = timer.C
		if err := windows.TerminateJobObject(job, 1); err != nil {
			shutdownErr = errors.Join(shutdownErr, fmt.Errorf("terminating runtime job: %w", err))
		}
	}
	if initialErr != nil {
		stop(initialErr)
	}
	for {
		if !stopping {
			if err := captureJobProcesses(job, processes); err != nil {
				stop(err)
			}
		}
		select {
		case waitErr = <-done:
			waited = true
			done = nil
		default:
		}
		active, queryErr := activeJobProcesses(job)
		if queryErr == nil && active == 0 && waited && jobProcessesExited(processes) {
			if stopping {
				return errors.Join(ctx.Err(), shutdownErr)
			}
			return waitErr
		}
		if !stopping {
			if err := ctx.Err(); err != nil {
				stop(err)
			} else if queryErr != nil {
				stop(fmt.Errorf("querying runtime job: %w", queryErr))
			}
		}
		select {
		case waitErr = <-done:
			waited = true
			done = nil
		case <-cancel:
		case <-ticker.C:
		case <-expired:
			// The owner also closes the kill-on-close job on return; retained handles provide a fallback when job termination itself was denied.
			for pid, process := range processes {
				if status, _ := windows.WaitForSingleObject(process, 0); status == windows.WAIT_OBJECT_0 {
					continue
				}
				if err := windows.TerminateProcess(process, 1); err != nil {
					shutdownErr = errors.Join(shutdownErr, fmt.Errorf("terminating runtime process %d: %w", pid, err))
				}
			}
			if err := cmd.Process.Kill(); err != nil && !errors.Is(err, os.ErrProcessDone) {
				shutdownErr = errors.Join(shutdownErr, fmt.Errorf("terminating runtime: %w", err))
			}
			return errors.Join(ctx.Err(), shutdownErr, fmt.Errorf("runtime cleanup exceeded %s; inspect the execution and recover any uncertain mutation before retrying", grace))
		}
	}
}

// resumeRuntime resumes the whole suspended process, including extra threads created before user code, just as the plugin host does.
func resumeRuntime(process windows.Handle) error {
	resume := windows.NewLazySystemDLL("ntdll.dll").NewProc("NtResumeProcess")
	if err := resume.Find(); err != nil {
		return fmt.Errorf("locating runtime resume procedure: %w", err)
	}
	status, _, _ := resume.Call(uintptr(process))
	if status != 0 {
		return fmt.Errorf("resuming runtime process: %w", windows.NTStatus(status))
	}
	return nil
}

// runtimeOutput detaches late pipe copies before a bounded shutdown returns buffers to their caller.
type runtimeOutput struct {
	mu     *sync.Mutex
	writer io.Writer
}

func (out *runtimeOutput) Write(p []byte) (int, error) {
	out.mu.Lock()
	defer out.mu.Unlock()
	if out.writer == nil {
		return len(p), nil
	}
	return out.writer.Write(p)
}

// protectRuntimeOutput serializes both streams because engine actions commonly share one buffer for stdout and stderr.
func protectRuntimeOutput(cmd *exec.Cmd) func() {
	mu := &sync.Mutex{}
	stdout, stderr := &runtimeOutput{mu: mu, writer: cmd.Stdout}, &runtimeOutput{mu: mu, writer: cmd.Stderr}
	// Files stay inherited handles so a blocked console or pipe cannot strand a parent-side copying goroutine.
	if _, file := cmd.Stdout.(*os.File); !file && cmd.Stdout != nil {
		cmd.Stdout = stdout
	}
	if _, file := cmd.Stderr.(*os.File); !file && cmd.Stderr != nil {
		cmd.Stderr = stderr
	}
	return func() {
		mu.Lock()
		defer mu.Unlock()
		stdout.writer, stderr.writer = nil, nil
	}
}

// jobAccounting matches JOBOBJECT_BASIC_ACCOUNTING_INFORMATION; active membership includes children even after their wrapper exits.
type jobAccounting struct {
	TotalUserTime, TotalKernelTime, PeriodUserTime, PeriodKernelTime int64
	PageFaults, TotalProcesses, ActiveProcesses, TerminatedProcesses uint32
}

func activeJobProcesses(job windows.Handle) (uint32, error) {
	var info jobAccounting
	err := windows.QueryInformationJobObject(job, windows.JobObjectBasicAccountingInformation, uintptr(unsafe.Pointer(&info)), uint32(unsafe.Sizeof(info)), nil)
	return info.ActiveProcesses, err
}

// captureJobProcesses retains synchronizable members while they are still listed because terminated members can disappear before their handles signal completion.
func captureJobProcesses(job windows.Handle, processes map[uint32]windows.Handle) error {
	size := 8 + 16*int(unsafe.Sizeof(uintptr(0)))
	for {
		data := make([]byte, size)
		err := windows.QueryInformationJobObject(job, windows.JobObjectBasicProcessIdList, uintptr(unsafe.Pointer(&data[0])), uint32(len(data)), nil)
		if errors.Is(err, windows.ERROR_MORE_DATA) {
			if size >= 8*1024*1024 {
				return fmt.Errorf("runtime process list keeps growing; reduce node concurrency before retrying")
			}
			size *= 2
			continue
		}
		if err != nil {
			return fmt.Errorf("listing runtime job processes: %w", err)
		}
		count := int(binary.LittleEndian.Uint32(data[4:8]))
		width := int(unsafe.Sizeof(uintptr(0)))
		for i := 0; i < count; i++ {
			offset := 8 + i*width
			pid := binary.LittleEndian.Uint32(data[offset : offset+4])
			if _, exists := processes[pid]; exists {
				continue
			}
			handle, err := windows.OpenProcess(windows.SYNCHRONIZE|windows.PROCESS_TERMINATE, false, pid)
			if errors.Is(err, windows.ERROR_INVALID_PARAMETER) {
				continue
			}
			if err != nil {
				return fmt.Errorf("tracking runtime job process: %w", err)
			}
			processes[pid] = handle
		}
		return nil
	}
}

func jobProcessesExited(processes map[uint32]windows.Handle) bool {
	for pid, process := range processes {
		status, err := windows.WaitForSingleObject(process, 0)
		if err != nil || status != windows.WAIT_OBJECT_0 {
			continue
		}
		_ = windows.CloseHandle(process)
		delete(processes, pid)
	}
	return len(processes) == 0
}

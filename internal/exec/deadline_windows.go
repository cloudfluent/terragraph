package exec

import (
	"context"
	"fmt"
	"os/exec"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

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
	if err := cmd.Start(); err != nil {
		return err
	}
	abort := func(err error) error {
		_ = windows.TerminateJobObject(job, 1)
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		return err
	}
	process, err := windows.OpenProcess(windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE, false, uint32(cmd.Process.Pid))
	if err != nil {
		return abort(fmt.Errorf("opening suspended runtime: %w", err))
	}
	assignErr := windows.AssignProcessToJobObject(job, process)
	_ = windows.CloseHandle(process)
	if assignErr != nil {
		return abort(fmt.Errorf("assigning runtime job: %w; remove incompatible host job restrictions or omit node timeouts", assignErr))
	}
	if err := ctx.Err(); err != nil {
		return abort(err)
	}
	if err := resumeRuntime(uint32(cmd.Process.Pid)); err != nil {
		return abort(err)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	ticker := time.NewTicker(20 * time.Millisecond)
	defer ticker.Stop()
	var waitErr error
	waited, terminated := false, false
	cancel := ctx.Done()
	for {
		if !terminated && ctx.Err() != nil {
			if err := windows.TerminateJobObject(job, 1); err == nil {
				terminated = true
				cancel = nil
			}
		}
		active, queryErr := activeJobProcesses(job)
		if queryErr == nil && active == 0 && waited {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return waitErr
		}
		select {
		case waitErr = <-done:
			waited = true
			done = nil
		case <-cancel:
		case <-ticker.C:
		}
	}
}

// resumeRuntime finds the initial thread after os/exec closes its thread handle; the process has not executed user code yet.
func resumeRuntime(pid uint32) error {
	snapshot, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPTHREAD, 0)
	if err != nil {
		return fmt.Errorf("locating runtime thread: %w", err)
	}
	defer func() { _ = windows.CloseHandle(snapshot) }()
	entry := windows.ThreadEntry32{Size: uint32(unsafe.Sizeof(windows.ThreadEntry32{}))}
	for err := windows.Thread32First(snapshot, &entry); err == nil; err = windows.Thread32Next(snapshot, &entry) {
		if entry.OwnerProcessID != pid {
			continue
		}
		thread, err := windows.OpenThread(windows.THREAD_SUSPEND_RESUME, false, entry.ThreadID)
		if err != nil {
			return fmt.Errorf("opening runtime thread: %w", err)
		}
		_, err = windows.ResumeThread(thread)
		_ = windows.CloseHandle(thread)
		if err != nil {
			return fmt.Errorf("resuming runtime thread: %w", err)
		}
		return nil
	}
	return fmt.Errorf("runtime initial thread unavailable; retry after checking host process restrictions")
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

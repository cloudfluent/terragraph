package exec

import (
	"errors"
	"fmt"
	"os/exec"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

// StartManagedProcess assigns the suspended server to a kill-on-close job before any plugin code can create untracked children.
func StartManagedProcess(cmd *exec.Cmd) (func(), error) {
	job, err := newKillOnCloseJob()
	if err != nil {
		return nil, err
	}
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: windows.CREATE_SUSPENDED}
	if err := cmd.Start(); err != nil {
		_ = windows.CloseHandle(job)
		return nil, err
	}
	if err := assignAndResume(job, cmd.Process.Pid, nil); err != nil {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		_ = windows.CloseHandle(job)
		return nil, err
	}
	return func() { _ = windows.CloseHandle(job) }, nil
}

// errJobAssignment lets each caller name its own remedy, since only timed runtimes can avoid job membership by dropping a flag.
var errJobAssignment = errors.New("assigning process job")

// newKillOnCloseJob ties every member's lifetime to the owner's handle, so a crashed owner cannot leave the tree running.
func newKillOnCloseJob() (windows.Handle, error) {
	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return 0, fmt.Errorf("creating process job: %w", err)
	}
	limits := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{}
	limits.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
	if _, err := windows.SetInformationJobObject(job, windows.JobObjectExtendedLimitInformation, uintptr(unsafe.Pointer(&limits)), uint32(unsafe.Sizeof(limits))); err != nil {
		_ = windows.CloseHandle(job)
		return 0, fmt.Errorf("protecting process job: %w", err)
	}
	return job, nil
}

// assignAndResume joins a CREATE_SUSPENDED process to job before any of its code runs; admit may refuse resumption once membership is certain.
func assignAndResume(job windows.Handle, pid int, admit func() error) error {
	process, err := windows.OpenProcess(windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE|windows.PROCESS_SUSPEND_RESUME, false, uint32(pid))
	if err != nil {
		return fmt.Errorf("opening suspended process: %w", err)
	}
	defer func() { _ = windows.CloseHandle(process) }()
	if err := windows.AssignProcessToJobObject(job, process); err != nil {
		return fmt.Errorf("%w: %w; remove incompatible host job restrictions", errJobAssignment, err)
	}
	if admit != nil {
		if err := admit(); err != nil {
			return err
		}
	}
	return resumeRuntime(process)
}

// resumeRuntime resumes the whole suspended process, including extra threads created before user code, since resuming only the main thread can strand the rest.
func resumeRuntime(process windows.Handle) error {
	resume := windows.NewLazySystemDLL("ntdll.dll").NewProc("NtResumeProcess")
	if err := resume.Find(); err != nil {
		return fmt.Errorf("locating process resume procedure: %w", err)
	}
	status, _, _ := resume.Call(uintptr(process))
	if status != 0 {
		return fmt.Errorf("resuming suspended process: %w", windows.NTStatus(status))
	}
	return nil
}

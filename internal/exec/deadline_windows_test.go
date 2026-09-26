package exec

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

func TestRunCommand_DeadlineHelper(t *testing.T) {
	mode := os.Getenv("TG_DEADLINE_HELPER")
	if mode == "" {
		return
	}
	if mode == "child" || mode == "noisy" {
		if err := os.WriteFile(os.Getenv("TG_DEADLINE_PID"), []byte(strconv.Itoa(os.Getpid())), 0600); err != nil {
			os.Exit(8)
		}
		if mode == "noisy" {
			for {
				_, _ = fmt.Fprintln(os.Stdout, "runtime output")
				_, _ = fmt.Fprintln(os.Stderr, "runtime diagnostic")
				time.Sleep(time.Millisecond)
			}
		}
		time.Sleep(time.Minute)
		os.Exit(0)
	}
	if mode == "parent" {
		child := exec.Command(os.Args[0], "-test.run=^TestRunCommand_DeadlineHelper$")
		child.Env = append(os.Environ(), "TG_DEADLINE_HELPER=child")
		if err := child.Start(); err != nil {
			os.Exit(9)
		}
		os.Exit(0)
	}
	os.Exit(10)
}

func TestRunCommand_DeadlineWaitsForWindowsDescendants(t *testing.T) {
	pidPath := filepath.Join(t.TempDir(), "child.pid")
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	cmd := exec.Command(os.Args[0], "-test.run=^TestRunCommand_DeadlineHelper$")
	cmd.Env = append(os.Environ(), "TG_DEADLINE_HELPER=parent", "TG_DEADLINE_PID="+pidPath)
	err := runCommand(ctx, cmd)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("error = %v, want deadline despite successful wrapper exit", err)
	}
	data, err := os.ReadFile(pidPath)
	if err != nil {
		t.Fatal(err)
	}
	pid, err := strconv.Atoi(string(data))
	if err != nil {
		t.Fatal(err)
	}
	process, err := windows.OpenProcess(windows.SYNCHRONIZE, false, uint32(pid))
	if errors.Is(err, windows.ERROR_INVALID_PARAMETER) {
		return
	}
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = windows.CloseHandle(process) }()
	status, err := windows.WaitForSingleObject(process, 0)
	if err != nil || status != windows.WAIT_OBJECT_0 {
		t.Fatalf("child status = %d, error = %v, want terminated before return", status, err)
	}
}

func TestRunCommand_DeadlineAllowsSuccessfulWindowsProcess(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cmd := exec.Command("cmd.exe", "/d", "/c", "exit", "0")
	if err := runCommand(ctx, cmd); err != nil {
		t.Fatal(err)
	}
}

func TestWaitForRuntime_DeniedTerminationHasBoundedCleanup(t *testing.T) {
	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = windows.CloseHandle(job) }()
	limits := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{}
	limits.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
	if _, err := windows.SetInformationJobObject(job, windows.JobObjectExtendedLimitInformation, uintptr(unsafe.Pointer(&limits)), uint32(unsafe.Sizeof(limits))); err != nil {
		t.Fatal(err)
	}
	pidPath := filepath.Join(t.TempDir(), "runtime.pid")
	cmd := exec.Command(os.Args[0], "-test.run=^TestRunCommand_DeadlineHelper$")
	cmd.Env = append(os.Environ(), "TG_DEADLINE_HELPER=noisy", "TG_DEADLINE_PID="+pidPath)
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: windows.CREATE_SUSPENDED}
	cmd.WaitDelay = time.Second
	var output bytes.Buffer
	cmd.Stdout, cmd.Stderr = &output, &output
	stopOutput := protectRuntimeOutput(cmd)
	defer stopOutput()
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	waiting := false
	defer func() {
		_ = windows.TerminateJobObject(job, 1)
		_ = cmd.Process.Kill()
		if !waiting {
			_ = cmd.Wait()
		}
	}()
	process, err := windows.OpenProcess(windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE|windows.PROCESS_SUSPEND_RESUME|windows.SYNCHRONIZE, false, uint32(cmd.Process.Pid))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = windows.CloseHandle(process) }()
	if err := windows.AssignProcessToJobObject(job, process); err != nil {
		t.Fatal(err)
	}
	if err := resumeRuntime(process); err != nil {
		t.Fatal(err)
	}
	until := time.Now().Add(5 * time.Second)
	for {
		if _, err := os.Stat(pidPath); err == nil {
			break
		}
		if time.Now().After(until) {
			t.Fatal("runtime never resumed")
		}
		time.Sleep(time.Millisecond)
	}
	// A query-only duplicate exercises a real access-denied termination without replacing Windows API functions.
	const jobObjectQuery = 0x0004
	var queryJob windows.Handle
	current := windows.CurrentProcess()
	if err := windows.DuplicateHandle(current, job, current, &queryJob, jobObjectQuery, false, 0); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = windows.CloseHandle(queryJob) }()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	waiting = true
	start := time.Now()
	err = waitForRuntime(ctx, cmd, queryJob, 100*time.Millisecond, nil)
	stopOutput()
	if !errors.Is(err, context.Canceled) || !errors.Is(err, windows.ERROR_ACCESS_DENIED) || !strings.Contains(err.Error(), "runtime cleanup exceeded") {
		t.Fatalf("got = %v, want cancellation and bounded cleanup failure", err)
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("elapsed = %s, want bounded cleanup", elapsed)
	}
	before := output.String()
	if status, err := windows.WaitForSingleObject(process, 2000); err != nil || status != windows.WAIT_OBJECT_0 {
		t.Fatalf("process status = %d, error = %v, want fallback termination", status, err)
	}
	time.Sleep(50 * time.Millisecond)
	if after := output.String(); after != before {
		t.Fatal("runtime wrote to caller buffer after cleanup returned")
	}
}

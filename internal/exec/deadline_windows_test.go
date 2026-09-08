package exec

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

func TestRunCommand_DeadlineHelper(t *testing.T) {
	mode := os.Getenv("TG_DEADLINE_HELPER")
	if mode == "" {
		return
	}
	if mode == "child" {
		if err := os.WriteFile(os.Getenv("TG_DEADLINE_PID"), []byte(strconv.Itoa(os.Getpid())), 0600); err != nil {
			os.Exit(8)
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
	if err != nil {
		return
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

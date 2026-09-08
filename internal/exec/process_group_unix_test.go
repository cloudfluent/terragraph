//go:build linux || darwin

package exec

import (
	"os/exec"
	"syscall"
	"testing"
	"time"
)

func TestProcessGroupRunning_ZombieDoesNotHoldRunOpen(t *testing.T) {
	cmd := exec.Command("sh", "-c", "exit 0")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = cmd.Wait() }()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		running, err := processGroupRunning(cmd.Process.Pid)
		if err != nil {
			t.Fatalf("inspecting process group: %v", err)
		}
		if !running {
			if err := syscall.Kill(cmd.Process.Pid, 0); err != nil {
				t.Fatalf("unreaped child = %v, want zombie still present", err)
			}
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("an unreaped zombie was mistaken for a running process")
}

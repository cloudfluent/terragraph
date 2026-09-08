//go:build linux || darwin

package main

import (
	"errors"
	"github.com/cloudfluent/terragraph/internal/runlock"
	"golang.org/x/sys/unix"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

func TestMain_ControllingTerminalApproval(t *testing.T) {
	for _, command := range []string{"apply", "destroy"} {
		for _, answer := range []string{"yes\n", "no\n", "\x04", "\x03"} {
			t.Run(command+"-"+answer, func(t *testing.T) {
				scenario := "approval"
				if command == "destroy" {
					scenario = "destroy"
				}
				f := newCancellationFixture(t, scenario)
				f.tty = true
				master, slave := openCancellationPTY(t)
				args := []string{command, "--node", "a"}
				if answer == "\x03" {
					args = []string{command}
				}
				// This fixture intentionally selects an upstream alone to exercise its own terminal prompt.
				if command == "destroy" {
					args = append(args, "--allow-orphan-destroy")
				}
				_, done := startCancellationCLIWithInput(t, f, slave, args...)
				if command == "apply" {
					waitCancellationFile(t, f.stdout, "Apply these changes")
				} else {
					waitCancellationFile(t, filepath.Join(f.dir, "destroy-waiting"), "ready")
				}
				if _, err := master.WriteString(answer); err != nil {
					t.Fatal(err)
				}
				waitCancellationTTYExit(t, master, done, answer != "yes\n")
				if answer != "yes\n" {
					if _, err := os.Stat(filepath.Join(f.dir, "applied")); !os.IsNotExist(err) {
						t.Fatal("apply ran after rejected or interrupted approval")
					}
				}
				if answer == "\x03" {
					waitCancellationFile(t, f.stderr, "context canceled")
					if _, err := os.Stat(filepath.Join(f.dir, "started-b")); !os.IsNotExist(err) {
						t.Fatal("queued node b ran after terminal interrupt")
					}
				}
				lock, err := runlock.TryAcquire(f.dir)
				if err != nil {
					t.Fatal(err)
				}
				_ = lock.Close()
			})
		}
	}
}

func TestMain_ControllingTerminalUnreadNormalDestroy(t *testing.T) {
	f := newCancellationFixture(t, "destroy-no-read")
	f.tty = true
	_, slave := openCancellationPTY(t)
	_, done := startCancellationCLIWithInput(t, f, slave, "destroy", "--node", "a", "--allow-orphan-destroy")
	waitCancellationExit(t, done, false)
}

// waitCancellationTTYExit drains terminal echo because Darwin can wait for its output queue while closing the controlling terminal during process exit.
func waitCancellationTTYExit(t *testing.T, master *os.File, done <-chan error, wantFailure bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		select {
		case err := <-done:
			if (err != nil) != wantFailure {
				t.Fatalf("exit error = %v, want failure %t", err, wantFailure)
			}
			return
		default:
		}
		fds := []unix.PollFd{{Fd: int32(master.Fd()), Events: unix.POLLIN}}
		ready, err := unix.Poll(fds, 20)
		if errors.Is(err, syscall.EINTR) {
			continue
		}
		if err != nil {
			t.Fatal(err)
		}
		if ready > 0 && fds[0].Revents&unix.POLLIN != 0 {
			var b [1024]byte
			_, _ = master.Read(b[:])
		}
	}
	t.Fatal("CLI did not finish terminal input")
}

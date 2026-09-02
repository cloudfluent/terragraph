package runlock

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

const (
	helperEnv    = "TG_RUNLOCK_HELPER"
	helperDirEnv = "TG_RUNLOCK_DIR"
	heldEnv      = "TG_RUNLOCK_HELD"
	releaseEnv   = "TG_RUNLOCK_RELEASE"
)

func TestTryAcquire_EmptyDirErrors(t *testing.T) {
	if _, err := TryAcquire(""); err == nil {
		t.Fatal("expected an error for an empty blueprint directory")
	}
}

func TestTryAcquire_CreatesLockFile(t *testing.T) {
	dir := t.TempDir()
	lock, err := TryAcquire(dir)
	if err != nil {
		t.Fatalf("TryAcquire: %v", err)
	}
	t.Cleanup(func() { _ = lock.Close() })

	if _, err := os.Stat(filepath.Join(dir, ".terragraph", "lock")); err != nil {
		t.Fatalf("expected lock file: %v", err)
	}
}

func TestTryAcquire_SameProcessCanReacquireAfterClose(t *testing.T) {
	dir := t.TempDir()
	lock, err := TryAcquire(dir)
	if err != nil {
		t.Fatalf("first TryAcquire: %v", err)
	}
	if err := lock.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	lock, err = TryAcquire(dir)
	if err != nil {
		t.Fatalf("TryAcquire after Close: %v", err)
	}
	if err := lock.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}
}

func TestTryAcquire_HeldByAnotherProcess(t *testing.T) {
	if os.Getenv(helperEnv) == "hold" {
		runHoldHelper()
		return
	}

	dir := t.TempDir()
	held := filepath.Join(dir, "held")
	release := filepath.Join(dir, "release")
	cmd := startHelper(t, dir, held, release, "hold")
	waitForFile(t, held)

	_, err := TryAcquire(dir)
	if !errors.Is(err, ErrHeld) {
		t.Fatalf("TryAcquire while held: err = %v, want ErrHeld", err)
	}

	if err := os.WriteFile(release, []byte("1"), 0o644); err != nil {
		t.Fatalf("signaling helper to release: %v", err)
	}
	if err := cmd.Wait(); err != nil {
		t.Fatalf("helper: %v", err)
	}

	lock, err := TryAcquire(dir)
	if err != nil {
		t.Fatalf("TryAcquire after helper released: %v", err)
	}
	if err := lock.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
}

func TestAcquire_BlocksUntilReleased(t *testing.T) {
	if os.Getenv(helperEnv) == "hold" {
		runHoldHelper()
		return
	}

	dir := t.TempDir()
	held := filepath.Join(dir, "held")
	release := filepath.Join(dir, "release")
	cmd := startHelper(t, dir, held, release, "hold")
	waitForFile(t, held)

	acquired := make(chan error, 1)
	go func() {
		lock, err := Acquire(dir, nil)
		if err != nil {
			acquired <- err
			return
		}
		acquired <- lock.Close()
	}()

	select {
	case err := <-acquired:
		if err != nil {
			t.Fatalf("Acquire: %v", err)
		}
		t.Fatal("Acquire returned while another process still holds the lock")
	case <-time.After(200 * time.Millisecond):
	}

	if err := os.WriteFile(release, []byte("1"), 0o644); err != nil {
		t.Fatalf("signaling helper to release: %v", err)
	}

	select {
	case err := <-acquired:
		if err != nil {
			t.Fatalf("Acquire after release: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Acquire did not return after the lock was released")
	}

	if err := cmd.Wait(); err != nil {
		t.Fatalf("helper: %v", err)
	}
}

func TestAcquire_WritesNoticeWhenWaiting(t *testing.T) {
	if os.Getenv(helperEnv) == "hold" {
		runHoldHelper()
		return
	}

	dir := t.TempDir()
	held := filepath.Join(dir, "held")
	release := filepath.Join(dir, "release")
	cmd := startHelper(t, dir, held, release, "hold")
	waitForFile(t, held)

	notices := make(chan []byte, 1)
	done := make(chan error, 1)
	go func() {
		lock, err := Acquire(dir, noticeWriter(notices))
		if err != nil {
			done <- err
			return
		}
		done <- lock.Close()
	}()

	select {
	case got := <-notices:
		if want := "waiting for another terragraph process using this blueprint to finish\n"; string(got) != want {
			t.Fatalf("wait notice = %q, want %q", got, want)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("expected a wait notice while the lock was held")
	}

	if err := os.WriteFile(release, []byte("1"), 0o644); err != nil {
		t.Fatalf("signaling helper to release: %v", err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Acquire: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Acquire did not return after the lock was released")
	}
	if err := cmd.Wait(); err != nil {
		t.Fatalf("helper: %v", err)
	}
}

func TestAcquire_NoNoticeWhenLockIsFree(t *testing.T) {
	var notice bytes.Buffer
	lock, err := Acquire(t.TempDir(), &notice)
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	if err := lock.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if notice.Len() != 0 {
		t.Fatalf("unexpected wait notice when lock was free: %q", notice.String())
	}
}

func startHelper(t *testing.T, dir, held, release, mode string) *exec.Cmd {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run=^"+t.Name()+"$")
	cmd.Env = append(os.Environ(),
		helperEnv+"="+mode,
		helperDirEnv+"="+dir,
		heldEnv+"="+held,
		releaseEnv+"="+release,
	)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		t.Fatalf("starting helper: %v", err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill() })
	return cmd
}

func runHoldHelper() {
	lock, err := Acquire(os.Getenv(helperDirEnv), nil)
	if err != nil {
		_, _ = os.Stderr.WriteString("helper Acquire: " + err.Error() + "\n")
		os.Exit(1)
	}
	if err := os.WriteFile(os.Getenv(heldEnv), []byte("1"), 0o644); err != nil {
		os.Exit(1)
	}
	waitForFileOrExit(os.Getenv(releaseEnv))
	if err := lock.Close(); err != nil {
		os.Exit(1)
	}
	os.Exit(0)
}

func waitForFile(t *testing.T, path string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(path); err == nil {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", path)
}

type noticeWriter chan []byte

func (n noticeWriter) Write(p []byte) (int, error) {
	cp := append([]byte(nil), p...)
	select {
	case n <- cp:
	default:
	}
	return len(p), nil
}

func waitForFileOrExit(path string) {
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(path); err == nil {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	os.Exit(1)
}

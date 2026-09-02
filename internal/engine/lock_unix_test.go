//go:build !windows

package engine

import (
	"bytes"
	"os"
	osexec "os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/cloudfluent/terragraph/internal/exec"
	"github.com/cloudfluent/terragraph/internal/runlock"
)

const (
	lockHoldEnv    = "TG_LOCK_HOLD"
	lockDirEnv     = "TG_LOCK_DIR"
	lockHeldEnv    = "TG_LOCK_HELD"
	lockReleaseEnv = "TG_LOCK_RELEASE"
)

// graph/validate only read the blueprint, so they must not take the process lock
// an apply is already holding — otherwise `terragraph graph` during a long apply
// would hang for the whole run.
func TestValidate_DoesNotTakeRunLock(t *testing.T) {
	if os.Getenv(lockHoldEnv) == "1" {
		runLockHoldHelper()
		return
	}

	baseDir := t.TempDir()
	writeModule(t, filepath.Join(baseDir, "module"))
	blueprintPath := writeBlueprint(t, baseDir, `node "a" { source = "./module" }`)
	e, err := Load(blueprintPath, exec.Terraform, &bytes.Buffer{}, &bytes.Buffer{})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	held := filepath.Join(baseDir, "held")
	release := filepath.Join(baseDir, "release")
	cmd := osexec.Command(os.Args[0], "-test.run=^"+t.Name()+"$")
	cmd.Env = append(os.Environ(),
		lockHoldEnv+"=1",
		lockDirEnv+"="+e.BaseDir,
		lockHeldEnv+"="+held,
		lockReleaseEnv+"="+release,
	)
	if err := cmd.Start(); err != nil {
		t.Fatalf("starting lock holder: %v", err)
	}
	defer func() { _ = cmd.Process.Kill() }()
	waitForTestFile(t, held)

	start := time.Now()
	_ = e.Validate()
	if time.Since(start) > time.Second {
		t.Fatalf("Validate blocked on the blueprint run lock for %s", time.Since(start))
	}

	if err := os.WriteFile(release, []byte("1"), 0o644); err != nil {
		t.Fatalf("releasing helper: %v", err)
	}
	if err := cmd.Wait(); err != nil {
		t.Fatalf("lock holder: %v", err)
	}
}

func runLockHoldHelper() {
	lock, err := runlock.Acquire(os.Getenv(lockDirEnv), nil)
	if err != nil {
		_, _ = os.Stderr.WriteString(err.Error() + "\n")
		os.Exit(1)
	}
	if err := os.WriteFile(os.Getenv(lockHeldEnv), []byte("1"), 0o644); err != nil {
		os.Exit(1)
	}
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(os.Getenv(lockReleaseEnv)); err == nil {
			_ = lock.Close()
			os.Exit(0)
		}
		time.Sleep(10 * time.Millisecond)
	}
	os.Exit(1)
}

func waitForTestFile(t *testing.T, path string) {
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

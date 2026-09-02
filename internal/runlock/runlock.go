// Package runlock serializes terragraph processes that mutate a blueprint's working state.
//
// plan, apply, destroy and vendor all write under the same blueprint directory:
// engine-managed files in .terragraph/ (tfvars, TF_DATA_DIR, saved plans) and, for
// nodes that share a module source, that directory's .terraform.lock.hcl. Two CLI
// processes doing that at once is a lost-update race, not in-process --parallelism
// (which is one process, one lock, and still allowed).
//
// The lock is an advisory file lock at <blueprint dir>/.terragraph/lock. It is
// released on Close or process exit, so a crash cannot leave a stale lock behind.
package runlock

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// ErrHeld is returned by TryAcquire when another process already holds the blueprint lock.
var ErrHeld = errors.New("another terragraph process is using this blueprint")

const waitNotice = "waiting for another terragraph process using this blueprint to finish"

// Lock is a held exclusive lock on one blueprint. Close releases it.
type Lock struct {
	f *os.File
}

// TryAcquire is Acquire but returns ErrHeld immediately if another process already holds the lock.
func TryAcquire(baseDir string) (*Lock, error) {
	return acquire(baseDir, true)
}

// Acquire takes an exclusive process lock for the blueprint at baseDir. If another
// terragraph process already holds it, a one-line notice is written to waitNoticeWriter
// (when not nil) and this call blocks until that process releases the lock.
func Acquire(baseDir string, waitNoticeWriter io.Writer) (*Lock, error) {
	lock, err := TryAcquire(baseDir)
	if err == nil {
		return lock, nil
	}
	if !errors.Is(err, ErrHeld) {
		return nil, err
	}
	if waitNoticeWriter != nil {
		_, _ = fmt.Fprintln(waitNoticeWriter, waitNotice)
	}
	return acquire(baseDir, false)
}

// Close releases the lock. It is safe to call on a nil Lock.
func (l *Lock) Close() error {
	if l == nil || l.f == nil {
		return nil
	}
	err := unlock(l.f)
	cerr := l.f.Close()
	l.f = nil
	if err != nil {
		return err
	}
	return cerr
}

func acquire(baseDir string, nonblocking bool) (*Lock, error) {
	f, err := openLock(baseDir)
	if err != nil {
		return nil, err
	}
	if err := lock(f, nonblocking); err != nil {
		_ = f.Close()
		return nil, err
	}
	return &Lock{f: f}, nil
}

func openLock(baseDir string) (*os.File, error) {
	if baseDir == "" {
		return nil, fmt.Errorf("empty blueprint directory")
	}
	path := filepath.Join(baseDir, ".terragraph", "lock")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("creating lock directory: %w", err)
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, fmt.Errorf("opening lock file %s: %w", path, err)
	}
	return f, nil
}

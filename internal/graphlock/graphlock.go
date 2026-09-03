// Package graphlock serializes a terragraph plan/apply/destroy run across machines.
//
// The local flock in internal/runlock only covers one checkout. This package holds
// a remote lease keyed by blueprint identity so two clean checkouts (laptop + CI,
// Alice + Bob) cannot walk the graph at once. Per-node Terraform state locks still
// cover individual state writes; this lease covers graph order (vpc then eks).
//
// Activation is the blueprint lock block, not a CLI flag. validate/graph/language-server
// never take it. Only s3 is implemented today (conditional PutObject, same idea as
// Terraform 1.10+ use_lockfile); dynamodb/gcs are reserved nested types in the HCL.
package graphlock

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/cloudfluent/terragraph/internal/blueprint"
)

// ErrHeld is returned when another run already owns the graph lock object.
var ErrHeld = errors.New("another terragraph process holds the graph lock")

const waitNotice = "waiting for another terragraph process holding the graph lock to finish"

// Lock is a held graph-level remote lease. Close releases it.
type Lock struct {
	release func(context.Context) error
}

// Close deletes the remote lock object. Safe on a nil Lock.
func (l *Lock) Close() error {
	if l == nil || l.release == nil {
		return nil
	}
	err := l.release(context.Background())
	l.release = nil
	return err
}

// Acquire takes the remote graph lock described by cfg. cfg must be non-nil with a
// configured backend. On contention a one-line notice goes to waitNoticeWriter (when
// not nil) and the call blocks until the other holder releases, matching runlock.
func Acquire(cfg *blueprint.LockConfig, waitNoticeWriter io.Writer) (*Lock, error) {
	if cfg == nil {
		return nil, fmt.Errorf("lock: nil config")
	}
	backend, err := newBackend(cfg)
	if err != nil {
		return nil, err
	}
	info := lockInfo{
		Who:     whoAmI(),
		Created: time.Now().UTC(),
	}
	body, err := json.Marshal(info)
	if err != nil {
		return nil, fmt.Errorf("lock: encoding holder info: %w", err)
	}

	lock, err := tryAcquire(backend, body)
	if err == nil {
		return lock, nil
	}
	if !errors.Is(err, ErrHeld) {
		return nil, err
	}
	if waitNoticeWriter != nil {
		_, _ = fmt.Fprintln(waitNoticeWriter, waitNotice)
	}
	for {
		time.Sleep(2 * time.Second)
		lock, err = tryAcquire(backend, body)
		if err == nil {
			return lock, nil
		}
		if !errors.Is(err, ErrHeld) {
			return nil, err
		}
	}
}

func tryAcquire(backend backend, body []byte) (*Lock, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := backend.TryCreate(ctx, body); err != nil {
		return nil, err
	}
	return &Lock{release: backend.Delete}, nil
}

type lockInfo struct {
	Who     string    `json:"who"`
	Created time.Time `json:"created"`
}

func whoAmI() string {
	host, _ := os.Hostname()
	user := os.Getenv("USER")
	if user == "" {
		user = os.Getenv("USERNAME")
	}
	return fmt.Sprintf("%s@%s pid=%d", user, host, os.Getpid())
}

// backend is one remote lock store. s3 first; dynamodb/gcs later without changing callers.
type backend interface {
	TryCreate(ctx context.Context, body []byte) error
	Delete(ctx context.Context) error
}

func newBackend(cfg *blueprint.LockConfig) (backend, error) {
	switch {
	case cfg.S3 != nil:
		return newS3Backend(cfg.S3)
	default:
		return nil, fmt.Errorf("lock: no nested backend configured; declare lock.s3")
	}
}

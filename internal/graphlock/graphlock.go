// Package graphlock serializes terragraph plan/apply/destroy across machines
// with a remote lock object. Same-checkout races stay on internal/runlock.
package graphlock

import (
	"context"
	"errors"
	"fmt"

	"github.com/cloudfluent/terragraph/internal/blueprint"
)

// ErrHeld is returned when the graph lock object already exists.
var ErrHeld = errors.New("another terragraph process holds the graph lock")

// Backend acquires a graph lock for one nested lock type.
type Backend interface {
	Matches(lock *blueprint.Lock) bool
	Acquire(ctx context.Context, lock *blueprint.Lock) (Held, error)
	Release(ctx context.Context, lock *blueprint.Lock) error
	// Holder reports who holds the lock and since when, for force-unlock to show before it breaks one. Empty strings mean unknown, which is not an error: the object may already be gone.
	Holder(ctx context.Context, lock *blueprint.Lock) (who, created string)
}

// Held is a held graph lock. Close releases it. Close on a nil or already-released Held is a no-op.
type Held interface {
	Close() error
}

type noopHeld struct{}

func (noopHeld) Close() error { return nil }

var backends = []Backend{
	s3Backend{},
	// dynamodb / gcs later; unknown types never reach here (parse Error).
}

// Acquire takes the graph lock described by lock. A nil lock is a no-op.
func Acquire(ctx context.Context, lock *blueprint.Lock) (Held, error) {
	if lock == nil {
		return noopHeld{}, nil
	}
	for _, b := range backends {
		if b.Matches(lock) {
			return b.Acquire(ctx, lock)
		}
	}
	return nil, fmt.Errorf("no graph lock backend matches this lock block")
}

// Holder reports who currently holds the lock described by lock, and since when, so force-unlock can show what it is about to break. Empty strings mean unknown — an unreadable or already-deleted object is not an error here.
func Holder(ctx context.Context, lock *blueprint.Lock) (who, created string) {
	if lock == nil {
		return "", ""
	}
	for _, b := range backends {
		if b.Matches(lock) {
			return b.Holder(ctx, lock)
		}
	}
	return "", ""
}

// Release deletes a leftover graph lock object (force-unlock). Held.Close refuses a lock whose etag moved; Release skips that guard on purpose — the holder is gone, so no If-Match could ever match — leaving the --yes confirmation at the CLI as the only safety. A nil lock is a no-op.
func Release(ctx context.Context, lock *blueprint.Lock) error {
	if lock == nil {
		return nil
	}
	for _, b := range backends {
		if b.Matches(lock) {
			return b.Release(ctx, lock)
		}
	}
	return fmt.Errorf("no graph lock backend matches this lock block")
}

package runlock

import (
	"context"
	"errors"
	"testing"
)

func TestAcquireContext_CancellationPreservesCurrentOwner(t *testing.T) {
	dir := t.TempDir()
	owner, err := Acquire(dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = owner.Close() }()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if lock, err := AcquireContext(ctx, dir, nil); lock != nil || !errors.Is(err, context.Canceled) {
		_ = lock.Close()
		t.Fatalf("lock = %v, error = %v, want cancellation", lock, err)
	}
	if lock, err := TryAcquire(dir); !errors.Is(err, ErrHeld) {
		_ = lock.Close()
		t.Fatalf("owner lock = %v, want held", err)
	}
}

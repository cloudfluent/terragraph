package graphlock

import (
	"context"
	"testing"

	"github.com/cloudfluent/terragraph/internal/blueprint"
)

type fakeBackend struct {
	matches bool
	held    Held
	err     error
	calls   *int
}

func (f fakeBackend) Matches(lock *blueprint.Lock) bool { return f.matches }

func (f fakeBackend) Acquire(ctx context.Context, lock *blueprint.Lock) (Held, error) {
	if f.calls != nil {
		*f.calls++
	}
	if f.err != nil {
		return nil, f.err
	}
	if f.held != nil {
		return f.held, nil
	}
	return noopHeld{}, nil
}

func (f fakeBackend) Release(ctx context.Context, lock *blueprint.Lock) error {
	if f.calls != nil {
		*f.calls++
	}
	return nil
}

func withBackends(t *testing.T, bs ...Backend) {
	t.Helper()
	orig := backends
	backends = bs
	t.Cleanup(func() { backends = orig })
}

func TestAcquire_NilLockIsNoop(t *testing.T) {
	calls := 0
	withBackends(t, fakeBackend{matches: true, calls: &calls})
	held, err := Acquire(context.Background(), nil)
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	if err := held.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if calls != 0 {
		t.Fatalf("backend called %d times, want 0", calls)
	}
}

func TestAcquire_DispatchesToMatchingBackend(t *testing.T) {
	calls := 0
	withBackends(t, fakeBackend{matches: true, calls: &calls})
	held, err := Acquire(context.Background(), &blueprint.Lock{S3: &blueprint.S3Lock{}})
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	if err := held.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if calls != 1 {
		t.Fatalf("backend called %d times, want 1", calls)
	}
}

func TestAcquire_NoMatchingBackend(t *testing.T) {
	withBackends(t, fakeBackend{matches: false})
	_, err := Acquire(context.Background(), &blueprint.Lock{S3: &blueprint.S3Lock{}})
	if err == nil {
		t.Fatal("expected an error when no backend matches")
	}
}

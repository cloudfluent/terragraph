package graphlock

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cloudfluent/terragraph/internal/blueprint"
)

type memBackend struct {
	held atomic.Bool
}

func (m *memBackend) TryCreate(ctx context.Context, body []byte) error {
	if !m.held.CompareAndSwap(false, true) {
		return ErrHeld
	}
	return nil
}

func (m *memBackend) Delete(ctx context.Context) error {
	m.held.Store(false)
	return nil
}

func TestTryAcquire_MemBackend(t *testing.T) {
	b := &memBackend{}
	lock, err := tryAcquire(b, []byte(`{}`))
	if err != nil {
		t.Fatalf("tryAcquire: %v", err)
	}
	if _, err := tryAcquire(b, []byte(`{}`)); !errors.Is(err, ErrHeld) {
		t.Fatalf("second tryAcquire err = %v, want ErrHeld", err)
	}
	if err := lock.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if _, err := tryAcquire(b, []byte(`{}`)); err != nil {
		t.Fatalf("after release tryAcquire: %v", err)
	}
}

func TestNewBackend_RequiresS3(t *testing.T) {
	_, err := newBackend(&blueprint.LockConfig{})
	if err == nil {
		t.Fatal("expected error for empty lock config")
	}
}

func TestAcquire_NilConfig(t *testing.T) {
	_, err := Acquire(nil, nil)
	if err == nil {
		t.Fatal("expected error for nil config")
	}
}

func TestWhoAmI_NonEmpty(t *testing.T) {
	if whoAmI() == "" {
		t.Fatal("whoAmI returned empty")
	}
	_ = time.Now()
}

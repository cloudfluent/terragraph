package engine

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/cloudfluent/terragraph/internal/blueprint"
	"github.com/cloudfluent/terragraph/internal/graphlock"
)

type spyHeld struct{ closed *int }

func (s spyHeld) Close() error {
	if s.closed != nil {
		*s.closed++
	}
	return nil
}

func TestLockGraph_SkippedWhenLockUnset(t *testing.T) {
	orig := acquireRemoteLock
	t.Cleanup(func() { acquireRemoteLock = orig })
	calls := 0
	acquireRemoteLock = func(ctx context.Context, lock *blueprint.Lock) (graphlock.Held, error) {
		calls++
		return spyHeld{}, nil
	}

	path := writeBlueprint(t, t.TempDir(), `node "a" { source = "./a" }`)
	bp, err := blueprint.ParseFile(path)
	if err != nil {
		t.Fatalf("ParseFile: %v", err)
	}
	unlock, err := (&Engine{Blueprint: bp}).lockGraph()
	if err != nil {
		t.Fatalf("lockGraph: %v", err)
	}
	unlock()
	if calls != 0 {
		t.Fatalf("acquireRemoteLock called %d times, want 0", calls)
	}
}

func TestLockGraph_AcquiresWhenLockSet(t *testing.T) {
	orig := acquireRemoteLock
	t.Cleanup(func() { acquireRemoteLock = orig })
	calls := 0
	closed := 0
	acquireRemoteLock = func(ctx context.Context, lock *blueprint.Lock) (graphlock.Held, error) {
		calls++
		if lock == nil || lock.S3 == nil {
			t.Fatal("expected s3 lock config")
		}
		return spyHeld{closed: &closed}, nil
	}

	path := writeBlueprint(t, t.TempDir(), `
lock {
  s3 {
    bucket = "acme-tfstate"
    key    = "terragraph/prod.lock"
    region = "ap-northeast-2"
  }
}
`)
	bp, err := blueprint.ParseFile(path)
	if err != nil {
		t.Fatalf("ParseFile: %v", err)
	}
	unlock, err := (&Engine{Blueprint: bp}).lockGraph()
	if err != nil {
		t.Fatalf("lockGraph: %v", err)
	}
	if calls != 1 {
		t.Fatalf("acquireRemoteLock called %d times, want 1", calls)
	}
	unlock()
	if closed != 1 {
		t.Fatalf("held.Close called %d times, want 1", closed)
	}
}

func TestLockGraph_HeldErrorSurfaces(t *testing.T) {
	orig := acquireRemoteLock
	t.Cleanup(func() { acquireRemoteLock = orig })
	acquireRemoteLock = func(ctx context.Context, lock *blueprint.Lock) (graphlock.Held, error) {
		return nil, graphlock.ErrHeld
	}

	path := writeBlueprint(t, t.TempDir(), `
lock {
  s3 {
    bucket = "acme-tfstate"
    key    = "terragraph/prod.lock"
    region = "ap-northeast-2"
  }
}
`)
	bp, err := blueprint.ParseFile(path)
	if err != nil {
		t.Fatalf("ParseFile: %v", err)
	}
	_, err = (&Engine{Blueprint: bp}).lockGraph()
	if !errors.Is(err, graphlock.ErrHeld) {
		t.Fatalf("lockGraph: err = %v, want ErrHeld", err)
	}
}

type failHeld struct{ err error }

func (f failHeld) Close() error { return f.err }

func TestLockGraph_CloseErrorWrittenToStderr(t *testing.T) {
	orig := acquireRemoteLock
	t.Cleanup(func() { acquireRemoteLock = orig })
	acquireRemoteLock = func(ctx context.Context, lock *blueprint.Lock) (graphlock.Held, error) {
		return failHeld{err: errors.New("delete denied")}, nil
	}

	path := writeBlueprint(t, t.TempDir(), `
lock {
  s3 {
    bucket = "acme-tfstate"
    key    = "terragraph/prod.lock"
    region = "ap-northeast-2"
  }
}
`)
	bp, err := blueprint.ParseFile(path)
	if err != nil {
		t.Fatalf("ParseFile: %v", err)
	}
	var stderr bytes.Buffer
	unlock, err := (&Engine{Blueprint: bp, Stderr: &stderr}).lockGraph()
	if err != nil {
		t.Fatalf("lockGraph: %v", err)
	}
	unlock()
	if !strings.Contains(stderr.String(), "delete denied") {
		t.Fatalf("stderr = %q, want Close error", stderr.String())
	}
}

func TestLockGraph_CancellationReachesAcquireAndStillReleasesHeldLock(t *testing.T) {
	orig := acquireRemoteLock
	t.Cleanup(func() { acquireRemoteLock = orig })
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	closed := 0
	acquireRemoteLock = func(got context.Context, lock *blueprint.Lock) (graphlock.Held, error) {
		if got != ctx {
			t.Fatal("graph lock did not receive execution context")
		}
		if err := got.Err(); err != nil {
			return nil, err
		}
		return spyHeld{closed: &closed}, nil
	}
	e := &Engine{Context: ctx, Blueprint: &blueprint.Blueprint{Lock: &blueprint.Lock{}}}
	unlock, err := e.lockGraph()
	if err != nil {
		t.Fatal(err)
	}
	cancel()
	unlock()
	if closed != 1 {
		t.Fatalf("close calls = %d, want 1 after cancellation", closed)
	}
	if _, err := e.lockGraph(); !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want context canceled", err)
	}
}

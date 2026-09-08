package engine

import (
	"context"
	"fmt"
	"io"
	"path/filepath"

	"github.com/cloudfluent/terragraph/internal/blueprint"
	"github.com/cloudfluent/terragraph/internal/runlock"
)

// OpenExecutionHistory avoids module inspection so a missing source or broken wiring cannot hide the record needed for recovery.
func OpenExecutionHistory(ctx context.Context, path string, diagnostics io.Writer) (*Engine, func(), error) {
	bp, dir, err := blueprint.LoadPath(path)
	if err != nil {
		return nil, nil, err
	}
	base, err := filepath.Abs(dir)
	if err != nil {
		return nil, nil, fmt.Errorf("resolving execution directory: %w", err)
	}
	lock, err := runlock.AcquireContext(ctx, base, diagnostics)
	if err != nil {
		return nil, nil, err
	}
	e := &Engine{Context: ctx, BaseDir: base, Blueprint: bp, Stderr: diagnostics, runLock: lock}
	return e, func() { _ = lock.Close(); e.runLock = nil }, nil
}

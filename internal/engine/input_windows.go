package engine

import (
	"context"
	"io"
)

// approvalInput preserves native Windows console behavior until interruptible console reads have a Windows-specific implementation.
func approvalInput(_ context.Context, input io.Reader) io.Reader { return input }

package main

import (
	"context"
)

// executionContext preserves native Windows console behavior until process-tree cancellation has a Windows-specific implementation.
func executionContext(string) (context.Context, func()) { return context.Background(), func() {} }

//go:build !windows

package engine

import "github.com/cloudfluent/terragraph/internal/privatefs"

func syncExecutionDirectory(path string) error { return privatefs.SyncDirectory(path) }

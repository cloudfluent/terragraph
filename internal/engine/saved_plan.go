package engine

import "github.com/cloudfluent/terragraph/internal/privatefs"

// prepareSavedPlan shares the same file protection as plugin work and receipts, avoiding divergent Windows ACL guarantees.
func prepareSavedPlan(path string) (func(), error) { return privatefs.Prepare(path) }

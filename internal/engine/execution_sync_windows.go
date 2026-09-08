package engine

// Windows file contents are flushed before rename; reopening the journal after a crash remains mandatory because directory fsync is unavailable.
func syncExecutionDirectory(_ string) error { return nil }

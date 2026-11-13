package repository

// FlushFileBuffers itself requires writable directory handles, even when an
// unchanged directory needs no entry mutation. Never suppress that requirement.
func preflightDirectory(path string, _ bool) error { return syncOriginal(path) }

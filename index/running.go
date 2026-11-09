package index

import (
	"errors"
	"path/filepath"
)

// Running reports whether a run currently holds Dir(root)/index.lock.
// It probes the lock without blocking, so it is false where locking is a no-op.
func Running(root string) bool {
	unlock, err := lock(filepath.Join(Dir(root), "index.lock"))
	if err != nil {
		return errors.Is(err, ErrLocked)
	}
	unlock()
	return false
}

// LockFile takes the exclusive, non-blocking cross-process lock index runs
// use, on any path; ErrLocked when another holder has it.
func LockFile(path string) (unlock func(), err error) { return lock(path) }

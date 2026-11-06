package index

import (
	"errors"
	"path/filepath"
)

// Running reports whether a run currently holds Dir(root)/index.lock.
// It probes the lock without blocking, so it is false where flock is a no-op.
func Running(root string) bool {
	unlock, err := lock(filepath.Join(Dir(root), "index.lock"))
	if err != nil {
		return errors.Is(err, ErrLocked)
	}
	unlock()
	return false
}

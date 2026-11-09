//go:build !unix && !windows

package index

// lock is a no-op where flock is unavailable; two processes indexing the same
// root at once produce the same points, so the race is harmless.
func lock(string) (unlock func(), err error) {
	return func() {}, nil
}

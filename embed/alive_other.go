//go:build !unix && !windows

package embed

// pidAlive is unknown here: every owner counts as running, so nothing is swept.
func pidAlive(int) bool { return true }

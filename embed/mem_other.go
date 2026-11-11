//go:build !linux && !windows

package embed

// shortcut: no free-memory reading on macOS and the BSDs, so the low-memory
// check never fires there; add one (host_statistics64) if it is reported.
func availableMB() (int, bool) { return 0, false }

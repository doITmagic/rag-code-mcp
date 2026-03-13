// Package arenagc provides a mechanism to drain gotreesitter's global
// nodeArena pools. These pools retain large pre-allocated slabs (up to
// 128MB for CSS files) indefinitely because they are package-level
// variables that the GC cannot collect. This package uses go:linkname
// to access the unexported pool variables and clear their free lists.
package arenagc

import (
	"sync"
	_ "unsafe" // required for go:linkname

	_ "github.com/odvcencio/gotreesitter" // ensure the package is linked
)

// nodeArenaPool mirrors the internal gotreesitter struct layout.
// Only the fields we need to access (mu + free) are declared.
type nodeArenaPool struct {
	mu      sync.Mutex
	class   uint8
	maxSize int
	free    []*struct{} // opaque; we just need to nil the slice
}

//go:linkname incrementalPool github.com/odvcencio/gotreesitter.incrementalArenaPool
var incrementalPool nodeArenaPool

//go:linkname fullPool github.com/odvcencio/gotreesitter.fullArenaPool
var fullPool nodeArenaPool

// DrainArenaPools clears the free lists of both gotreesitter arena pools,
// allowing the GC to reclaim the large node slabs they retain.
// This should be called after indexing completes, followed by runtime.GC()
// and debug.FreeOSMemory().
func DrainArenaPools() {
	incrementalPool.mu.Lock()
	incrementalPool.free = nil
	incrementalPool.mu.Unlock()

	fullPool.mu.Lock()
	fullPool.free = nil
	fullPool.mu.Unlock()
}

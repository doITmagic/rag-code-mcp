package workspace

import (
	"sync"
	"time"

	"github.com/doITmagic/rag-code-mcp/internal/memory"
)

// NewManagerForTest creates a lightweight Manager instance for unit tests.
// Only the components required by tools (detector + cache + bookkeeping) are initialized.
func NewManagerForTest(cache *Cache) *Manager {
	if cache == nil {
		cache = NewCache(time.Minute)
	}

	return &Manager{
		detector:         NewDetector(),
		cache:            cache,
		indexing:         make(map[string]bool),
		memories:         make(map[string]memory.LongTermMemory),
		scanFingerprints: make(map[string]string),
		watchers:         make(map[string]*FileWatcher),
		knownWorkspaces:  make(map[string]*Info),
		collLocks:        make(map[string]*sync.Mutex),
	}
}

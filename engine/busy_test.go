package engine

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/doITmagic/rag-code-mcp/v2/embed"
	"github.com/doITmagic/rag-code-mcp/v2/index"
	"github.com/doITmagic/rag-code-mcp/v2/store"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A run that met files locked by another process (index.Status.Busy) queues
// a files run for them after lockRetryDelay, without holding other requests
// back, at most busyRetries times in a row.
func TestBusyFilesRetried(t *testing.T) {
	cfg := testConfig(t)
	var mu sync.Mutex
	var calls [][]string
	busyFor := 1 // runs that still find lk.go locked
	e := New(cfg, store.NewMemory(), &embed.Fake{}, nil)
	defer e.Close()
	e.run = func(_ context.Context, o index.Options) (index.Status, error) {
		mu.Lock()
		defer mu.Unlock()
		calls = append(calls, o.Files)
		st := index.Status{Root: o.Root, Phase: "done", Total: len(o.Files), Done: len(o.Files)}
		if len(calls) <= busyFor {
			st.Done, st.Failed, st.Busy = st.Done-1, 1, []string{"lk.go"}
		}
		return st, nil
	}
	n := func() int { mu.Lock(); defer mu.Unlock(); return len(calls) }
	w, err := e.Workspace(t.TempDir())
	require.NoError(t, err)

	w.IndexFiles([]string{"lk.go", "a.go"}, nil)
	require.Eventually(t, func() bool { return n() == 1 }, time.Second, 5*time.Millisecond)
	w.IndexFiles([]string{"b.go"}, nil)
	require.Eventually(t, func() bool { return n() == 2 }, time.Second, 5*time.Millisecond, "not held back by the pending retry")
	require.Eventually(t, func() bool { return n() == 3 }, lockRetryDelay(0)+2*time.Second, 20*time.Millisecond)
	mu.Lock()
	assert.Equal(t, []string{"lk.go"}, calls[2])
	mu.Unlock()
	e.mu.Lock()
	assert.Zero(t, w.busyTries, "a run that read every file resets the count")
	e.mu.Unlock()

	// A file that stays locked: no more than busyRetries runs.
	mu.Lock()
	busyFor = 1 << 30
	mu.Unlock()
	e.mu.Lock()
	w.busyTries = busyRetries - 1
	e.mu.Unlock()
	w.IndexFiles([]string{"lk.go"}, nil)
	require.Eventually(t, func() bool { return n() == 4 }, time.Second, 5*time.Millisecond)
	w.IndexFiles([]string{"c.go"}, nil)
	require.Eventually(t, func() bool { return n() == 5 }, time.Second, 5*time.Millisecond)
	e.mu.Lock()
	assert.Equal(t, busyRetries, w.busyTries, "exhausted: no new retry is scheduled")
	e.mu.Unlock()
}

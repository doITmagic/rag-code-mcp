package embed

import (
	"context"
	"net/http"
	"net/http/httptest"
	"runtime"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// With too little free memory a local Ollama is not called at all: the model
// load would only time out and start again. A warm model needs no new memory.
func TestLowMemory(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.Write([]byte(`{"embeddings":[[1,2,3]]}`))
	}))
	defer srv.Close()
	free := 500
	old := freeMB
	freeMB = func() (int, bool) { return free, true }
	t.Cleanup(func() { freeMB = old })

	o := NewOllama(srv.URL, "m", nil)
	o.MinFreeMB = 1536
	_, err := o.Embed(context.Background(), []string{"a"})
	require.ErrorIs(t, err, ErrLowMemory)
	assert.Contains(t, err.Error(), "500 MB free, at least 1536 MB needed")
	_, err = o.Dimension(context.Background())
	require.ErrorIs(t, err, ErrLowMemory)
	assert.Zero(t, calls.Load(), "Ollama must not be called")

	free = 4000
	_, err = o.Embed(context.Background(), []string{"a"})
	require.NoError(t, err)
	free = 500 // the model is loaded now: no check
	_, err = o.Embed(context.Background(), []string{"a"})
	require.NoError(t, err)

	remote := NewOllama("http://ollama.example:11434", "m", nil)
	remote.MinFreeMB = 1536
	assert.NoError(t, remote.checkMemory(), "another machine's memory is not ours")
	o.MinFreeMB, o.lastOK = 0, atomic.Int64{}
	assert.NoError(t, o.checkMemory(), "0 turns the check off")
	if _, ok := availableMB(); runtime.GOOS == "linux" || runtime.GOOS == "windows" {
		assert.True(t, ok, "this platform reads its free memory")
	}
}

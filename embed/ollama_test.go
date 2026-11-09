package embed

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// embedServer answers /api/embed with 3-float vectors.
func embedServer(t *testing.T) (*httptest.Server, *atomic.Int32, *[]int) {
	t.Helper()
	var calls atomic.Int32
	sizes := &[]int{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		switch r.URL.Path {
		case "/api/embed":
			var req struct {
				Model string   `json:"model"`
				Input []string `json:"input"`
			}
			require.NoError(t, json.NewDecoder(r.Body).Decode(&req))
			*sizes = append(*sizes, len(req.Input))
			vecs := make([][]float64, len(req.Input))
			for i := range vecs {
				vecs[i] = []float64{float64(i), 0.5, -1}
			}
			json.NewEncoder(w).Encode(map[string]any{"embeddings": vecs})
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return srv, &calls, sizes
}

func TestOllamaEmbed(t *testing.T) {
	srv, calls, sizes := embedServer(t)
	o := NewOllama(srv.URL+"/", "m", nil)
	vecs, err := o.Embed(context.Background(), []string{"a", "b"})
	require.NoError(t, err)
	assert.Equal(t, [][]float32{{0, 0.5, -1}, {1, 0.5, -1}}, vecs)
	assert.Equal(t, int32(1), calls.Load())
	assert.Equal(t, []int{2}, *sizes)
	assert.Equal(t, "m", o.Model())
}

func TestOllamaBatches(t *testing.T) {
	srv, calls, sizes := embedServer(t)
	o := NewOllama(srv.URL, "m", nil)
	vecs, err := o.Embed(context.Background(), make([]string, 40))
	require.NoError(t, err)
	assert.Len(t, vecs, 40)
	assert.Equal(t, int32(3), calls.Load())
	assert.Equal(t, []int{16, 16, 8}, *sizes)
}

// Long texts are also cut by size: on CPU a full request of 16 long texts
// takes ~19 s and a query embed queued behind it waits that long.
func TestOllamaBatchesCutBySize(t *testing.T) {
	srv, _, sizes := embedServer(t)
	o := NewOllama(srv.URL, "m", nil)
	long := strings.Repeat("x", 2000)
	in := []string{long, long, long, long, long, "a", "b", strings.Repeat("y", 9000), "c"}
	vecs, err := o.Embed(context.Background(), in)
	require.NoError(t, err)
	assert.Len(t, vecs, len(in))
	assert.Equal(t, []int{4, 3, 1, 1}, *sizes)
}

func TestOllamaDimensionCached(t *testing.T) {
	srv, calls, _ := embedServer(t)
	o := NewOllama(srv.URL, "m", nil)
	for range 3 {
		d, err := o.Dimension(context.Background())
		require.NoError(t, err)
		assert.Equal(t, 3, d)
	}
	assert.Equal(t, int32(1), calls.Load())
}

func TestOllamaErrors(t *testing.T) {
	t.Run("500", func(t *testing.T) {
		var calls atomic.Int32
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			calls.Add(1)
			http.Error(w, "boom", http.StatusInternalServerError)
		}))
		defer srv.Close()
		o := NewOllama(srv.URL, "m", nil)
		_, err := o.Embed(context.Background(), []string{"a"})
		require.ErrorIs(t, err, ErrUnavailable)
		assert.Contains(t, err.Error(), "boom")
		assert.Equal(t, int32(1), calls.Load(), "no retry on HTTP error")
		_, err = o.Dimension(context.Background())
		require.ErrorIs(t, err, ErrUnavailable)
	})

	t.Run("timeout", func(t *testing.T) {
		release := make(chan struct{})
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { <-release }))
		defer srv.Close()
		defer close(release)
		ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
		defer cancel()
		_, err := NewOllama(srv.URL, "m", nil).Embed(ctx, []string{"a"})
		require.ErrorIs(t, err, ErrUnavailable)
		require.ErrorIs(t, err, context.DeadlineExceeded)
	})

	t.Run("ctx cancel", func(t *testing.T) {
		srv, calls, _ := embedServer(t)
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		_, err := NewOllama(srv.URL, "m", nil).Embed(ctx, []string{"a"})
		require.ErrorIs(t, err, ErrUnavailable)
		require.ErrorIs(t, err, context.Canceled)
		assert.Equal(t, int32(0), calls.Load(), "no retry on dead ctx")
	})

	t.Run("short response", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			json.NewEncoder(w).Encode(map[string]any{"embeddings": [][]float64{{1}}})
		}))
		defer srv.Close()
		_, err := NewOllama(srv.URL, "m", nil).Embed(context.Background(), []string{"a", "b"})
		require.ErrorIs(t, err, ErrUnavailable)
	})

	t.Run("retry on transport error", func(t *testing.T) {
		var calls atomic.Int32
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if calls.Add(1) == 1 {
				c, _, err := w.(http.Hijacker).Hijack()
				require.NoError(t, err)
				c.Close() // client sees EOF
				return
			}
			json.NewEncoder(w).Encode(map[string]any{"embeddings": [][]float64{{1, 2}}})
		}))
		defer srv.Close()
		vecs, err := NewOllama(srv.URL, "m", nil).Embed(context.Background(), []string{"a"})
		require.NoError(t, err)
		assert.Equal(t, [][]float32{{1, 2}}, vecs)
		assert.Equal(t, int32(2), calls.Load())
	})

	t.Run("down", func(t *testing.T) {
		srv := httptest.NewServer(http.NotFoundHandler())
		srv.Close()
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_, err := NewOllama(srv.URL, "m", nil).Embed(ctx, []string{"a"})
		require.ErrorIs(t, err, ErrUnavailable)
	})
}

func TestOllamaLive(t *testing.T) {
	url := "http://localhost:11434"
	if v, ok := os.LookupEnv("RAGCODE_TEST_OLLAMA"); ok {
		url = v
	} else {
		t.Skip("set RAGCODE_TEST_OLLAMA=http://host:11434 to run against a live Ollama")
	}
	o := NewOllama(url, "qwen3-embedding:0.6b", nil)
	ctx := context.Background()
	d, err := o.Dimension(ctx)
	require.NoError(t, err)
	vecs, err := o.Embed(ctx, []string{"func main() {}"})
	require.NoError(t, err)
	assert.Len(t, vecs[0], d)
}

// Threads caps the CPU threads Ollama uses (options.num_thread); 0 leaves it
// to Ollama and sends no options, so a GPU setup is not touched.
func TestOllamaThreads(t *testing.T) {
	for _, threads := range []int{0, 4} {
		var got map[string]any
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			require.NoError(t, json.NewDecoder(r.Body).Decode(&got))
			fmt.Fprint(w, `{"embeddings":[[1,0]]}`)
		}))
		o := NewOllama(srv.URL, "m", nil)
		o.Threads = threads
		_, err := o.Embed(context.Background(), []string{"a"})
		srv.Close()
		require.NoError(t, err)
		if threads == 0 {
			assert.NotContains(t, got, "options")
		} else {
			assert.Equal(t, map[string]any{"num_thread": float64(threads)}, got["options"])
		}
	}
}

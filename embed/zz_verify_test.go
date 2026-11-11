package embed

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Independent verification probes; see ollama_test.go for the implementer's tests.

func rawServer(t *testing.T, status int, body string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(status)
		w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestVerifyBadBodies(t *testing.T) {
	tests := []struct{ name, body string }{
		{"garbage", "\x00\xff\x01binary"},
		{"empty", ""},
		{"one byte", "{"},
		{"truncated", `{"embeddings":[[1,2`},
		{"null embeddings", `{"embeddings":null}`},
		{"empty vector", `{"embeddings":[[]]}`},
		{"string in vector", `{"embeddings":[["a"]]}`},
		{"too many", `{"embeddings":[[1],[2]]}`},
		{"wrong shape", `{"embeddings":[1]}`},
		{"html", `<html>502</html>`},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			srv := rawServer(t, 200, tc.body)
			o := NewOllama(srv.URL, "m", nil)
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			start := time.Now()
			vecs, err := o.Embed(ctx, []string{"a"})
			require.ErrorIs(t, err, ErrUnavailable)
			assert.Nil(t, vecs)
			assert.Less(t, time.Since(start), 2*time.Second, "no retry on a bad 200 body")
			_, err = o.Dimension(ctx)
			require.ErrorIs(t, err, ErrUnavailable)
		})
	}
}

func TestVerifyEmptyInput(t *testing.T) {
	srv, calls, _ := embedServer(t)
	o := NewOllama(srv.URL, "m", nil)
	for _, in := range [][]string{nil, {}} {
		vecs, err := o.Embed(context.Background(), in)
		require.NoError(t, err)
		assert.Empty(t, vecs)
	}
	assert.Equal(t, int32(0), calls.Load(), "no request for no input")

	// Invalid UTF-8 and a 1-byte input must not panic and go through as inputs.
	vecs, err := o.Embed(context.Background(), []string{"\xff\x00", "x", ""})
	require.NoError(t, err)
	assert.Len(t, vecs, 3)
}

func TestVerifyRetryExactlyOnce(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		c, _, err := w.(http.Hijacker).Hijack()
		require.NoError(t, err)
		c.Close()
	}))
	defer srv.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	start := time.Now()
	_, err := NewOllama(srv.URL, "m", nil).Embed(ctx, []string{"a"})
	require.ErrorIs(t, err, ErrUnavailable)
	assert.Equal(t, int32(2), calls.Load(), "one retry only")
	el := time.Since(start)
	assert.GreaterOrEqual(t, el, retryDelay, "retry waits 1 s")
	assert.Less(t, el, 3*time.Second)
}

func TestVerifyCancelDuringRetryWait(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		c, _, err := w.(http.Hijacker).Hijack()
		require.NoError(t, err)
		c.Close()
	}))
	defer srv.Close()
	ctx, cancel := context.WithCancel(context.Background())
	go func() { time.Sleep(100 * time.Millisecond); cancel() }()
	start := time.Now()
	_, err := NewOllama(srv.URL, "m", nil).Embed(ctx, []string{"a"})
	require.ErrorIs(t, err, ErrUnavailable)
	assert.Less(t, time.Since(start), 700*time.Millisecond, "retry wait honours ctx")
	assert.Equal(t, int32(1), calls.Load(), "no retry after cancel")
}

func TestVerifyDimensionReprobeAndConcurrent(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			http.Error(w, "loading", http.StatusInternalServerError)
			return
		}
		var req struct {
			Input []string `json:"input"`
		}
		require.NoError(t, json.NewDecoder(r.Body).Decode(&req))
		assert.Equal(t, []string{"dimension probe"}, req.Input)
		json.NewEncoder(w).Encode(map[string]any{"embeddings": [][]float64{{1, 2, 3, 4, 5}}})
	}))
	defer srv.Close()
	o := NewOllama(srv.URL, "m", nil)
	_, err := o.Dimension(context.Background())
	require.ErrorIs(t, err, ErrUnavailable)
	assert.Equal(t, int32(1), calls.Load())

	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			d, err := o.Dimension(context.Background())
			assert.NoError(t, err)
			assert.Equal(t, 5, d)
		}()
	}
	wg.Wait()
	d, _ := o.Dimension(context.Background())
	assert.Equal(t, 5, d)
	assert.LessOrEqual(t, calls.Load(), int32(9), "probe not repeated once cached")
}

func TestVerifyMidBatchFailure(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 2 {
			http.Error(w, "boom", 500)
			return
		}
		var req struct {
			Input []string `json:"input"`
		}
		json.NewDecoder(r.Body).Decode(&req)
		vecs := make([][]float64, len(req.Input))
		for i := range vecs {
			vecs[i] = []float64{1}
		}
		json.NewEncoder(w).Encode(map[string]any{"embeddings": vecs})
	}))
	defer srv.Close()
	vecs, err := NewOllama(srv.URL, "m", nil).Embed(context.Background(), make([]string, 40))
	require.ErrorIs(t, err, ErrUnavailable)
	assert.Nil(t, vecs, "no partial result")
	assert.Equal(t, int32(2), calls.Load(), "stops at the failed batch")
}

func TestVerifyFakeEdges(t *testing.T) {
	f := &Fake{Dim: 1}
	v := f.Vector("")
	require.Len(t, v, 1)
	assert.InDelta(t, 1, float64(v[0]*v[0]), 1e-6)
	assert.NotEqual(t, (&Fake{Dim: 4}).Vector("x"), (&Fake{Dim: 4}).Vector("x "), "distinct texts differ")
	assert.Equal(t, (&Fake{Dim: 4}).Vector("x"), (&Fake{Dim: 4}).Vector("x"), "deterministic across instances")

	vecs, err := f.Embed(context.Background(), nil)
	require.NoError(t, err)
	assert.Empty(t, vecs)
	assert.Equal(t, "fake", f.Model())

	// Fail counter under concurrency: exactly N calls fail.
	g := &Fake{Dim: 2}
	g.Fail.Store(5)
	var failed atomic.Int32
	var wg sync.WaitGroup
	for range 20 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := g.Embed(context.Background(), []string{"a"}); err != nil {
				assert.ErrorIs(t, err, ErrUnavailable)
				failed.Add(1)
			}
		}()
	}
	wg.Wait()
	assert.Equal(t, int32(5), failed.Load())
}

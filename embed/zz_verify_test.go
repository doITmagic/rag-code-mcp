package embed

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

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

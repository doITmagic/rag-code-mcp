package embed

import (
	"context"
	"math"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFake(t *testing.T) {
	f := &Fake{Dim: 16}
	ctx := context.Background()
	d, err := f.Dimension(ctx)
	require.NoError(t, err)
	assert.Equal(t, 16, d)

	a, err := f.Embed(ctx, []string{"hello", "world", "hello"})
	require.NoError(t, err)
	require.Len(t, a, 3)
	assert.Equal(t, a[0], a[2], "deterministic")
	assert.NotEqual(t, a[0], a[1], "text-dependent")
	for _, v := range a {
		var n float64
		for _, x := range v {
			n += float64(x) * float64(x)
		}
		assert.InDelta(t, 1, math.Sqrt(n), 1e-5, "normalized")
	}
	assert.Len(t, (&Fake{}).Vector("x"), 8, "zero Dim defaults to 8")
}

func TestFakeFail(t *testing.T) {
	f := &Fake{Dim: 4}
	f.Fail.Store(2)
	ctx := context.Background()
	for range 2 {
		_, err := f.Embed(ctx, []string{"a"})
		require.ErrorIs(t, err, ErrUnavailable)
	}
	_, err := f.Embed(ctx, []string{"a"})
	require.NoError(t, err)

	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	_, err = f.Embed(cancelled, []string{"a"})
	require.ErrorIs(t, err, context.Canceled)
}

// While Fetch is open, a call marked WithoutFetchWait fails at once with
// ErrFetching and an unmarked one waits for it to close.
func TestFakeFetch(t *testing.T) {
	f := &Fake{Fetch: make(chan struct{})}
	_, err := f.Dimension(WithoutFetchWait(context.Background()))
	require.ErrorIs(t, err, ErrFetching)
	require.ErrorIs(t, err, ErrUnavailable)
	_, err = f.Embed(WithoutFetchWait(context.Background()), []string{"a"})
	require.ErrorIs(t, err, ErrFetching)

	waited, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	_, err = f.Dimension(waited)
	require.ErrorIs(t, err, context.DeadlineExceeded, "an unmarked call waits")

	close(f.Fetch)
	_, err = f.Dimension(WithoutFetchWait(context.Background()))
	require.NoError(t, err)
	_, err = f.Embed(context.Background(), []string{"a"})
	require.NoError(t, err)
}

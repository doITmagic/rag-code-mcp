package vecdb

import (
	"fmt"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCollectionPathsEscapeURICharacters(t *testing.T) {
	for _, dir := range []string{"a#b", "a%b", "a#b?c%d"} {
		t.Run(dir, func(t *testing.T) {
			if runtime.GOOS == "windows" && dir == "a#b?c%d" {
				t.Skip("Windows paths cannot contain a question mark")
			}
			d := New(filepath.Join(t.TempDir(), dir))
			defer d.Close()
			for i, coll := range []string{"one#hash", "two space"} {
				n := 4 + i*4
				_, err := d.Ensure(ctx, coll, n)
				require.NoError(t, err)
				require.NoError(t, d.Upsert(ctx, coll, []Point{pt(coll, make([]float32, n), nil)}))
			}
			for i, coll := range []string{"one#hash", "two space"} {
				n, err := d.Dim(ctx, coll)
				require.NoError(t, err)
				assert.Equal(t, 4+i*4, n)
				hits, err := d.Scan(ctx, coll, nil, 10)
				require.NoError(t, err)
				assert.Equal(t, []string{coll}, ids(hits))
				assert.FileExists(t, d.Path(coll), fmt.Sprintf("collection %q must use its exact path", coll))
			}
			require.NoError(t, d.Close())
			for _, coll := range []string{"one#hash", "two space"} {
				hits, err := d.Scan(ctx, coll, nil, 10)
				require.NoError(t, err)
				assert.Equal(t, []string{coll}, ids(hits))
			}
		})
	}
}

func TestCollectionNameRejectsPercent(t *testing.T) {
	d := New(t.TempDir())
	defer d.Close()
	for _, coll := range []string{"a%b", "a%2fb", "a%23b"} {
		_, err := d.Ensure(ctx, coll, 4)
		assert.ErrorContains(t, err, "invalid collection name")
	}
}

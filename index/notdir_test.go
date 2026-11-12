package index

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A queued file whose parent directory became a file again (chain.go/x.go
// after chain.go is restored) is a deleted file, not a parse failure.
func TestFileUnderPathThatBecameAFileIsDeleted(t *testing.T) {
	e := newEnv(t)
	inner := e.write(t, "chain.go/x.go", "package x\n\nfunc Inner() {}\n")
	_, err := e.run(t, nil)
	require.NoError(t, err)
	require.NoError(t, os.RemoveAll(filepath.Dir(inner)))
	outer := e.write(t, "chain.go", "package x\n\nfunc Outer() {}\n")
	st, err := e.run(t, func(o *Options) { o.Files = []string{inner, outer} })
	require.NoError(t, err)
	assert.Equal(t, 0, st.Failed)
	assert.Equal(t, 1, st.Total)
}

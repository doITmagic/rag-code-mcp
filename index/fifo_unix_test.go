//go:build unix

package index

import (
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestVerifyBinaryAndFifoInExplicitFiles(t *testing.T) {
	e := newEnv(t)
	bin := e.write(t, "bin.go", "ab\x00cd")
	fifo := filepath.Join(e.root, "pipe.go")
	require.NoError(t, syscall.Mkfifo(fifo, 0o644))
	start := time.Now()
	st, err := e.run(t, func(o *Options) { o.Files = []string{bin, fifo} })
	require.NoError(t, err)
	assert.Equal(t, 0, st.Total)
	assert.Less(t, time.Since(start), 5*time.Second, "a fifo must not block the run")
}

package tools

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A line range of a binary file over 8 MiB returns no lines: the NUL byte in
// its first 8 KiB is seen although the file is streamed, not loaded.
func TestReadFileLargeBinaryRange(t *testing.T) {
	v := newEnv(t, false, nil)

	binPath := filepath.Join(v.root, "firmware.bin")
	f, err := os.Create(binPath)
	require.NoError(t, err)
	_, err = f.Write([]byte("\x7fELF\x00\x00\x00binary-header\nreadable line two\nreadable line three\n"))
	require.NoError(t, err)
	chunk := bytes.Repeat([]byte("a"), 64<<10)
	chunk[len(chunk)-1] = '\n'
	for range 130 { // > 8 MiB total
		_, err = f.Write(chunk)
		require.NoError(t, err)
	}
	require.NoError(t, f.Close())

	r, res := v.call(t, "rag_read_file_context", map[string]any{
		"workspace_root": v.root,
		"file_path":      "firmware.bin",
		"start_line":     2,
		"end_line":       3,
	})
	require.False(t, res.IsError, r.Message)

	var out struct {
		Lines string `json:"lines"`
	}
	data(t, r, &out)
	assert.Empty(t, out.Lines, "binary file (NUL in first 8 KiB) must return no lines")
	assert.Contains(t, r.Message, "the file is binary; its content is not returned")
}

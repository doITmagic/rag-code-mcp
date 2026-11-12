package tools

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A symbol made only of combining marks is nothing once diacritics are
// dropped: the text search must find no line, not every line.
func TestGrepCombiningMarksOnlyFindsNothing(t *testing.T) {
	v := newEnv(t, true, nil)

	r, res := v.call(t, "rag_find_usages", map[string]any{
		"workspace_root": v.root,
		"symbol":         "\u0301",
	})
	require.False(t, res.IsError, r.Message)

	var out struct {
		Grep []GrepHit `json:"grep"`
	}
	data(t, r, &out)
	assert.Empty(t, out.Grep, "ASCII files do not contain U+0301; data.grep must be empty")
}

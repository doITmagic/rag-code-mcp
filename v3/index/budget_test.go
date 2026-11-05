package index

import (
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/doITmagic/rag-code-mcp/v3/parser"
)

// inflightAnalyzer records the most source bytes being analyzed at once.
type inflightAnalyzer struct{ now, peak *atomic.Int64 }

func (inflightAnalyzer) Language() string     { return "budget" }
func (inflightAnalyzer) Extensions() []string { return []string{".bud"} }
func (a inflightAnalyzer) Analyze(f parser.File) ([]parser.Symbol, error) {
	n := a.now.Add(int64(len(f.Source)))
	for p := a.peak.Load(); n > p && !a.peak.CompareAndSwap(p, n); p = a.peak.Load() {
	}
	time.Sleep(20 * time.Millisecond)
	a.now.Add(-int64(len(f.Source)))
	return nil, nil
}

// Tree-sitter needs hundreds of times a file's size while it parses, so the
// source being parsed at once is capped whatever the number of CPUs.
func TestParseBudget(t *testing.T) {
	var now, peak atomic.Int64
	parser.Register(inflightAnalyzer{&now, &peak})
	e := newEnv(t)
	big := strings.Repeat("x", maxFileSize-1)
	for i := range 12 {
		e.write(t, string(rune('a'+i))+".bud", big)
	}
	for i := range 40 {
		e.write(t, "small/"+string(rune('a'+i%26))+string(rune('a'+i/26))+".bud", "y")
	}
	_, err := e.run(t, nil)
	require.NoError(t, err)
	assert.LessOrEqual(t, peak.Load(), int64(parseBudget))
	assert.Greater(t, peak.Load(), int64(0))
}

// Files are embedded in batches, so a batch must still resolve calls into
// files of a later batch: the run-wide name index is built before any batch.
func TestBatchesResolveForwardReferences(t *testing.T) {
	defer func(old int64) { batchBytes = old }(batchBytes)
	batchBytes = 1 // one file per batch, in path order: a.go before b.go
	e := newEnv(t)
	a := e.write(t, "a.go", "func Foo -> b.Bar\nfunc Qux -> Bar\nfunc Baz -> Dup\n")
	b := e.write(t, "b.go", "func Bar\nfunc Dup\n")
	e.write(t, "c.go", "func Dup\n")
	st, err := e.run(t, nil)
	require.NoError(t, err)
	assert.Equal(t, 3, st.Done)
	bar := e.symbols(t, b)["Bar"]
	syms := e.symbols(t, a)
	assert.Equal(t, bar.ID, syms["Foo"].Relations[0].TargetID)
	assert.Equal(t, parser.ResolutionExact, syms["Foo"].Relations[0].Resolution)
	assert.Equal(t, bar.ID, syms["Qux"].Relations[0].TargetID)
	assert.Equal(t, parser.ResolutionAmbiguous, syms["Baz"].Relations[0].Resolution)
}

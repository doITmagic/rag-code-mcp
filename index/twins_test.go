package index

import (
	"testing"

	"github.com/doITmagic/rag-code-mcp/v2/parser"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// twinID is the id of the symbol named name starting at line in path.
func twinID(t *testing.T, e *env, path, name string, line int) string {
	t.Helper()
	for _, s := range e.all(t, path) {
		if s.Name == name && s.StartLine == line {
			return s.ID
		}
	}
	t.Fatalf("no %s at %s:%d", name, path, line)
	return ""
}

// Overloads, getter/setter pairs and merged declarations share a qualified
// name in one file: one target (the widest), by qualified name and by name,
// in a full run and from the store in a files run. The same qualified name in
// two files (build tags) stays ambiguous.
func TestResolutionTwinsInOneFile(t *testing.T) {
	e := newEnv(t)
	src := "func Q -> twin.Def\nfunc N -> Def\nfunc T -> lk.Open\n"
	a := e.write(t, "a.go", src)
	tw := e.write(t, "twin.go", "func Def\nfunc Def\nfunc Def +3\n")
	e.write(t, "x/lk.go", "func Open\n")
	e.write(t, "y/lk.go", "func Open\n")
	check := func() {
		t.Helper()
		want := twinID(t, e, tw, "Def", 3)
		syms := e.symbols(t, a)
		assert.Equal(t, parser.Relation{Kind: parser.RelCall, TargetName: "Def", TargetQualifiedName: "twin.Def", TargetID: want, Resolution: parser.ResolutionExact, Line: 1}, syms["Q"].Relations[0])
		assert.Equal(t, parser.Relation{Kind: parser.RelCall, TargetName: "Def", TargetID: want, Resolution: parser.ResolutionUnique, Line: 2}, syms["N"].Relations[0])
		assert.Equal(t, parser.ResolutionAmbiguous, syms["T"].Relations[0].Resolution)
		assert.Empty(t, syms["T"].Relations[0].TargetID)
	}
	_, err := e.run(t, nil)
	require.NoError(t, err)
	check()

	e.write(t, "a.go", src+"\n")
	_, err = e.run(t, func(o *Options) { o.Files = []string{a} })
	require.NoError(t, err)
	check()
}

// Equal spans: the lowest start line wins.
func TestResolutionTwinsEqualSpans(t *testing.T) {
	e := newEnv(t)
	a := e.write(t, "a.go", "func Q -> twin.Def\n")
	tw := e.write(t, "twin.go", "func Other\nfunc Def\nfunc Def\n")
	_, err := e.run(t, nil)
	require.NoError(t, err)
	assert.Equal(t, twinID(t, e, tw, "Def", 2), e.symbols(t, a)["Q"].Relations[0].TargetID)
}

// A wider twin added later to an unchanged symbol's file takes over in a
// files run too, as a full run would pick it.
func TestResolutionNewTwinRefreshesReferrers(t *testing.T) {
	e := newEnv(t)
	a := e.write(t, "a.go", "func Q -> twin.Def\n")
	tw := e.write(t, "twin.go", "func Def\n")
	_, err := e.run(t, nil)
	require.NoError(t, err)
	require.Equal(t, twinID(t, e, tw, "Def", 1), e.symbols(t, a)["Q"].Relations[0].TargetID)

	e.write(t, "twin.go", "func Def\nfunc Def +5\n")
	_, err = e.run(t, func(o *Options) { o.Files = []string{tw} })
	require.NoError(t, err)
	rel := e.symbols(t, a)["Q"].Relations[0]
	assert.Equal(t, parser.ResolutionExact, rel.Resolution)
	assert.Equal(t, twinID(t, e, tw, "Def", 2), rel.TargetID)
}

// A qualified name that normalizes to a bare name ("..delta") is a name-only
// guess: unique or ambiguous, never exact by suffix.
func TestResolutionDegenerateQualifiedName(t *testing.T) {
	e := newEnv(t)
	a := e.write(t, "a.go", "func D -> ..delta\n")
	b := e.write(t, "b.go", "func delta\n")
	_, err := e.run(t, nil)
	require.NoError(t, err)
	rel := e.symbols(t, a)["D"].Relations[0]
	assert.Equal(t, parser.ResolutionUnique, rel.Resolution)
	assert.Equal(t, e.symbols(t, b)["delta"].ID, rel.TargetID)

	e.write(t, "c.go", "func delta\n")
	_, err = e.run(t, nil)
	require.NoError(t, err)
	rel = e.symbols(t, a)["D"].Relations[0]
	assert.Equal(t, parser.ResolutionAmbiguous, rel.Resolution)
	assert.Empty(t, rel.TargetID)
}

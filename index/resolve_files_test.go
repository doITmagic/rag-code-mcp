package index

import (
	"os"
	"testing"

	"github.com/doITmagic/rag-code-mcp/v2/parser"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A files run sees the same candidates as a full run: a target with one
// candidate in the run and another in the store stays ambiguous.
func TestResolutionFilesRunSeesStoredCandidates(t *testing.T) {
	e := newEnv(t)
	a := e.write(t, "a.go", "func Sfx -> pkg.Svc\nfunc Ex -> m.Dup\n")
	s1 := e.write(t, "src.pkg.go", "func Svc\n")
	e.write(t, "other.pkg.go", "func Svc\n")
	m1 := e.write(t, "m.go", "func Dup\n")
	e.write(t, "x/m.go", "func Dup\n")
	_, err := e.run(t, nil)
	require.NoError(t, err)
	rel := func(name string) parser.Relation { return e.symbols(t, a)[name].Relations[0] }
	require.Equal(t, parser.ResolutionAmbiguous, rel("Sfx").Resolution)
	exAmbig := rel("Ex").Resolution

	e.write(t, "a.go", "func Sfx -> pkg.Svc\nfunc Ex -> m.Dup\n\n")
	e.write(t, "src.pkg.go", "func Svc\n\n")
	e.write(t, "m.go", "func Dup\n\n")
	_, err = e.run(t, func(o *Options) { o.Files = []string{a, s1, m1} })
	require.NoError(t, err)
	assert.Equal(t, parser.ResolutionAmbiguous, rel("Sfx").Resolution)
	assert.Empty(t, rel("Sfx").TargetID)
	assert.Equal(t, exAmbig, rel("Ex").Resolution)
}

// Removing one of two candidates makes an ambiguous name call unique: the
// unchanged caller is re-resolved, as a full run would.
func TestResolutionRemovedCandidateRepairsAmbiguity(t *testing.T) {
	for name, remove := range map[string]func(t *testing.T, e *env, f string){
		"file deleted":  func(t *testing.T, e *env, f string) { require.NoError(t, os.Remove(f)) },
		"symbol edited": func(t *testing.T, e *env, f string) { e.write(t, "c.go", "func Other\n") },
	} {
		t.Run(name, func(t *testing.T) {
			e := newEnv(t)
			a := e.write(t, "a.go", "func Caller -> Run\n")
			b := e.write(t, "b.go", "func Run\n")
			c := e.write(t, "c.go", "func Run\n")
			_, err := e.run(t, nil)
			require.NoError(t, err)
			require.Equal(t, parser.ResolutionAmbiguous, e.symbols(t, a)["Caller"].Relations[0].Resolution)

			remove(t, e, c)
			_, err = e.run(t, nil)
			require.NoError(t, err)
			rel := e.symbols(t, a)["Caller"].Relations[0]
			assert.Equal(t, parser.ResolutionUnique, rel.Resolution)
			assert.Equal(t, e.symbols(t, b)["Run"].ID, rel.TargetID)
		})
	}
}

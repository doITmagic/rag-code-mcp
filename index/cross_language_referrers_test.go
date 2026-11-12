package index

import (
	"os"
	"testing"

	"github.com/doITmagic/rag-code-mcp/v2/parser"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeCrossLanguage registers line analyzers for JavaScript, html (.fkh) and
// docs (.fkd), restoring the JavaScript one after the test.
func fakeCrossLanguage(t *testing.T) {
	prev := parser.ForLanguage("javascript")
	parser.Register(lineAnalyzer{"javascript", ".js"})
	parser.Register(lineAnalyzer{"html", ".fkh"})
	parser.Register(lineAnalyzer{"docs", ".fkd"})
	t.Cleanup(func() {
		if prev != nil {
			parser.Register(prev)
		}
	})
}

// crossFiles is the page and doc of the cross-language tests: an onclick
// handler naming the browser global window.f and a doc naming util.f (both
// JavaScript files below are the module util).
var crossFiles = map[string]string{
	"page.fkh":  "func page -> window.f",
	"guide.fkd": "func guide -> util.f",
}

// crossRels runs a full index over files and returns the relations of the
// page and the doc: what an incremental run must end with.
func crossRels(t *testing.T, files map[string]string) (page, doc parser.Relation) {
	t.Helper()
	f := newEnv(t)
	for p, c := range files {
		f.write(t, p, c)
	}
	_, err := f.run(t, nil)
	require.NoError(t, err)
	return f.symbols(t, f.root+"/page.fkh")["page"].Relations[0], f.symbols(t, f.root+"/guide.fkd")["guide"].Relations[0]
}

// html and docs callers resolve across languages (onclick="f()" names a
// JavaScript f). When a files run removes one of two JavaScript definitions
// of f, those callers must turn from ambiguous to the remaining f, as in a
// full run over the final files.
func TestFilesRunCrossLanguageCallerAfterDefinitionRemoved(t *testing.T) {
	fakeCrossLanguage(t)
	e := newEnv(t)
	for p, c := range crossFiles {
		e.write(t, p, c)
	}
	e.write(t, "src/util.js", "func f")
	b := e.write(t, "lib/util.js", "func f")
	_, err := e.run(t, nil)
	require.NoError(t, err)
	require.Equal(t, parser.ResolutionAmbiguous, e.symbols(t, e.root+"/page.fkh")["page"].Relations[0].Resolution, "precondition")
	require.Equal(t, parser.ResolutionAmbiguous, e.symbols(t, e.root+"/guide.fkd")["guide"].Relations[0].Resolution, "precondition")

	require.NoError(t, os.Remove(b))
	_, err = e.run(t, func(o *Options) { o.Files = []string{}; o.Deleted = []string{b} })
	require.NoError(t, err)

	files := map[string]string{"src/util.js": "func f"}
	for p, c := range crossFiles {
		files[p] = c
	}
	wantPage, wantDoc := crossRels(t, files)
	require.NotEmpty(t, wantPage.TargetID, "full run")
	require.NotEmpty(t, wantDoc.TargetID, "full run")
	page, doc := e.symbols(t, e.root+"/page.fkh")["page"].Relations[0], e.symbols(t, e.root+"/guide.fkd")["guide"].Relations[0]
	a := e.symbols(t, e.root+"/src/util.js")["f"].ID
	assert.Equal(t, a, page.TargetID, "html caller")
	assert.Equal(t, wantPage.Resolution, page.Resolution, "html caller")
	assert.Equal(t, a, doc.TargetID, "docs caller")
	assert.Equal(t, wantDoc.Resolution, doc.Resolution, "docs caller")
}

// The reverse: a files run that adds a second JavaScript f must make the
// html and docs callers of the first one ambiguous, as a full run does.
func TestFilesRunCrossLanguageCallerAfterDefinitionAdded(t *testing.T) {
	fakeCrossLanguage(t)
	e := newEnv(t)
	for p, c := range crossFiles {
		e.write(t, p, c)
	}
	e.write(t, "src/util.js", "func f")
	_, err := e.run(t, nil)
	require.NoError(t, err)
	require.NotEmpty(t, e.symbols(t, e.root+"/page.fkh")["page"].Relations[0].TargetID, "precondition")

	b := e.write(t, "lib/util.js", "func f")
	_, err = e.run(t, func(o *Options) { o.Files = []string{b} })
	require.NoError(t, err)

	files := map[string]string{"src/util.js": "func f", "lib/util.js": "func f"}
	for p, c := range crossFiles {
		files[p] = c
	}
	wantPage, wantDoc := crossRels(t, files)
	require.Empty(t, wantPage.TargetID, "full run")
	require.Empty(t, wantDoc.TargetID, "full run")
	page, doc := e.symbols(t, e.root+"/page.fkh")["page"].Relations[0], e.symbols(t, e.root+"/guide.fkd")["guide"].Relations[0]
	assert.Equal(t, wantPage.TargetID, page.TargetID, "html caller")
	assert.Equal(t, wantPage.Resolution, page.Resolution, "html caller")
	assert.Equal(t, wantDoc.TargetID, doc.TargetID, "docs caller")
	assert.Equal(t, wantDoc.Resolution, doc.Resolution, "docs caller")
}

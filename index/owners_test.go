package index

import (
	"context"
	"os"
	"testing"

	"github.com/doITmagic/rag-code-mcp/v2/parser"
	_ "github.com/doITmagic/rag-code-mcp/v2/parser/python"
	"github.com/doITmagic/rag-code-mcp/v2/store"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Two twins (one qualified name twice in one file) are one candidate: the
// widest, then the lowest start line. When an edit shifts both twins, an
// incremental run must re-resolve their caller in another file to the same
// twin a full run picks.
func TestFilesRunTwinsShiftedPickOtherTwin(t *testing.T) {
	e := newEnv(t)
	twins := e.write(t, "d0/m0.go", "func c\n\nfunc a\nfunc a\n")
	caller := e.write(t, "d1/m3.go", "func x -> m0.a\n")
	_, err := e.run(t, nil)
	require.NoError(t, err)

	// Drop the blank line: both twins move up one line, so both get new IDs.
	e.write(t, "d0/m0.go", "func c\nfunc a\nfunc a\n")
	_, err = e.run(t, nil)
	require.NoError(t, err)

	// What a full run over the final files picks: the twin on line 2.
	f := newEnv(t)
	f.write(t, "d0/m0.go", "func c\nfunc a\nfunc a\n")
	fc := f.write(t, "d1/m3.go", "func x -> m0.a\n")
	_, err = f.run(t, nil)
	require.NoError(t, err)
	require.Equal(t, 2, f.byID(t, f.symbols(t, fc)["x"].Relations[0].TargetID).StartLine)

	first := ""
	for _, s := range e.all(t, twins) {
		if s.Name == "a" && s.StartLine == 2 {
			first = s.ID
		}
	}
	require.NotEmpty(t, first)
	rel := e.symbols(t, caller)["x"].Relations[0]
	assert.Equal(t, "exact", rel.Resolution)
	assert.Equal(t, first, rel.TargetID, "the caller must link to the first twin (line 2), as a full run does; it links to the twin on line %d", e.byID(t, rel.TargetID).StartLine)
}

func (e *env) byID(t *testing.T, id string) parser.Symbol {
	t.Helper()
	hits, err := e.store.Scroll(context.Background(), "c", store.Filter{IDs: []string{id}}, 1)
	require.NoError(t, err)
	require.Len(t, hits, 1, "no point with id %s", id)
	return hits[0].Symbol
}

var realPython = parser.ForLanguage("python")

// A function owns members too: Repo.save() on a JavaScript function Repo
// falls back to the bare name util.save. Deleting Repo, then adding it back,
// in incremental runs must unlink, then relink, the caller as a full run does.
func TestFilesRunFunctionOwnerDeletedAndAdded(t *testing.T) {
	parser.Register(realJS)
	e := newEnv(t)
	repo := e.write(t, "repo.js", "export default function Repo() {}\n")
	e.write(t, "util.js", "export function save() {}\n")
	caller := e.write(t, "caller.js", "import Repo from './repo';\nexport function run() {\n  Repo.save();\n}\n")
	_, err := e.run(t, nil)
	require.NoError(t, err)
	last := func() parser.Relation {
		rel := e.symbols(t, caller)["run"].Relations
		return rel[len(rel)-1]
	}
	require.Equal(t, "repo.default.save", last().TargetQualifiedName)
	require.Equal(t, parser.ResolutionUnique, last().Resolution, "precondition: the by-name fallback")
	linked := last().TargetID

	require.NoError(t, os.Remove(repo))
	_, err = e.run(t, func(o *Options) { o.Files = []string{}; o.Deleted = []string{repo} })
	require.NoError(t, err)
	assert.Empty(t, last().TargetID, "a full run leaves Repo.save() unresolved once Repo is gone")
	assert.Empty(t, last().Resolution)

	e.write(t, "repo.js", "export default function Repo() {}\n")
	_, err = e.run(t, func(o *Options) { o.Files = []string{repo} })
	require.NoError(t, err)
	assert.Equal(t, linked, last().TargetID)
	assert.Equal(t, parser.ResolutionUnique, last().Resolution)
}

// Repo.save() names an owner that exists but does not define save, so
// resolve falls back to the bare name: util.save (unique). Once the owner
// class is deleted, a full run leaves the call unresolved (an unknown owner
// is external); an incremental run must not keep the by-name link.
func TestFilesRunOwnerDeletedKeepsNameFallback(t *testing.T) {
	parser.Register(realPython)
	t.Cleanup(func() { parser.Register(lineAnalyzer{"python", ".py"}) })
	e := newEnv(t)
	models := e.write(t, "models.py", "class Repo:\n    def load(self):\n        pass\n")
	e.write(t, "util.py", "def save():\n    pass\n")
	caller := e.write(t, "caller.py", "from models import Repo\n\n\ndef run():\n    Repo.save()\n")
	_, err := e.run(t, nil)
	require.NoError(t, err)
	rel := e.symbols(t, caller)["run"].Relations
	require.NotEmpty(t, rel)
	require.Equal(t, "models.Repo.save", rel[len(rel)-1].TargetQualifiedName)
	require.Equal(t, "unique", rel[len(rel)-1].Resolution, "precondition: the by-name fallback")

	require.NoError(t, os.Remove(models))
	_, err = e.run(t, func(o *Options) { o.Files = []string{}; o.Deleted = []string{models} })
	require.NoError(t, err)

	rel = e.symbols(t, caller)["run"].Relations
	last := rel[len(rel)-1]
	assert.Empty(t, last.TargetID, "a full run leaves Repo.save() unresolved once class Repo is gone")
	assert.Empty(t, last.Resolution)

	// Sanity: a recreate (a full run over the same files) gives the expectation.
	_, err = e.run(t, func(o *Options) { o.Recreate = true })
	require.NoError(t, err)
	rel = e.symbols(t, caller)["run"].Relations
	require.Empty(t, rel[len(rel)-1].TargetID)
}

// The reverse: the owner class appears in a later incremental run. A full
// run then falls back to the bare name (unique); the incremental run must
// re-resolve the unchanged caller.
func TestFilesRunOwnerAddedLeavesCallerUnresolved(t *testing.T) {
	parser.Register(realPython)
	t.Cleanup(func() { parser.Register(lineAnalyzer{"python", ".py"}) })
	e := newEnv(t)
	e.write(t, "util.py", "def save():\n    pass\n")
	caller := e.write(t, "caller.py", "from models import Repo\n\n\ndef run():\n    Repo.save()\n")
	_, err := e.run(t, nil)
	require.NoError(t, err)
	rel := e.symbols(t, caller)["run"].Relations
	require.Empty(t, rel[len(rel)-1].TargetID, "precondition: unknown owner, unresolved")

	models := e.write(t, "models.py", "class Repo:\n    def load(self):\n        pass\n")
	_, err = e.run(t, func(o *Options) { o.Files = []string{models} })
	require.NoError(t, err)

	rel = e.symbols(t, caller)["run"].Relations
	last := rel[len(rel)-1]
	assert.Equal(t, "unique", last.Resolution, "a full run links Repo.save() by name once class Repo exists")
	assert.NotEmpty(t, last.TargetID)

	// Sanity: a recreate (a full run over the same files) gives the expectation.
	_, err = e.run(t, func(o *Options) { o.Recreate = true })
	require.NoError(t, err)
	rel = e.symbols(t, caller)["run"].Relations
	require.Equal(t, "unique", rel[len(rel)-1].Resolution)
}

// Child.save() resolves through Child's declared base: base.Base.save, exact.
// When Child's base changes to Other (which defines its own save), a full run
// links the unchanged caller to other.Other.save; an incremental run must
// not keep the link through the old base.
func TestFilesRunBaseChangedKeepsLinkThroughOldBase(t *testing.T) {
	parser.Register(realPython)
	t.Cleanup(func() { parser.Register(lineAnalyzer{"python", ".py"}) })
	e := newEnv(t)
	base := e.write(t, "base.py", "class Base:\n    def save(self):\n        pass\n")
	other := e.write(t, "other.py", "class Other:\n    def save(self):\n        pass\n")
	child := e.write(t, "child.py", "from base import Base\nfrom other import Other\n\n\nclass Child(Base):\n    pass\n")
	caller := e.write(t, "caller.py", "from child import Child\n\n\ndef run():\n    Child.save(None)\n")
	_, err := e.run(t, nil)
	require.NoError(t, err)
	last := func() parser.Relation {
		rel := e.symbols(t, caller)["run"].Relations
		require.NotEmpty(t, rel)
		return rel[len(rel)-1]
	}
	require.Equal(t, "child.Child.save", last().TargetQualifiedName)
	require.Equal(t, e.symbols(t, base)["save"].ID, last().TargetID, "precondition: linked through the base")

	e.write(t, "child.py", "from base import Base\nfrom other import Other\n\n\nclass Child(Other):\n    pass\n")
	_, err = e.run(t, func(o *Options) { o.Files = []string{child} })
	require.NoError(t, err)

	assert.Equal(t, e.symbols(t, other)["save"].ID, last().TargetID, "a full run links Child.save() to Other.save after the base changed")

	// Sanity: a recreate (a full run over the same files) gives the expectation.
	_, err = e.run(t, func(o *Options) { o.Recreate = true })
	require.NoError(t, err)
	require.Equal(t, e.symbols(t, other)["save"].ID, last().TargetID)
}

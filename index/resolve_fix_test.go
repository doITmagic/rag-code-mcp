package index

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/doITmagic/rag-code-mcp/v2/parser"
	"github.com/doITmagic/rag-code-mcp/v2/store"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// rel returns the first relation of the symbol named name and the id of the
// symbol named target ("" when target is "").
func (e *env) rel(t *testing.T, name, target string) (parser.Relation, string) {
	t.Helper()
	hits, err := e.store.Scroll(context.Background(), "c", store.Filter{Name: name}, store.MaxScroll)
	require.NoError(t, err)
	require.Len(t, hits, 1, name)
	require.NotEmpty(t, hits[0].Symbol.Relations, name)
	id := ""
	if target != "" {
		th, err := e.store.Scroll(context.Background(), "c", store.Filter{Name: target}, store.MaxScroll)
		require.NoError(t, err)
		require.Len(t, th, 1, target)
		id = th[0].Symbol.ID
	}
	return hits[0].Symbol.Relations[0], id
}

func TestResolveNameStaysInLanguage(t *testing.T) {
	parser.Register(lineAnalyzer{"python", ".py"})
	e := newEnv(t)
	e.write(t, "web.py", "func Render -> filterRows\n")
	e.write(t, "client.go", "func filterRows\n")
	_, err := e.run(t, nil)
	require.NoError(t, err)
	r, _ := e.rel(t, "Render", "")
	assert.Empty(t, r.TargetID, "a call never resolves into another language")

	// Same through the store lookup: only the caller is in the run.
	e.write(t, "web.py", "\nfunc Render -> filterRows\n")
	_, err = e.run(t, func(o *Options) { o.Files = []string{"web.py"} })
	require.NoError(t, err)
	r, _ = e.rel(t, "Render", "")
	assert.Empty(t, r.TargetID)
}

// A qualified target in code never resolves into another language either
// (an external Python module foo vs a foo.go file); docs and html links do
// cross on purpose.
func TestResolveQualifiedStaysInLanguage(t *testing.T) {
	parser.Register(lineAnalyzer{"python", ".py"})
	parser.Register(lineAnalyzer{"html", ".htm"})
	e := newEnv(t)
	e.write(t, "web.py", "func Render -> foo.run\n")
	e.write(t, "page.htm", "func Click -> foo.run\n")
	e.write(t, "foo.go", "func run\n")
	check := func(t *testing.T) {
		t.Helper()
		r, _ := e.rel(t, "Render", "")
		assert.Empty(t, r.TargetID, "exact match into another language")
		r, _ = e.rel(t, "Click", "")
		assert.NotEmpty(t, r.TargetID, "html calls into code")
	}
	_, err := e.run(t, nil)
	require.NoError(t, err)
	t.Run("full run", check)

	// Same through the store lookup: only the callers are in the run.
	e.write(t, "web.py", "\nfunc Render -> foo.run\n")
	e.write(t, "page.htm", "\nfunc Click -> foo.run\n")
	_, err = e.run(t, func(o *Options) { o.Files = []string{"web.py", "page.htm"} })
	require.NoError(t, err)
	t.Run("files run", check)
}

func TestResolveTargetDefinedLater(t *testing.T) {
	e := newEnv(t)
	e.write(t, "b.go", "func Caller -> a.Foo\nfunc Named -> Bar\n")
	e.write(t, "a.go", "func Other\n")
	_, err := e.run(t, nil)
	require.NoError(t, err)

	e.write(t, "a.go", "func Other\nfunc Foo\nfunc Bar\n")
	_, err = e.run(t, func(o *Options) { o.Files = []string{"a.go"} })
	require.NoError(t, err)
	r, foo := e.rel(t, "Caller", "Foo")
	assert.Equal(t, foo, r.TargetID)
	r, bar := e.rel(t, "Named", "Bar")
	assert.Equal(t, bar, r.TargetID)
	assert.Equal(t, parser.ResolutionUnique, r.Resolution)

	// A second Bar makes the name-only relation ambiguous.
	e.write(t, "c.go", "func Bar\n")
	_, err = e.run(t, func(o *Options) { o.Files = []string{"c.go"} })
	require.NoError(t, err)
	r, _ = e.rel(t, "Named", "")
	assert.Empty(t, r.TargetID)
	assert.Equal(t, parser.ResolutionAmbiguous, r.Resolution)
}

func TestResolveInheritedMemberFallsBackToName(t *testing.T) {
	e := newEnv(t)
	// c.Show.helper names the owner c.Show, which exists but does not define helper.
	e.write(t, "c.go", "func Show -> c.Show.helper\nfunc Out -> fmt.Println\n")
	e.write(t, "base.go", "func helper\nfunc Println\n")
	_, err := e.run(t, nil)
	require.NoError(t, err)
	r, helper := e.rel(t, "Show", "helper")
	assert.Equal(t, helper, r.TargetID)
	assert.Equal(t, parser.ResolutionUnique, r.Resolution)
	r, _ = e.rel(t, "Out", "")
	assert.Empty(t, r.TargetID, "an unknown owner is external, not a name guess")
}

func TestFilesRunAfterFailedRunWidens(t *testing.T) {
	e := newEnv(t)
	f1 := e.write(t, "f1.go", funcs(40, "A"))
	e.write(t, "f2.go", funcs(40, "B"))
	e.emb.before = func(context.Context, int32) error { return errors.New("ollama down") }
	_, err := e.run(t, nil)
	require.Error(t, err)

	e.emb.before = func(context.Context, int32) error { return nil }
	st, err := e.run(t, func(o *Options) { o.Files = []string{f1} })
	require.NoError(t, err)
	assert.Equal(t, "done", st.Phase)
	assert.Equal(t, 80, e.count(t, store.Filter{}), "the files run finished the failed full run")
}

func TestRunRefusesRagcodeSymlinks(t *testing.T) {
	e := newEnv(t)
	e.write(t, "a.go", "func A\n")
	victim := filepath.Join(t.TempDir(), "victim")
	require.NoError(t, os.WriteFile(victim, []byte("IMPORTANT\n"), 0o644))
	require.NoError(t, os.MkdirAll(filepath.Join(e.root, ".ragcode"), 0o755))
	require.NoError(t, os.Symlink(victim, filepath.Join(e.root, ".ragcode", ".gitignore")))
	_, err := e.run(t, nil)
	require.NoError(t, err)
	data, err := os.ReadFile(victim)
	require.NoError(t, err)
	assert.Equal(t, "IMPORTANT\n", string(data))

	// Run writes nothing under the root, so a committed .ragcode symlink
	// gets nothing through it.
	e2 := newEnv(t)
	target := t.TempDir()
	require.NoError(t, os.Symlink(target, filepath.Join(e2.root, ".ragcode")))
	_, err = e2.run(t, nil)
	require.NoError(t, err)
	entries, err := os.ReadDir(target)
	require.NoError(t, err)
	assert.Empty(t, entries)
}

func TestReadIndexFilesRefuseDevices(t *testing.T) {
	if _, err := os.Stat("/dev/zero"); err != nil {
		t.Skip("no /dev/zero")
	}
	root := t.TempDir()
	require.NoError(t, os.MkdirAll(Dir(root), 0o755))
	require.NoError(t, os.Symlink("/dev/zero", statusPath(root)))
	require.NoError(t, os.Symlink("/dev/zero", statePath(root)))
	done := make(chan struct{})
	go func() {
		defer close(done)
		_, ok := ReadStatus(root)
		assert.False(t, ok)
		_, err := LoadState(root)
		assert.Error(t, err)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("reading a /dev/zero symlink did not stop")
	}
}

// A qualified-suffix match is exact (python: a src/ layout), yet a second
// symbol with that suffix makes it ambiguous: the unchanged caller is
// re-resolved.
func TestResolveSuffixMatchTurnsAmbiguous(t *testing.T) {
	e := newEnv(t)
	e.write(t, "a.py", "func Caller -> pkg.Svc\n")
	e.write(t, "src.pkg.py", "func Svc\n")
	_, err := e.run(t, nil)
	require.NoError(t, err)
	r, svc := e.rel(t, "Caller", "Svc")
	assert.Equal(t, svc, r.TargetID)
	assert.Equal(t, parser.ResolutionExact, r.Resolution)

	e.write(t, "lib.pkg.py", "func Svc\n")
	_, err = e.run(t, func(o *Options) { o.Files = []string{"lib.pkg.py"} })
	require.NoError(t, err)
	r, _ = e.rel(t, "Caller", "")
	assert.Empty(t, r.TargetID)
	assert.Equal(t, parser.ResolutionAmbiguous, r.Resolution)
}

// A Python suffix is exact only when what it cuts off is a source root, not
// a package: the stdlib json.dumps is not src.flask.json.dumps, while
// flask.json.dumps is.
func TestResolvePythonSuffixNotInsidePackage(t *testing.T) {
	e := newEnv(t)
	e.write(t, "src/flask/__init__.py", "")
	e.write(t, "src.flask.json.py", "func dumps\n")
	e.write(t, "a.py", "func Std -> json.dumps\nfunc Own -> flask.json.dumps\n")
	_, err := e.run(t, nil)
	require.NoError(t, err)
	r, _ := e.rel(t, "Std", "")
	assert.NotEqual(t, parser.ResolutionExact, r.Resolution, "stdlib json is not the project's flask.json")
	r, dumps := e.rel(t, "Own", "dumps")
	assert.Equal(t, dumps, r.TargetID)
	assert.Equal(t, parser.ResolutionExact, r.Resolution)
}

// Outside python and gdscript a qualified suffix is a guess: PHP Status::run()
// of a global \\Status is not HealthCheck\\WP_CLI\\Status::run.
func TestResolveSuffixMatchNotExactInPHP(t *testing.T) {
	e := newEnv(t)
	e.write(t, "a.php", "func Caller -> Status.run\n")
	e.write(t, "HealthCheck.WP_CLI.Status.php", "func run\n")
	_, err := e.run(t, nil)
	require.NoError(t, err)
	r, run := e.rel(t, "Caller", "run")
	assert.Equal(t, parser.ResolutionUnique, r.Resolution)
	assert.Equal(t, run, r.TargetID)
}

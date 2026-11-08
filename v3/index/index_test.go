package index

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/doITmagic/rag-code-mcp/v3/embed"
	"github.com/doITmagic/rag-code-mcp/v3/parser"
	"github.com/doITmagic/rag-code-mcp/v3/store"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// lineAnalyzer is a self-contained stand-in for a language analyzer. Each
// "func NAME [-> TARGET]" line is a function in module <basename>; a TARGET
// with a dot is a qualified name, without one a bare name. A line "panic"
// panics, a line "error" fails.
type lineAnalyzer struct{ lang, ext string }

func (a lineAnalyzer) Language() string     { return a.lang }
func (a lineAnalyzer) Extensions() []string { return []string{a.ext} }
func (a lineAnalyzer) Analyze(f parser.File) ([]parser.Symbol, error) {
	module := strings.TrimSuffix(filepath.Base(f.Path), a.ext)
	if a.ext == ".md" {
		return parser.Finalize(f, []parser.Symbol{{Name: module, Kind: parser.KindChunk, Module: module, Content: string(f.Source)}}), nil
	}
	var syms []parser.Symbol
	for i, line := range strings.Split(string(f.Source), "\n") {
		switch {
		case line == "panic":
			panic("bad file")
		case line == "error":
			return nil, errors.New("bad file")
		case strings.HasPrefix(line, "class "): // class C : base.QN ...
			name, bases, _ := strings.Cut(strings.TrimPrefix(line, "class "), " : ")
			s := parser.Symbol{Name: name, Kind: parser.KindClass, Module: module, Content: line, StartLine: i + 1}
			for _, b := range strings.Fields(bases) {
				s.Relations = append(s.Relations, parser.Relation{Kind: parser.RelExtends, TargetName: b[strings.LastIndex(b, ".")+1:], TargetQualifiedName: b, Line: i + 1})
			}
			syms = append(syms, s)
		case strings.HasPrefix(line, "tpl "), strings.HasPrefix(line, "var "), strings.HasPrefix(line, "scene "):
			kind, name, _ := strings.Cut(line, " ")
			k := map[string]parser.Kind{"tpl": parser.KindTemplate, "var": parser.KindVariable, "scene": parser.KindScene}[kind]
			syms = append(syms, parser.Symbol{Name: name, Kind: k, Module: module, Content: line, StartLine: i + 1})
		case strings.HasPrefix(line, "func "):
			name, target, _ := strings.Cut(strings.TrimPrefix(line, "func "), " -> ")
			s := parser.Symbol{Name: name, Kind: parser.KindFunction, Module: module, Content: line, StartLine: i + 1}
			if n, ok := strings.CutSuffix(name, " as default"); ok { // a JS default export
				s.Name, s.QualifiedName = n, module+".default"
			}
			if target != "" {
				rel := parser.Relation{Kind: parser.RelCall, Line: i + 1}
				if strings.Contains(target, ".") {
					rel.TargetQualifiedName = target
					rel.TargetName = target[strings.LastIndex(target, ".")+1:]
				} else {
					rel.TargetName = target
				}
				s.Relations = []parser.Relation{rel}
			}
			syms = append(syms, s)
		}
	}
	return parser.Finalize(f, syms), nil
}

func TestMain(m *testing.M) {
	// Backstop: a test that forgets RAGCODE_HOME must not write index files
	// into the real ~/.ragcode.
	home, err := os.MkdirTemp("", "ragcode-home-")
	if err != nil {
		panic(err)
	}
	os.Setenv("RAGCODE_HOME", home)
	parser.Register(lineAnalyzer{"go", ".go"})
	parser.Register(lineAnalyzer{"markdown", ".md"})
	code := m.Run()
	os.RemoveAll(home)
	os.Exit(code)
}

// hookEmbedder runs before on every Embed call, so a test can block or fail
// a chosen call.
type hookEmbedder struct {
	embed.Fake
	calls  atomic.Int32
	before func(ctx context.Context, call int32) error
}

func (h *hookEmbedder) Embed(ctx context.Context, texts []string) ([][]float32, error) {
	if err := h.before(ctx, h.calls.Add(1)); err != nil {
		return nil, err
	}
	return h.Fake.Embed(ctx, texts)
}

type env struct {
	root  string
	store *store.Memory
	emb   *hookEmbedder
	opts  Options
}

func newEnv(t *testing.T) *env {
	t.Helper()
	e := &env{root: t.TempDir(), store: store.NewMemory(), emb: &hookEmbedder{before: func(context.Context, int32) error { return nil }}}
	e.opts = Options{Root: e.root, Collection: "c", Store: e.store, Embedder: e.emb, Exclude: []string{"vendor"}}
	return e
}

func (e *env) write(t *testing.T, rel, content string) string {
	t.Helper()
	p := filepath.Join(e.root, rel)
	require.NoError(t, os.MkdirAll(filepath.Dir(p), 0o755))
	require.NoError(t, os.WriteFile(p, []byte(content), 0o644))
	// Edits inside the same timestamp tick would be invisible to the diff.
	require.NoError(t, os.Chtimes(p, time.Now(), time.Now().Add(time.Second)))
	return p
}

func (e *env) run(t *testing.T, mod func(*Options)) (Status, error) {
	t.Helper()
	o := e.opts
	if mod != nil {
		mod(&o)
	}
	return Run(context.Background(), o)
}

func (e *env) count(t *testing.T, f store.Filter) int {
	t.Helper()
	hits, err := e.store.Scroll(context.Background(), "c", f, store.MaxScroll)
	require.NoError(t, err)
	return len(hits)
}

func (e *env) symbols(t *testing.T, path string) map[string]parser.Symbol {
	t.Helper()
	hits, err := e.store.Scroll(context.Background(), "c", store.Filter{FilePath: path}, store.MaxScroll)
	require.NoError(t, err)
	out := map[string]parser.Symbol{}
	for _, h := range hits {
		out[h.Symbol.Name] = h.Symbol
	}
	return out
}

// all is every stored symbol of path, including those sharing a name.
func (e *env) all(t *testing.T, path string) []parser.Symbol {
	t.Helper()
	hits, err := e.store.Scroll(context.Background(), "c", store.Filter{FilePath: path}, store.MaxScroll)
	require.NoError(t, err)
	out := make([]parser.Symbol, 0, len(hits))
	for _, h := range hits {
		out = append(out, h.Symbol)
	}
	return out
}

func funcs(n int, prefix string) string {
	var b strings.Builder
	for i := 0; i < n; i++ {
		fmt.Fprintf(&b, "func %s%d\n", prefix, i)
	}
	return b.String()
}

func TestRunFull(t *testing.T) {
	e := newEnv(t)
	a := e.write(t, "a.go", "func A1\nfunc A2\n")
	e.write(t, "docs/readme.md", "# hi")
	e.write(t, "vendor/v.go", "func V")
	e.write(t, ".env", "SECRET=1")
	e.write(t, "notes.txt", "no analyzer")

	var progress []Status
	st, err := e.run(t, func(o *Options) { o.Progress = func(s Status) { progress = append(progress, s) } })
	require.NoError(t, err)
	assert.Equal(t, "done", st.Phase)
	assert.Equal(t, 2, st.Total)
	assert.Equal(t, 2, st.Done)
	assert.Equal(t, 0, st.Failed)
	assert.False(t, st.EndedAt.IsZero())
	assert.Equal(t, 3, e.count(t, store.Filter{}))
	assert.NotEmpty(t, progress)

	state, err := LoadState(e.root)
	require.NoError(t, err)
	assert.Len(t, state.Files, 2)
	assert.Len(t, state.Files[a].IDs, 2)

	saved, ok := ReadStatus(e.root)
	require.True(t, ok)
	assert.Equal(t, "done", saved.Phase)
	assert.Equal(t, 2, saved.Done)

	// A second full run is a no-op.
	st, err = e.run(t, nil)
	require.NoError(t, err)
	assert.Equal(t, 0, st.Total)
	assert.Equal(t, 3, e.count(t, store.Filter{}))
}

func TestRunIncrementalEdit(t *testing.T) {
	e := newEnv(t)
	a := e.write(t, "a.go", "func A1\nfunc A2\n")
	b := e.write(t, "b.go", "func B1\n")
	_, err := e.run(t, nil)
	require.NoError(t, err)

	e.write(t, "a.go", "func A1\nfunc A3\nfunc A4\n")
	for name, files := range map[string][]string{"full": nil, "files": {a}} {
		t.Run(name, func(t *testing.T) {
			st, err := e.run(t, func(o *Options) { o.Files = files })
			require.NoError(t, err)
			assert.Equal(t, 1, st.Total)
			assert.Equal(t, 1, st.Done)
			syms := e.symbols(t, a)
			assert.ElementsMatch(t, []string{"A1", "A3", "A4"}, keys(syms))
			assert.Len(t, e.symbols(t, b), 1)
			// Re-run with the same edit is a no-op: the state has the new mtime.
			st, err = e.run(t, func(o *Options) { o.Files = files })
			require.NoError(t, err)
			assert.Equal(t, 0, st.Total)
			e.write(t, "a.go", "func A1\nfunc A3\nfunc A4\n")
		})
	}
}

func TestRunDelete(t *testing.T) {
	e := newEnv(t)
	a := e.write(t, "a.go", "func A1\n")
	b := e.write(t, "b.go", "func B1\n")
	_, err := e.run(t, nil)
	require.NoError(t, err)
	require.Equal(t, 2, e.count(t, store.Filter{}))

	t.Run("deleted option", func(t *testing.T) {
		require.NoError(t, os.Remove(b))
		st, err := e.run(t, func(o *Options) { o.Files = []string{}; o.Deleted = []string{b} })
		require.NoError(t, err)
		assert.Equal(t, 1, st.Deleted)
		assert.Equal(t, 0, st.Total)
		assert.Equal(t, 0, e.count(t, store.Filter{FilePath: b}))
		state, _ := LoadState(e.root)
		assert.NotContains(t, state.Files, b)
		assert.Contains(t, state.Files, a)
	})
	t.Run("full walk notices", func(t *testing.T) {
		require.NoError(t, os.Remove(a))
		st, err := e.run(t, nil)
		require.NoError(t, err)
		assert.Equal(t, 1, st.Deleted)
		assert.Equal(t, 0, e.count(t, store.Filter{}))
		state, _ := LoadState(e.root)
		assert.Empty(t, state.Files)
	})
	t.Run("file listed but gone", func(t *testing.T) {
		c := e.write(t, "c.go", "func C1\n")
		_, err := e.run(t, func(o *Options) { o.Files = []string{c} })
		require.NoError(t, err)
		require.NoError(t, os.Remove(c))
		st, err := e.run(t, func(o *Options) { o.Files = []string{c} })
		require.NoError(t, err)
		assert.Equal(t, 1, st.Deleted)
		assert.Equal(t, 0, e.count(t, store.Filter{}))
	})
}

func TestRunEmbedFailureKeepsStateConsistent(t *testing.T) {
	e := newEnv(t)
	f1 := e.write(t, "f1.go", funcs(30, "A"))
	f2 := e.write(t, "f2.go", funcs(30, "B"))
	f3 := e.write(t, "f3.go", funcs(10, "C"))
	// 70 symbols: embed calls 1-4 cover the first 64 (one upsert), call 5
	// fails; the run does not retry (the embedder owns transport retries).
	e.emb.before = func(_ context.Context, call int32) error {
		if call >= 5 {
			return errors.New("ollama down")
		}
		return nil
	}
	st, err := e.run(t, nil)
	require.EqualError(t, err, "ollama down")
	assert.Equal(t, "failed", st.Phase)
	assert.Equal(t, "ollama down", st.Error)
	assert.Equal(t, int32(5), e.emb.calls.Load(), "no retry in the run")
	assert.Equal(t, 2, st.Done)

	state, err := LoadState(e.root)
	require.NoError(t, err)
	assert.ElementsMatch(t, []string{f1, f2}, keys(state.Files))
	for path, entry := range state.Files {
		assert.Equal(t, len(entry.IDs), e.count(t, store.Filter{FilePath: path}), path)
	}
	saved, ok := ReadStatus(e.root)
	require.True(t, ok)
	assert.Equal(t, "failed", saved.Phase)

	// The next run only redoes the file that did not make it.
	e.emb.before = func(context.Context, int32) error { return nil }
	st, err = e.run(t, nil)
	require.NoError(t, err)
	assert.Equal(t, 1, st.Total)
	assert.Equal(t, 10, e.count(t, store.Filter{FilePath: f3}))
	assert.Equal(t, 70, e.count(t, store.Filter{}))
}

func TestRunParseFailures(t *testing.T) {
	e := newEnv(t)
	e.write(t, "ok.go", "func A\n")
	e.write(t, "boom.go", "panic\n")
	e.write(t, "bad.go", "error\n")
	st, err := e.run(t, nil)
	require.NoError(t, err)
	assert.Equal(t, 3, st.Total)
	assert.Equal(t, 1, st.Done)
	assert.Equal(t, 2, st.Failed)
	assert.Equal(t, 1, e.count(t, store.Filter{}))
}

func TestRunCancel(t *testing.T) {
	e := newEnv(t)
	e.write(t, "a.go", "func A\n")
	e.emb.before = func(ctx context.Context, _ int32) error {
		<-ctx.Done()
		return ctx.Err()
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := Run(ctx, e.opts)
		done <- err
	}()
	time.AfterFunc(50*time.Millisecond, cancel)
	select {
	case err := <-done:
		assert.ErrorIs(t, err, context.Canceled)
	case <-time.After(time.Second):
		t.Fatal("run did not stop within 1 s of cancel")
	}
	state, _ := LoadState(e.root)
	assert.Empty(t, state.Files)
}

func TestRunHeadChanged(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	e := newEnv(t)
	git := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", e.root}, args...)...)
		cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
		out, err := cmd.CombinedOutput()
		require.NoError(t, err, string(out))
	}
	git("init", "-q", "-b", "main")
	e.write(t, "a.go", "func A\n")
	git("add", ".")
	git("commit", "-qm", "one")

	e.emb.before = func(context.Context, int32) error {
		e.write(t, "b.go", "func B\n")
		git("add", ".")
		git("commit", "-qm", "two")
		return nil
	}
	st, err := e.run(t, nil)
	assert.ErrorIs(t, err, ErrHeadChanged)
	assert.Equal(t, "main", st.Branch)
}

func TestResolution(t *testing.T) {
	e := newEnv(t)
	a := e.write(t, "a.go", strings.Join([]string{
		"func Foo -> b.Bar",     // exact
		"func Baz -> Dup",       // ambiguous: b.Dup and c.Dup
		"func Qux -> Bar",       // unique by name
		"func Nope -> zzz.Nope", // unresolved
		"func Nope2 -> Nope3",   // unresolved
		"func Many -> b.Dup",    // exact even though the name is ambiguous
		"func Sfx -> pkg.Svc",   // unique by qualified suffix: src.pkg.Svc
	}, "\n"))
	b := e.write(t, "b.go", "func Bar\nfunc Dup\n")
	e.write(t, "c.go", "func Dup\n")
	svc := e.write(t, "src.pkg.go", "func Svc\n")

	check := func(t *testing.T) {
		t.Helper()
		bar := e.symbols(t, b)["Bar"]
		dup := e.symbols(t, b)["Dup"]
		syms := e.symbols(t, a)
		rel := func(name string) parser.Relation { return syms[name].Relations[0] }
		assert.Equal(t, parser.Relation{Kind: parser.RelCall, TargetName: "Bar", TargetQualifiedName: "b.Bar", TargetID: bar.ID, Resolution: parser.ResolutionExact, Line: 1}, rel("Foo"))
		assert.Equal(t, parser.ResolutionAmbiguous, rel("Baz").Resolution)
		assert.Empty(t, rel("Baz").TargetID)
		assert.Equal(t, parser.ResolutionUnique, rel("Qux").Resolution)
		assert.Equal(t, bar.ID, rel("Qux").TargetID)
		assert.Empty(t, rel("Nope").Resolution)
		assert.Empty(t, rel("Nope2").Resolution)
		assert.Equal(t, parser.ResolutionExact, rel("Many").Resolution)
		assert.Equal(t, dup.ID, rel("Many").TargetID)
		assert.Equal(t, parser.ResolutionUnique, rel("Sfx").Resolution)
		assert.Equal(t, e.symbols(t, svc)["Svc"].ID, rel("Sfx").TargetID)
	}
	_, err := e.run(t, nil)
	require.NoError(t, err)
	t.Run("full run", check)

	// Only a.go is re-parsed: b and c come from the store.
	e.write(t, "a.go", strings.Join([]string{
		"func Foo -> b.Bar", "func Baz -> Dup", "func Qux -> Bar", "func Nope -> zzz.Nope", "func Nope2 -> Nope3", "func Many -> b.Dup", "func Sfx -> pkg.Svc",
	}, "\n")+"\n")
	st, err := e.run(t, func(o *Options) { o.Files = []string{a} })
	require.NoError(t, err)
	require.Equal(t, 1, st.Total)
	t.Run("incremental run", check)
}

// A call never binds to a template, scene node or doc chunk, and by name
// only to something callable; a scene node sharing a class's qualified name
// does not make the class ambiguous.
func TestResolutionTargetKinds(t *testing.T) {
	e := newEnv(t)
	src := strings.Join([]string{
		"func Caller -> default", // only a template is named default
		"func Mover -> size",     // only a variable is named size
		"func Ext -> g.Gp",       // g.Gp: a function and a scene node
		"func Reader -> g.cfg",   // exact: a variable may hold a function
	}, "\n") + "\n"
	a := e.write(t, "a.go", src)
	e.write(t, "t.go", "tpl default\nvar size\n")
	g := e.write(t, "g.go", "func Gp\nscene Gp\nvar cfg\n")
	check := func(t *testing.T) {
		t.Helper()
		syms := e.symbols(t, a)
		rel := func(name string) parser.Relation { return syms[name].Relations[0] }
		assert.Empty(t, rel("Caller").TargetID, "call bound to a template")
		assert.Empty(t, rel("Caller").Resolution)
		assert.Empty(t, rel("Mover").TargetID, "call bound by name to a variable")
		var fn, cfg string
		for _, s := range e.all(t, g) {
			switch {
			case s.Name == "Gp" && s.Kind == parser.KindFunction:
				fn = s.ID
			case s.Name == "cfg":
				cfg = s.ID
			}
		}
		assert.Equal(t, fn, rel("Ext").TargetID)
		assert.Equal(t, parser.ResolutionExact, rel("Ext").Resolution)
		assert.Equal(t, cfg, rel("Reader").TargetID)
	}
	_, err := e.run(t, nil)
	require.NoError(t, err)
	t.Run("full run", check)
	e.write(t, "a.go", src+"\n")
	_, err = e.run(t, func(o *Options) { o.Files = []string{a} })
	require.NoError(t, err)
	t.Run("incremental run", check)
}

// $this->m() / self.m() naming a member the owner class does not define
// resolves through the owner's declared bases (parent class, trait) even
// when the bare name is ambiguous.
func TestResolutionThroughBases(t *testing.T) {
	e := newEnv(t)
	src := "class C : t.T\nfunc C.go -> a.C.search\n"
	a := e.write(t, "a.go", src)
	tf := e.write(t, "t.go", "func T.search\n")
	e.write(t, "u.go", "func U.search\n")
	check := func(t *testing.T) {
		t.Helper()
		r := e.symbols(t, a)["C.go"].Relations[0]
		assert.Equal(t, e.symbols(t, tf)["T.search"].ID, r.TargetID)
		assert.Equal(t, parser.ResolutionExact, r.Resolution)
	}
	_, err := e.run(t, nil)
	require.NoError(t, err)
	t.Run("full run", check)
	e.write(t, "a.go", src+"\n")
	_, err = e.run(t, func(o *Options) { o.Files = []string{a} })
	require.NoError(t, err)
	t.Run("incremental run", check)
}

func TestSkip(t *testing.T) {
	root := t.TempDir()
	mk := func(name string, content []byte) string {
		p := filepath.Join(root, name)
		require.NoError(t, os.MkdirAll(filepath.Dir(p), 0o755))
		require.NoError(t, os.WriteFile(p, content, 0o644))
		return p
	}
	big := mk("big.go", nil)
	require.NoError(t, os.Truncate(big, maxFileSize+1))
	cases := []struct {
		name   string
		path   string
		reason string
	}{
		{"source", mk("ok.go", []byte("func A")), ""},
		{"markdown", mk("README.md", []byte("# x")), ""},
		{"no analyzer", mk("notes.txt", []byte("x")), "no analyzer"},
		{"over 256 KiB", big, "over 256 KiB"},
		{"binary", mk("bin.go", []byte("abc\x00def")), "binary"},
		{"min.js", mk("app.min.js", []byte("x")), "minified"},
		{"min.css", mk("app.min.css", []byte("x")), "minified"},
		{"map", mk("app.js.map", []byte("x")), "minified"},
		{"package-lock", mk("package-lock.json", []byte("{}")), "lockfile"},
		{"go.sum", mk("go.sum", []byte("x")), "lockfile"},
		{"Cargo.lock", mk("Cargo.lock", []byte("x")), "lockfile"},
		{".env", mk(".env", []byte("x")), "secret"},
		{".env.local", mk(".env.local", []byte("x")), "secret"},
		{"pem", mk("cert.pem", []byte("x")), "secret"},
		{"key", mk("server.key", []byte("x")), "secret"},
		{"p12", mk("a.p12", []byte("x")), "secret"},
		{"id_rsa.pub", mk("id_rsa.pub", []byte("x")), "secret"},
		{"credentials.json", mk("credentials.json", []byte("{}")), "secret"},
		{"secrets.yaml", mk("secrets.yaml", []byte("x")), "secret"},
		{"Secrets.TOML", mk("Secrets.TOML", []byte("x")), "secret"},
		{".npmrc", mk(".npmrc", []byte("x")), "secret"},
		{".pypirc", mk(".pypirc", []byte("x")), "secret"},
		{".netrc", mk(".netrc", []byte("x")), "secret"},
		{"credentials.go is code", mk("credentials.go", []byte("func A")), ""},
		{"secrets.md is docs", mk("secrets.md", []byte("# x")), ""},
		{"directory", filepath.Join(root, "dir"), "not a regular file"},
	}
	require.NoError(t, os.Mkdir(filepath.Join(root, "dir"), 0o755))
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			info, err := os.Stat(c.path)
			require.NoError(t, err)
			assert.Equal(t, c.reason, Skip(c.path, info, readHead(c.path)))
		})
	}
}

func TestWalk(t *testing.T) {
	e := newEnv(t)
	want := []string{
		e.write(t, "a.go", "func A"),
		e.write(t, "docs/guide.md", "# g"),
		e.write(t, "src/deep/x.go", "func X"),
	}
	e.write(t, "vendor/v.go", "func V")
	e.write(t, "src/vendor/w.go", "func W")
	e.write(t, ".git/config.md", "# hidden")
	e.write(t, "node_modules/n.go", "func N")
	e.write(t, "src/app.min.js", "x")
	e.write(t, "src/bin.go", "a\x00b")
	e.write(t, "src/notes.txt", "x")

	got, err := Walk(e.root, []string{"vendor", "node_modules"})
	require.NoError(t, err)
	assert.Equal(t, want, got)

	_, err = Walk(filepath.Join(e.root, "missing"), nil)
	assert.Error(t, err)
}

func TestEmbedText(t *testing.T) {
	s := &parser.Symbol{Kind: parser.KindFunction, QualifiedName: "m.F", Signature: "func F()", Doc: "doc", Content: "body"}
	assert.Equal(t, "function m.F\nfunc F()\ndoc\nbody", embedText(s))

	s.Content = strings.Repeat("é", 2000)
	got := embedText(s)
	assert.LessOrEqual(t, len(got), maxEmbedText)
	assert.True(t, utf8.ValidString(got))

	// Long Content is cut exactly as the full text used to be: the 26-byte
	// header leaves 1974 bytes, 987 two-byte runes.
	assert.Equal(t, "function m.F\nfunc F()\ndoc\n"+strings.Repeat("é", 987), got)
	s.Content = strings.Repeat("x", 1<<20)
	assert.Equal(t, "function m.F\nfunc F()\ndoc\n"+strings.Repeat("x", 1974), embedText(s))
	s.Content = strings.Repeat("é", 986) + "x" + strings.Repeat("é", 1000) // byte 1974 of Content is a continuation byte
	assert.Equal(t, "function m.F\nfunc F()\ndoc\n"+strings.Repeat("é", 986)+"x", embedText(s))
}

// A path named in Options.Deleted is purged from the store even when
// state.json never recorded it (interrupted run, lost state).
func TestRunDeletedPurgesUnrecordedFile(t *testing.T) {
	e := newEnv(t)
	a := e.write(t, "a.go", "func A1\n")
	_, err := e.run(t, nil)
	require.NoError(t, err)
	stray := filepath.Join(e.root, "stray.go")
	require.NoError(t, e.store.Upsert(context.Background(), "c", []store.Item{{Vector: make([]float32, 8), Symbol: parser.Symbol{ID: "stray", Name: "S", FilePath: stray}}}))
	require.Equal(t, 1, e.count(t, store.Filter{FilePath: stray}))

	ts := &traceStore{Memory: e.store}
	st, err := e.run(t, func(o *Options) { o.Store = ts; o.Files = []string{}; o.Deleted = []string{stray} })
	require.NoError(t, err)
	assert.Equal(t, 1, st.Deleted)
	assert.Contains(t, ts.ops, "delete:stray.go")
	assert.Equal(t, 0, e.count(t, store.Filter{FilePath: stray}))
	assert.Equal(t, 1, e.count(t, store.Filter{FilePath: a}))
}

// An upsert batch is also cut by Content bytes: four 3 MiB single-symbol
// files never travel in one 12 MiB message.
func TestRunUpsertBatchByteBudget(t *testing.T) {
	defer func(old int64) { batchBytes = old }(batchBytes)
	batchBytes = 1 << 40 // one batch: only the upsert byte budget cuts
	e := newEnv(t)
	for i := range 50 {
		e.write(t, fmt.Sprintf("f%02d.md", i), strings.Repeat("x", 200<<10))
	}
	ts := &traceStore{Memory: e.store}
	st, err := e.run(t, func(o *Options) { o.Store = ts })
	require.NoError(t, err)
	assert.Equal(t, 50, st.Done)
	assert.Equal(t, 41, ts.maxUpsert, "41 x 200 KiB crosses the 8 MiB budget before the 64-point batch")
	assert.Equal(t, 50, e.count(t, store.Filter{}))
}

func keys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

// A files-only run into a missing collection walks the whole tree, or the
// index stays partial forever.
func TestRunFreshCollectionIgnoresFiles(t *testing.T) {
	e := newEnv(t)
	a := e.write(t, "a.go", "func A\n")
	e.write(t, "b.go", "func B\n")
	st, err := e.run(t, func(o *Options) { o.Files = []string{a} })
	require.NoError(t, err)
	assert.Equal(t, 2, st.Total)
	assert.Equal(t, 2, e.count(t, store.Filter{}))
}

type cancelStore struct {
	*store.Memory
	cancel context.CancelFunc
}

func (c cancelStore) Upsert(context.Context, string, []store.Item) error {
	c.cancel()
	return errors.New("rpc error: code = Canceled desc = context canceled")
}

// A store error after shutdown is an interrupted run, so Resume restarts it.
func TestRunStoreErrorAfterCancelIsInterrupted(t *testing.T) {
	e := newEnv(t)
	e.write(t, "a.go", "func A\n")
	ctx, cancel := context.WithCancel(context.Background())
	o := e.opts
	o.Store = cancelStore{e.store, cancel}
	st, err := Run(ctx, o)
	assert.ErrorIs(t, err, context.Canceled)
	assert.Equal(t, "interrupted", st.Phase)
}

func TestRunFilesSkipsSymlink(t *testing.T) {
	e := newEnv(t)
	e.write(t, "a.go", "func A\n")
	_, err := e.run(t, nil)
	require.NoError(t, err)
	secret := filepath.Join(t.TempDir(), "secret.md")
	require.NoError(t, os.WriteFile(secret, []byte("token"), 0o644))
	link := filepath.Join(e.root, "notes.md")
	require.NoError(t, os.Symlink(secret, link))
	st, err := e.run(t, func(o *Options) { o.Files = []string{link} })
	require.NoError(t, err)
	assert.Equal(t, 0, st.Total)
	assert.Equal(t, 0, e.count(t, store.Filter{FilePath: link}))
}

func TestWalkPrunesLinkedWorktree(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	e := newEnv(t)
	a := e.write(t, "a.go", "func A\n")
	git := func(args ...string) {
		out, err := exec.Command("git", append([]string{"-C", e.root, "-c", "user.name=t", "-c", "user.email=t@t"}, args...)...).CombinedOutput()
		require.NoError(t, err, string(out))
	}
	git("init", "-q")
	git("add", "a.go")
	git("commit", "-qm", "init")
	git("worktree", "add", "-q", "wt/feature")
	require.True(t, IsLinkedWorktree(filepath.Join(e.root, "wt", "feature")))
	assert.False(t, IsLinkedWorktree(e.root))
	files, err := Walk(e.root, nil)
	require.NoError(t, err)
	assert.Equal(t, []string{a}, files)
}

// Files git ignores are left out, by any .gitignore in the tree, also when
// the watcher names one explicitly; a tracked file stays even if a pattern
// matches it.
func TestWalkSkipsGitIgnored(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	e := newEnv(t)
	a := e.write(t, "a.go", "package a\nfunc A() {}\n")
	e.write(t, ".gitignore", "lucrari/\n*.gen.go\n")
	e.write(t, "sub/.gitignore", "c.go\n")
	b := e.write(t, "lucrari/b.go", "package b\nfunc B() {}\n")
	e.write(t, "sub/c.go", "package c\nfunc C() {}\n")
	g := e.write(t, "kept.gen.go", "package a\nfunc G() {}\n")
	git := func(args ...string) {
		out, err := exec.Command("git", append([]string{"-C", e.root}, args...)...).CombinedOutput()
		require.NoError(t, err, string(out))
	}
	git("init", "-q")
	git("add", "-f", "kept.gen.go")
	// A nested repository the parent ignores is a separate project: its
	// own rules apply (e.g. a legacy app kept as a nested repo).
	e.write(t, ".gitignore", "lucrari/\n*.gen.go\nlegacy/\n")
	x := e.write(t, "legacy/x.go", "package x\nfunc X() {}\n")
	e.write(t, "legacy/.gitignore", "y.go\n")
	e.write(t, "legacy/y.go", "package x\nfunc Y() {}\n")
	git("-C", "legacy", "init", "-q")
	files, err := Walk(e.root, nil)
	require.NoError(t, err)
	assert.Equal(t, []string{a, g, x}, files)

	_, err = e.run(t, func(o *Options) { o.Files = []string{b} })
	require.NoError(t, err)
	assert.Zero(t, e.count(t, store.Filter{FilePath: b}))
}

func TestRunRecreate(t *testing.T) {
	e := newEnv(t)
	e.write(t, "a.go", "func A\n")
	_, err := e.run(t, nil)
	require.NoError(t, err)
	stray := parser.Symbol{ID: "stray", Name: "Stray", FilePath: "gone.go"}
	require.NoError(t, e.store.Upsert(context.Background(), "c", []store.Item{{Vector: make([]float32, 8), Symbol: stray}}))
	st, err := e.run(t, func(o *Options) { o.Recreate = true })
	require.NoError(t, err)
	assert.Equal(t, 1, st.Total, "everything is re-embedded")
	assert.Equal(t, 1, e.count(t, store.Filter{}), "the old collection was dropped")
	assert.True(t, st.Recreated)
}

// dropStore checks, at the moment the collection is dropped, what a kill -9
// right then would leave on disk.
type dropStore struct {
	*store.Memory
	root  string
	files int
}

func (d *dropStore) DeleteCollection(ctx context.Context, name string) error {
	st, _ := LoadState(d.root)
	d.files = len(st.Files)
	return d.Memory.DeleteCollection(ctx, name)
}

// A crash between dropping the collection and saving the new state must not
// leave a state.json listing files the store no longer has: incremental runs
// would then see them as unchanged and never re-embed them.
func TestRunRecreateSavesEmptyStateBeforeDrop(t *testing.T) {
	e := newEnv(t)
	e.write(t, "a.go", "func A\n")
	_, err := e.run(t, nil)
	require.NoError(t, err)
	ds := &dropStore{Memory: e.store, root: e.root, files: -1}
	_, err = e.run(t, func(o *Options) { o.Recreate = true; o.Store = ds })
	require.NoError(t, err)
	assert.Equal(t, 0, ds.files, "state.json on disk when the collection is dropped")
}

// An unchanged caller re-resolved because its callee moved must not be
// forgotten when the run fails after the callee's file was stored: the retry
// still repairs the caller's edge.
func TestRunEmbedFailureKeepsStaleCallerQueued(t *testing.T) {
	e := newEnv(t)
	a := e.write(t, "a.go", "func Caller -> b.Target\n")
	b := e.write(t, "b.go", "func Target\n"+funcs(63, "P"))
	_, err := e.run(t, nil)
	require.NoError(t, err)

	// Target moves a line down: new ID. b.go's 64 symbols fill embed calls
	// 1-4 and one upsert; call 5, the stale caller a.go, fails.
	e.write(t, "b.go", "\nfunc Target\n"+funcs(63, "P"))
	e.emb.calls.Store(0)
	e.emb.before = func(_ context.Context, call int32) error {
		if call >= 5 {
			return errors.New("ollama down")
		}
		return nil
	}
	_, err = e.run(t, nil)
	require.EqualError(t, err, "ollama down")

	e.emb.before = func(context.Context, int32) error { return nil }
	_, err = e.run(t, nil)
	require.NoError(t, err)
	target := e.symbols(t, b)["Target"]
	require.Equal(t, 2, target.StartLine)
	assert.Equal(t, target.ID, e.symbols(t, a)["Caller"].Relations[0].TargetID)
}

// A crash after a recreate saved its empty state but before the drop leaves
// points that no state lists: the next run must not keep the ones of files
// that are gone (deleted or newly ignored) searchable.
func TestRunCrashBeforeDropForgetsGoneFiles(t *testing.T) {
	e := newEnv(t)
	e.write(t, "a.go", "func A\n")
	b := e.write(t, "b.go", "func B\n")
	_, err := e.run(t, nil)
	require.NoError(t, err)
	require.NoError(t, os.Remove(b))
	// What the crash leaves: the empty state, the old collection, a status not done.
	st := newState(e.root)
	st.Model, st.Format = e.emb.Model(), Format
	require.NoError(t, st.Save())
	(&run{root: e.root, status: Status{Root: e.root, Phase: "embedding"}}).writeStatus()

	_, err = e.run(t, nil)
	require.NoError(t, err)
	assert.Zero(t, e.count(t, store.Filter{FilePath: b}), "points of the deleted file")
	assert.Equal(t, 1, e.count(t, store.Filter{}))
}

// A collection emptied behind the state's back (an automatic recreate on a
// new dimension that failed half way, a crash after it, a manual delete) is
// re-embedded instead of being reported done while empty.
func TestRunEmptyCollectionWithStaleState(t *testing.T) {
	e := newEnv(t)
	e.write(t, "a.go", "func A\n")
	e.write(t, "b.go", "func B\n")
	_, err := e.run(t, nil)
	require.NoError(t, err)
	ctx := context.Background()
	require.NoError(t, e.store.DeleteCollection(ctx, "c"))
	_, err = e.store.EnsureCollection(ctx, "c", 8)
	require.NoError(t, err)
	st, err := e.run(t, nil)
	require.NoError(t, err)
	assert.Equal(t, 2, st.Total, "every file re-embedded")
	assert.Equal(t, 2, e.count(t, store.Filter{}))
}

type namedEmbedder struct {
	embed.Embedder
	name string
}

func (n namedEmbedder) Model() string { return n.name }

// A different model with the same dimension would mix incompatible vectors in
// one collection: the run becomes a recreate.
func TestRunModelChangeRecreates(t *testing.T) {
	e := newEnv(t)
	e.write(t, "a.go", "func A\n")
	e.write(t, "b.go", "func B\n")
	_, err := e.run(t, nil)
	require.NoError(t, err)
	st, err := e.run(t, func(o *Options) { o.Embedder = namedEmbedder{o.Embedder, "other-model"} })
	require.NoError(t, err)
	assert.Equal(t, 2, st.Total, "re-embedded with the new model")
	again, err := e.run(t, func(o *Options) { o.Embedder = namedEmbedder{o.Embedder, "other-model"} })
	require.NoError(t, err)
	assert.Equal(t, 0, again.Total, "same model: nothing to do")
}

// A state written by an older parser format re-indexes everything once, so a
// parser fix reaches files that did not change.
func TestRunFormatChangeRecreates(t *testing.T) {
	e := newEnv(t)
	e.write(t, "a.go", "func A\n")
	e.write(t, "b.go", "func B\n")
	_, err := e.run(t, nil)
	require.NoError(t, err)
	st, err := LoadState(e.root)
	require.NoError(t, err)
	st.Format = Format - 1
	require.NoError(t, st.Save())
	again, err := e.run(t, nil)
	require.NoError(t, err)
	assert.Equal(t, 2, again.Total, "re-indexed with the new format")
	last, err := e.run(t, nil)
	require.NoError(t, err)
	assert.Equal(t, 0, last.Total, "same format: nothing to do")
}

// The index files live under the global home, so a project gets nothing in
// its tree from a run.
func TestRunKeepsStateOutOfRoot(t *testing.T) {
	t.Setenv("RAGCODE_HOME", t.TempDir())
	e := newEnv(t)
	e.write(t, "a.go", "func A\n")
	_, err := e.run(t, nil)
	require.NoError(t, err)
	assert.FileExists(t, filepath.Join(Dir(e.root), "state.json"))
	assert.FileExists(t, filepath.Join(Dir(e.root), "index_status.json"))
	assert.NoDirExists(t, filepath.Join(e.root, ".ragcode"))
	st, ok := ReadStatus(e.root)
	require.True(t, ok)
	assert.Equal(t, "done", st.Phase)
}

// A branch switch moves symbols in more files than one store lookup per
// file or per id could cover: an unchanged caller must still be re-resolved
// to the new target ids, none may dangle.
func TestRunManyMovedTargetsKeepCallersResolved(t *testing.T) {
	e := newEnv(t)
	n := maxLookups + 100
	for i := 0; i < n; i++ { // three callers per target: a batch of ids hits more than MaxScroll points
		e.write(t, fmt.Sprintf("t%d.go", i), fmt.Sprintf("func T%d\n", i))
		e.write(t, fmt.Sprintf("c%d.go", i), fmt.Sprintf("func A%d -> t%d.T%d\nfunc B%d -> t%d.T%d\nfunc C%d -> t%d.T%d\n", i, i, i, i, i, i, i, i, i))
	}
	_, err := e.run(t, nil)
	require.NoError(t, err)

	for i := 0; i < n; i++ { // every target moves one line down: new ids
		e.write(t, fmt.Sprintf("t%d.go", i), fmt.Sprintf("\nfunc T%d\n", i))
	}
	_, err = e.run(t, nil)
	require.NoError(t, err)

	dangling := 0
	for i := 0; i < n; i++ {
		for _, s := range e.symbols(t, filepath.Join(e.root, fmt.Sprintf("c%d.go", i))) {
			for _, rel := range s.Relations {
				if e.count(t, store.Filter{IDs: []string{rel.TargetID}}) != 1 {
					dangling++
				}
			}
		}
	}
	assert.Zero(t, dangling, "callers left with stale target ids")
}

// A files-only run whose stale referrer was deleted from disk meanwhile (a
// branch switch the run has not reconciled yet) purges it: not a parse failure.
func TestRunPurgesVanishedReferrer(t *testing.T) {
	e := newEnv(t)
	a := e.write(t, "a.go", "func A -> b.B\n")
	b := e.write(t, "b.go", "func B\n")
	_, err := e.run(t, nil)
	require.NoError(t, err)

	require.NoError(t, os.Remove(a))
	e.write(t, "b.go", "\nfunc B\n")
	st, err := e.run(t, func(o *Options) { o.Files = []string{b} })
	require.NoError(t, err)
	assert.Zero(t, st.Failed)
	assert.Equal(t, 1, st.Deleted)
	assert.Zero(t, e.count(t, store.Filter{FilePath: a}))
	state, err := LoadState(e.root)
	require.NoError(t, err)
	assert.NotContains(t, state.Files, a)
}

// A checkout rewrites files while a run indexes them: a target changed or
// removed between the name pass and its own batch must not leave a caller,
// resolved against the first parse, pointing at an id that was never stored,
// past the next run.
func TestRunTargetChangedBetweenPasses(t *testing.T) {
	for name, change := range map[string]func(t *testing.T, e *env, b string){
		"moved":   func(t *testing.T, e *env, b string) { e.write(t, "b.go", "\nfunc B\n") },
		"removed": func(t *testing.T, e *env, b string) { require.NoError(t, os.Remove(b)) },
	} {
		t.Run(name, func(t *testing.T) {
			defer func(old int64) { batchBytes = old }(batchBytes)
			batchBytes = 1 // one file per batch, in path order: a.go before b.go
			e := newEnv(t)
			a := e.write(t, "a.go", "func A -> B\n")
			b := e.write(t, "b.go", "func B\n")
			e.emb.before = func(_ context.Context, call int32) error {
				if call == 1 { // embedding a.go: b.go is not parsed for storage yet
					change(t, e, b)
				}
				return nil
			}
			_, err := e.run(t, nil)
			require.NoError(t, err)
			e.emb.before = func(context.Context, int32) error { return nil }
			_, err = e.run(t, nil)
			require.NoError(t, err)

			for _, rel := range e.symbols(t, a)["A"].Relations {
				if rel.TargetID != "" {
					assert.Equal(t, 1, e.count(t, store.Filter{IDs: []string{rel.TargetID}}), "dangling target id")
				}
			}
		})
	}
}

// A files run after a branch switch lists paths the full run already purged,
// or that never were indexed: they are not counted as deleted.
func TestDeletedCountsOnlyIndexedFiles(t *testing.T) {
	e := newEnv(t)
	a := e.write(t, "a.go", "func A1\n")
	_, err := e.run(t, nil)
	require.NoError(t, err)
	st, err := e.run(t, func(o *Options) {
		o.Files = []string{}
		o.Deleted = []string{filepath.Join(e.root, "never.go"), filepath.Join(e.root, "gone.go")}
	})
	require.NoError(t, err)
	assert.Equal(t, 0, st.Deleted)
	require.NoError(t, os.Remove(a))
	st, err = e.run(t, func(o *Options) { o.Files = []string{}; o.Deleted = []string{a} })
	require.NoError(t, err)
	assert.Equal(t, 1, st.Deleted)
}

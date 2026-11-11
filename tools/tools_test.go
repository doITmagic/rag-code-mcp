package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"
	"unicode"

	"github.com/doITmagic/rag-code-mcp/v2/embed"
	"github.com/doITmagic/rag-code-mcp/v2/engine"
	"github.com/doITmagic/rag-code-mcp/v2/index"
	"github.com/doITmagic/rag-code-mcp/v2/internal/config"
	"github.com/doITmagic/rag-code-mcp/v2/internal/update"
	"github.com/doITmagic/rag-code-mcp/v2/parser"
	"github.com/doITmagic/rag-code-mcp/v2/store"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeAnalyzer parses .fk files: "func NAME" starts a function in module
// "m/<dir>" (a "package X" line sets module X instead), an indented
// "call m/dir.NAME" line adds a call relation to it.
type fakeAnalyzer struct{}

func (fakeAnalyzer) Language() string     { return "fake" }
func (fakeAnalyzer) Extensions() []string { return []string{".fk"} }
func (fakeAnalyzer) Analyze(f parser.File) ([]parser.Symbol, error) {
	rel, _ := filepath.Rel(f.Root, filepath.Dir(f.Path))
	mod := "m/" + filepath.ToSlash(rel)
	var syms []parser.Symbol
	for i, line := range strings.Split(string(f.Source), "\n") {
		if p, ok := strings.CutPrefix(line, "package "); ok {
			mod = p
		} else if name, ok := strings.CutPrefix(line, "func "); ok {
			var qn string // "func X as default": named X, qualified like a JS default export
			if n, ok := strings.CutSuffix(name, " as default"); ok {
				name, qn = n, mod+".default"
			}
			syms = append(syms, parser.Symbol{Name: name, QualifiedName: qn, Kind: parser.KindFunction, Module: mod, Content: line, StartLine: i + 1,
				EndLine: i + 1, Exported: unicode.IsUpper(rune(name[0])), Signature: "func " + name + "()"})
		} else if kind, name, ok := strings.Cut(line, " "); ok && (kind == "scene" || kind == "const" || kind == "chunk") {
			k := map[string]parser.Kind{"scene": parser.KindScene, "const": parser.KindConstant, "chunk": parser.KindChunk}[kind]
			syms = append(syms, parser.Symbol{Name: name, Kind: k, Module: mod, Content: line, StartLine: i + 1, EndLine: i + 1, Exported: true})
		} else if target, ok := strings.CutPrefix(strings.TrimSpace(line), "call "); ok && len(syms) > 0 {
			s := &syms[len(syms)-1]
			tqn := target // "call x" without a dot: a call by name only (receiver type unknown)
			if !strings.Contains(target, ".") {
				tqn = ""
			}
			s.Relations = append(s.Relations, parser.Relation{Kind: parser.RelCall, TargetName: target[strings.LastIndex(target, ".")+1:],
				TargetQualifiedName: tqn, Line: i + 1})
			s.Content += "\n" + line
			s.EndLine = i + 1
		}
	}
	return parser.Finalize(f, syms), nil
}

// otherLangAnalyzer parses .fk2 files like fakeAnalyzer, as another language.
type otherLangAnalyzer struct{ fakeAnalyzer }

func (otherLangAnalyzer) Language() string     { return "fake2" }
func (otherLangAnalyzer) Extensions() []string { return []string{".fk2"} }
func (o otherLangAnalyzer) Analyze(f parser.File) ([]parser.Symbol, error) {
	syms, err := o.fakeAnalyzer.Analyze(f)
	for i := range syms {
		syms[i].Language = "fake2"
	}
	return syms, err
}

// fileModAnalyzer parses .fkm files like fakeAnalyzer, with one module per
// file ("m/<dir>/<base>"), as JS and Vue modules are.
type fileModAnalyzer struct{ fakeAnalyzer }

func (fileModAnalyzer) Extensions() []string { return []string{".fkm"} }
func (a fileModAnalyzer) Analyze(f parser.File) ([]parser.Symbol, error) {
	syms, err := a.fakeAnalyzer.Analyze(f)
	rel, _ := filepath.Rel(f.Root, strings.TrimSuffix(f.Path, filepath.Ext(f.Path)))
	for i := range syms {
		s := &syms[i]
		s.Module = "m/" + filepath.ToSlash(rel)
		s.QualifiedName = parser.Qualify(s.Module, "", s.Name)
		s.ID = parser.SymbolID(s.FilePath, s.QualifiedName, s.StartLine)
	}
	return syms, err
}

// downEmbedder is an embedder whose service is unreachable.
type downEmbedder struct{ *embed.Fake }

func (downEmbedder) Dimension(context.Context) (int, error) { return 0, embed.ErrUnavailable }

func TestMain(m *testing.M) {
	// Backstop: a test that forgets RAGCODE_HOME must not write index files
	// into the real ~/.ragcode.
	home, err := os.MkdirTemp("", "ragcode-home-")
	if err != nil {
		panic(err)
	}
	os.Setenv("RAGCODE_HOME", home)
	parser.Register(fakeAnalyzer{})
	parser.Register(otherLangAnalyzer{})
	parser.Register(fileModAnalyzer{})
	code := m.Run()
	os.RemoveAll(home)
	os.Exit(code)
}

type env struct {
	cs   *mcp.ClientSession
	e    *engine.Engine
	root string
	t    *tools
}

// newEnv starts a server over in-memory transports with a real engine on the
// memory store and a workspace of .fk files; index says whether to wait for
// the first run.
func newEnv(t *testing.T, index bool, tweak func(*config.Config)) *env {
	t.Helper()
	return newEnvWith(t, &embed.Fake{}, index, tweak)
}

func newEnvWith(t *testing.T, em embed.Embedder, index bool, tweak func(*config.Config)) *env {
	t.Helper()
	t.Setenv("RAGCODE_HOME", t.TempDir())
	cfg := config.Default()
	cfg.WatchEnabled = false
	if tweak != nil {
		tweak(&cfg)
	}
	root, err := filepath.EvalSymlinks(t.TempDir())
	require.NoError(t, err)
	files := map[string]string{
		"pkg/a.fk":   "func Foo\nfunc foo\nfunc Bar\n  call m/pkg.Foo\n",
		"pkg/b.fk":   "func Dup\n",
		"other/c.fk": "func Dup\nfunc Baz\n  call m/pkg.Bar\n  call m/pkg.Dup\n",
	}
	for p, c := range files {
		require.NoError(t, os.MkdirAll(filepath.Dir(filepath.Join(root, p)), 0o755))
		require.NoError(t, os.WriteFile(filepath.Join(root, p), []byte(c), 0o644))
	}
	e := engine.New(cfg, store.NewMemory(), em, nil)
	t.Cleanup(e.Close)
	if index {
		w, err := e.Workspace(root)
		require.NoError(t, err)
		w.Index(false)
		require.Eventually(t, func() bool {
			s := w.Status()
			return s.Phase == "done" && !s.Running && !s.Queued
		}, 10*time.Second, 20*time.Millisecond)
	}
	server := mcp.NewServer(&mcp.Implementation{Name: "test", Version: "0"}, nil)
	tl := &tools{e: e, cfg: cfg, version: "v1.0.0", started: time.Now(), releases: "http://127.0.0.1:1/none", http: &http.Client{Timeout: time.Second}}
	tl.register(server)
	ct, st := mcp.NewInMemoryTransports()
	_, err = server.Connect(context.Background(), st, nil)
	require.NoError(t, err)
	client := mcp.NewClient(&mcp.Implementation{Name: "c", Version: "0"}, nil)
	client.AddRoots(&mcp.Root{URI: "file://" + filepath.ToSlash(root)})
	// Roots can only be listed from a tool call on the pre-2026 protocol.
	cs, err := client.Connect(context.Background(), ct, &mcp.ClientSessionOptions{ProtocolVersion: "2025-11-25"})
	require.NoError(t, err)
	t.Cleanup(func() { cs.Close() })
	return &env{cs: cs, e: e, root: root, t: tl}
}

// call invokes a tool; the response is decoded when the call is not an error.
func (v *env) call(t *testing.T, name string, args map[string]any) (Response, *mcp.CallToolResult) {
	t.Helper()
	res, err := v.cs.CallTool(context.Background(), &mcp.CallToolParams{Name: name, Arguments: args})
	require.NoError(t, err)
	require.NotEmpty(t, res.Content)
	text := res.Content[0].(*mcp.TextContent).Text
	var r Response
	if !res.IsError {
		require.NoError(t, json.Unmarshal([]byte(text), &r), text)
	} else {
		r.Message = text
	}
	return r, res
}

func data(t *testing.T, r Response, out any) {
	t.Helper()
	b, err := json.Marshal(r.Data)
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal(b, out))
}

func TestMissingRootAndBadRoot(t *testing.T) {
	v := newEnv(t, false, nil)
	// Two client roots: the fallback must refuse to guess.
	cs2 := func() *mcp.ClientSession {
		server := mcp.NewServer(&mcp.Implementation{Name: "t", Version: "0"}, nil)
		v.t.register(server)
		ct, st := mcp.NewInMemoryTransports()
		_, err := server.Connect(context.Background(), st, nil)
		require.NoError(t, err)
		c := mcp.NewClient(&mcp.Implementation{Name: "c", Version: "0"}, nil)
		c.AddRoots(&mcp.Root{URI: "file:///a"}, &mcp.Root{URI: "file:///b"})
		cs, err := c.Connect(context.Background(), ct, &mcp.ClientSessionOptions{ProtocolVersion: "2025-11-25"})
		require.NoError(t, err)
		t.Cleanup(func() { cs.Close() })
		return cs
	}()
	for name, args := range map[string]map[string]any{
		"rag_search":               {"query": "Foo"},
		"rag_find_usages":          {"symbol": "Foo"},
		"rag_call_hierarchy":       {"symbol": "Foo"},
		"rag_list_package_exports": {"package": "pkg"},
		"rag_read_file_context":    {"file_path": "pkg/a.fk"},
		"rag_index_workspace":      {},
	} {
		res, err := cs2.CallTool(context.Background(), &mcp.CallToolParams{Name: name, Arguments: args})
		require.NoError(t, err)
		assert.True(t, res.IsError, name)
		assert.Contains(t, res.Content[0].(*mcp.TextContent).Text, "workspace_root", name)

		args["workspace_root"] = "relative/path"
		_, res = v.call(t, name, args)
		assert.True(t, res.IsError, name)
		assert.Contains(t, res.Content[0].(*mcp.TextContent).Text, "not absolute", name)
	}
}

func TestRootFromClientRoots(t *testing.T) {
	v := newEnv(t, true, nil)
	r, res := v.call(t, "rag_search", map[string]any{"query": "Foo"})
	require.False(t, res.IsError, r.Message)
	assert.Equal(t, "ok", r.Status)
	assert.Equal(t, "done", r.Index.State)
}

func TestSearch(t *testing.T) {
	v := newEnv(t, true, nil)
	var out struct {
		Results []SearchHit `json:"results"`
		Count   int         `json:"count"`
	}
	r, _ := v.call(t, "rag_search", map[string]any{"workspace_root": v.root, "query": "Foo", "limit": 5})
	require.Equal(t, "ok", r.Status, r.Message)
	data(t, r, &out)
	require.NotEmpty(t, out.Results)
	assert.Equal(t, "m/pkg.Foo", out.Results[0].QualifiedName)
	assert.Equal(t, "exact", out.Results[0].Match)
	assert.Equal(t, float32(1), out.Results[0].Score)
	assert.NotEmpty(t, out.Results[0].Content, "content stays when there are few results")

	// Qualified query, path prefix, kinds and language filters.
	r, _ = v.call(t, "rag_search", map[string]any{"workspace_root": v.root, "query": "other.Dup", "path_prefix": "other", "kinds": []string{"function"}, "language": "fake"})
	data(t, r, &out)
	require.Len(t, out.Results, 1)
	assert.Equal(t, filepath.Join(v.root, "other", "c.fk"), out.Results[0].FilePath)

	r, _ = v.call(t, "rag_search", map[string]any{"workspace_root": v.root, "query": "Dup", "kinds": []string{"class"}})
	data(t, r, &out)
	assert.Empty(t, out.Results)

	// A natural-language query takes the semantic path and never errors.
	r, _ = v.call(t, "rag_search", map[string]any{"workspace_root": v.root, "query": "the function that calls foo"})
	assert.Equal(t, "ok", r.Status)

	_, res := v.call(t, "rag_search", map[string]any{"workspace_root": v.root, "query": " "})
	assert.True(t, res.IsError)
}

func TestSearchNotIndexedFallback(t *testing.T) {
	v := newEnv(t, false, func(c *config.Config) { c.AutoIndex = false })
	r, _ := v.call(t, "rag_search", map[string]any{"workspace_root": v.root, "query": "Bar", "file_path": "pkg/a.fk"})
	assert.Equal(t, "not_indexed", r.Status)
	assert.Equal(t, "not_indexed", r.Index.State)
	var out struct{ Results []SearchHit }
	data(t, r, &out)
	require.NotEmpty(t, out.Results)
	assert.Equal(t, "m/pkg.Bar", out.Results[0].QualifiedName)
	assert.Equal(t, "fallback", out.Results[0].Match)
	assert.Contains(t, out.Results[0].Content, "call m/pkg.Foo")

	// With auto_index the engine queues the first run and says so.
	v2 := newEnv(t, false, nil)
	r, _ = v2.call(t, "rag_search", map[string]any{"workspace_root": v2.root, "query": "Bar"})
	assert.Contains(t, []string{"indexing", "not_indexed"}, r.Status)
	w, err := v2.e.Workspace(v2.root)
	require.NoError(t, err)
	require.Eventually(t, func() bool { return w.Status().Phase == "done" }, 10*time.Second, 20*time.Millisecond)
}

// gateEmbedder blocks multi-text (indexing) batches until open is closed;
// single-text query embeds pass through.
type gateEmbedder struct {
	*embed.Fake
	open chan struct{}
}

func (g gateEmbedder) Embed(ctx context.Context, texts []string) ([][]float32, error) {
	if len(texts) > 1 {
		<-g.open
	}
	return g.Fake.Embed(ctx, texts)
}

// While a recreate is still filling the collection, a search that finds
// nothing there falls back to the on-the-fly parse and says the index is in
// progress instead of answering "ok" with no results.
func TestSearchDuringRecreateFallsBack(t *testing.T) {
	g := gateEmbedder{Fake: &embed.Fake{}, open: make(chan struct{})}
	v := newEnvWith(t, g, false, func(c *config.Config) { c.AutoIndex = false })
	w, err := v.e.Workspace(v.root)
	require.NoError(t, err)
	w.Index(true)
	t.Cleanup(func() {
		close(g.open)
		require.Eventually(t, func() bool { s := w.Status(); return !s.Running && !s.Queued }, 10*time.Second, 20*time.Millisecond)
	})
	require.Eventually(t, func() bool { s := w.Status(); return s.Running }, 10*time.Second, 20*time.Millisecond)

	r, _ := v.call(t, "rag_search", map[string]any{"workspace_root": v.root, "query": "Bar"})
	assert.Equal(t, "indexing", r.Status, r.Message)
	var out struct{ Results []SearchHit }
	data(t, r, &out)
	require.NotEmpty(t, out.Results)
	assert.Equal(t, "m/pkg.Bar", out.Results[0].QualifiedName)
	assert.Equal(t, "fallback", out.Results[0].Match)
}

func TestFindUsages(t *testing.T) {
	v := newEnv(t, true, nil)
	var out struct {
		Files           []FileUsages `json:"files"`
		Total           int          `json:"total"`
		AmbiguousByName int          `json:"ambiguous_by_name"`
	}
	r, _ := v.call(t, "rag_find_usages", map[string]any{"workspace_root": v.root, "symbol": "Foo"})
	require.Equal(t, "ok", r.Status, r.Message)
	data(t, r, &out)
	require.Len(t, out.Files, 1)
	require.Len(t, out.Files[0].Usages, 1)
	u := out.Files[0].Usages[0]
	assert.Equal(t, "m/pkg.Bar", u.Caller)
	assert.Equal(t, 4, u.Line)
	assert.Equal(t, "call m/pkg.Foo", u.Text)
	assert.Equal(t, "call", u.Kind)
	assert.Equal(t, 1, out.Total)

	// Two symbols named Dup: ambiguous, unless file_path picks one.
	r, _ = v.call(t, "rag_find_usages", map[string]any{"workspace_root": v.root, "symbol": "Dup"})
	assert.Equal(t, "ambiguous", r.Status)
	var amb struct{ Candidates []Candidate }
	data(t, r, &amb)
	assert.Len(t, amb.Candidates, 2)
	r, _ = v.call(t, "rag_find_usages", map[string]any{"workspace_root": v.root, "symbol": "Dup", "file_path": "pkg/b.fk"})
	require.Equal(t, "ok", r.Status, r.Message)
	data(t, r, &out)
	assert.Equal(t, 1, out.Total, "Baz calls m/pkg.Dup")
	r, _ = v.call(t, "rag_find_usages", map[string]any{"workspace_root": v.root, "symbol": "m/other.Dup"})
	require.Equal(t, "ok", r.Status, r.Message)
	data(t, r, &out)
	assert.Equal(t, 0, out.Total)

	r, _ = v.call(t, "rag_find_usages", map[string]any{"workspace_root": v.root, "symbol": "Nope"})
	assert.Equal(t, "ok", r.Status)
	assert.Contains(t, r.Message, "no indexed symbol")

	v2 := newEnv(t, false, func(c *config.Config) { c.AutoIndex = false })
	r, _ = v2.call(t, "rag_find_usages", map[string]any{"workspace_root": v2.root, "symbol": "Foo"})
	assert.Equal(t, "not_indexed", r.Status)
}

func TestCallHierarchy(t *testing.T) {
	v := newEnv(t, true, nil)
	var out struct {
		Incoming *Node  `json:"incoming"`
		Outgoing *Node  `json:"outgoing"`
		Text     string `json:"text"`
	}
	r, _ := v.call(t, "rag_call_hierarchy", map[string]any{"workspace_root": v.root, "symbol": "Foo", "direction": "both", "depth": 9})
	require.Equal(t, "ok", r.Status, r.Message)
	data(t, r, &out)
	require.NotNil(t, out.Incoming)
	require.Len(t, out.Incoming.Children, 1)
	assert.Equal(t, "m/pkg.Bar", out.Incoming.Children[0].QualifiedName)
	require.Len(t, out.Incoming.Children[0].Children, 1, "depth 2+: Baz calls Bar")
	assert.Equal(t, "m/other.Baz", out.Incoming.Children[0].Children[0].QualifiedName)
	assert.Empty(t, out.Outgoing.Children)
	assert.Contains(t, out.Text, "incoming:\n  m/pkg.Foo")
	assert.Contains(t, out.Text, "    m/pkg.Bar")

	r, _ = v.call(t, "rag_call_hierarchy", map[string]any{"workspace_root": v.root, "symbol": "Baz", "direction": "outgoing"})
	require.Equal(t, "ok", r.Status, r.Message)
	data(t, r, &out)
	require.Len(t, out.Outgoing.Children, 2)
	assert.Equal(t, "m/pkg.Bar", out.Outgoing.Children[0].QualifiedName)
	assert.Equal(t, "m/pkg.Dup", out.Outgoing.Children[1].QualifiedName)
	require.Len(t, out.Outgoing.Children[0].Children, 1)
	assert.Equal(t, "m/pkg.Foo", out.Outgoing.Children[0].Children[0].QualifiedName)

	r, _ = v.call(t, "rag_call_hierarchy", map[string]any{"workspace_root": v.root, "symbol": "Dup"})
	assert.Equal(t, "ambiguous", r.Status)
	_, res := v.call(t, "rag_call_hierarchy", map[string]any{"workspace_root": v.root, "symbol": "Foo", "direction": "sideways"})
	assert.True(t, res.IsError)

	v2 := newEnv(t, false, func(c *config.Config) { c.AutoIndex = false })
	r, _ = v2.call(t, "rag_call_hierarchy", map[string]any{"workspace_root": v2.root, "symbol": "Foo"})
	assert.Equal(t, "not_indexed", r.Status)
}

func TestListPackageExports(t *testing.T) {
	v := newEnv(t, true, nil)
	var out struct {
		Modules []string            `json:"modules"`
		Exports map[string][]Export `json:"exports"`
	}
	for _, pkg := range []string{"m/pkg", "pkg"} {
		r, _ := v.call(t, "rag_list_package_exports", map[string]any{"workspace_root": v.root, "package": pkg})
		require.Equal(t, "ok", r.Status, r.Message)
		data(t, r, &out)
		assert.Equal(t, []string{"m/pkg"}, out.Modules, pkg)
		names := []string{}
		for _, e := range out.Exports["function"] {
			names = append(names, e.Name)
		}
		assert.Equal(t, []string{"Bar", "Dup", "Foo"}, names, pkg)
	}
	r, _ := v.call(t, "rag_list_package_exports", map[string]any{"workspace_root": v.root, "package": "nope"})
	assert.Equal(t, "ok", r.Status)
	assert.Contains(t, r.Message, "no indexed module")

	v2 := newEnv(t, false, func(c *config.Config) { c.AutoIndex = false })
	r, _ = v2.call(t, "rag_list_package_exports", map[string]any{"workspace_root": v2.root, "package": "pkg"})
	assert.Equal(t, "not_indexed", r.Status)
}

func TestReadFileContext(t *testing.T) {
	v := newEnv(t, false, func(c *config.Config) { c.AutoIndex = false }) // works without an index
	var out struct {
		Outline    []Outline       `json:"outline"`
		Lines      string          `json:"lines"`
		Symbols    []parser.Symbol `json:"symbols"`
		TotalLines int             `json:"total_lines"`
		Language   string          `json:"language"`
	}
	r, _ := v.call(t, "rag_read_file_context", map[string]any{"workspace_root": v.root, "file_path": "pkg/a.fk"})
	require.Equal(t, "ok", r.Status, r.Message)
	data(t, r, &out)
	assert.Len(t, out.Outline, 3)
	assert.Equal(t, "fake", out.Language)
	assert.Contains(t, out.Lines, "    1 | func Foo\n")
	assert.Equal(t, 4, out.TotalLines)

	r, _ = v.call(t, "rag_read_file_context", map[string]any{"workspace_root": v.root, "file_path": filepath.Join(v.root, "pkg/a.fk"), "symbol": "pkg.Bar"})
	require.Equal(t, "ok", r.Status, r.Message)
	data(t, r, &out)
	require.Len(t, out.Symbols, 1)
	assert.Equal(t, "func Bar\n  call m/pkg.Foo", out.Symbols[0].Content)
	assert.Len(t, out.Symbols[0].Relations, 1)

	r, _ = v.call(t, "rag_read_file_context", map[string]any{"workspace_root": v.root, "file_path": "pkg/a.fk", "start_line": 3, "end_line": 4})
	require.Equal(t, "ok", r.Status, r.Message)
	data(t, r, &out)
	assert.Equal(t, "    3 | func Bar\n    4 |   call m/pkg.Foo\n", out.Lines)

	for _, p := range []string{"../etc/passwd", "/etc/passwd", "pkg/missing.fk", "pkg"} {
		_, res := v.call(t, "rag_read_file_context", map[string]any{"workspace_root": v.root, "file_path": p})
		assert.True(t, res.IsError, p)
	}
	_, res := v.call(t, "rag_read_file_context", map[string]any{"workspace_root": v.root, "file_path": "pkg/a.fk", "symbol": "Nope"})
	assert.True(t, res.IsError)
}

func TestIndexWorkspace(t *testing.T) {
	v := newEnv(t, false, nil)
	var out struct {
		Root       string `json:"root"`
		Collection string `json:"collection"`
		Mode       string `json:"mode"`
	}
	// No confirmation step: a call without confirm starts the run.
	r, _ := v.call(t, "rag_index_workspace", map[string]any{"workspace_root": v.root})
	require.Equal(t, "indexing", r.Status, r.Message)
	data(t, r, &out)
	assert.Equal(t, v.root, out.Root)
	assert.True(t, strings.HasPrefix(out.Collection, "ragcode-"))
	assert.Equal(t, "incremental", out.Mode)

	r, _ = v.call(t, "rag_index_workspace", map[string]any{"workspace_root": v.root, "confirm": true, "mode": "recreate"})
	require.Equal(t, "indexing", r.Status, r.Message)
	assert.Contains(t, []string{"queued", "indexing", "done"}, r.Index.State)
	assert.FileExists(t, filepath.Join(v.root, ".ragcode", "RAGCODE.md"))
	assert.FileExists(t, filepath.Join(v.root, ".ragcode", ".gitignore"))
	w, err := v.e.Workspace(v.root)
	require.NoError(t, err)
	require.Eventually(t, func() bool { s := w.Status(); return s.Phase == "done" && !s.Running }, 10*time.Second, 20*time.Millisecond)
	r, _ = v.call(t, "rag_search", map[string]any{"workspace_root": v.root, "query": "Foo"})
	assert.Equal(t, "ok", r.Status)

	// An incremental plan over an index of another Format says it re-indexes everything.
	var plan struct {
		Full string `json:"full_reindex"`
	}
	r, _ = v.call(t, "rag_index_workspace", map[string]any{"workspace_root": v.root})
	data(t, r, &plan)
	assert.Empty(t, plan.Full)
	st, err := index.LoadState(v.root)
	require.NoError(t, err)
	st.Format = index.Format - 1
	require.NoError(t, st.Save())
	r, _ = v.call(t, "rag_index_workspace", map[string]any{"workspace_root": v.root})
	data(t, r, &plan)
	assert.Contains(t, plan.Full, "every file is re-indexed")

	_, res := v.call(t, "rag_index_workspace", map[string]any{"workspace_root": v.root, "mode": "full"})
	assert.True(t, res.IsError)
	_, res = v.call(t, "rag_index_workspace", map[string]any{"workspace_root": filepath.Join(v.root, "nope")})
	assert.True(t, res.IsError)
}

func TestServerInfo(t *testing.T) {
	gh := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"tag_name":"v2.0.0"}`))
	}))
	defer gh.Close()
	ollama := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/api/tags", r.URL.Path)
		_, _ = w.Write([]byte(`{"models":[{"name":"embeddinggemma-2:270m"}]}`))
	}))
	defer ollama.Close()
	v := newEnv(t, true, func(c *config.Config) { c.OllamaBaseURL = ollama.URL })
	v.t.releases = gh.URL
	var out struct {
		Version   string      `json:"version"`
		PID       int         `json:"pid"`
		Ollama    Service     `json:"ollama"`
		Update    Update      `json:"update"`
		Languages []string    `json:"languages"`
		Roots     []KnownRoot `json:"roots"`
	}
	r, _ := v.call(t, "rag_server_info", map[string]any{})
	require.Equal(t, "ok", r.Status, r.Message)
	data(t, r, &out)
	assert.Equal(t, "v1.0.0", out.Version)
	assert.Equal(t, os.Getpid(), out.PID)
	assert.True(t, out.Ollama.Reachable)
	assert.Empty(t, out.Ollama.Error)
	assert.Equal(t, Update{Current: "v1.0.0", Latest: "v2.0.0", Available: true}, out.Update)
	assert.Contains(t, out.Languages, "fake")
	require.Len(t, out.Roots, 1)
	assert.Equal(t, v.root, out.Roots[0].Root)
	assert.Equal(t, "done", out.Roots[0].State)
	assert.Equal(t, 3, out.Roots[0].Files)

	// The release answer is cached: a dead URL still yields the cached tag.
	v.t.releases = "http://127.0.0.1:1/none"
	r, _ = v.call(t, "rag_server_info", map[string]any{})
	data(t, r, &out)
	assert.Equal(t, "v2.0.0", out.Update.Latest)
	assert.FileExists(t, filepath.Join(config.HomeDir(), "cache", "update.json"))
}

// Offline, a failed release check is remembered too, so every rag_server_info
// does not wait on GitHub again.
func TestServerInfoCachesFailedUpdateCheck(t *testing.T) {
	hits := 0
	gh := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer gh.Close()
	v := newEnv(t, false, func(c *config.Config) { c.OllamaBaseURL = "http://127.0.0.1:1"; c.AutoIndex = false })
	v.t.releases = gh.URL
	var out struct {
		Update Update `json:"update"`
	}
	for range 2 {
		r, _ := v.call(t, "rag_server_info", map[string]any{})
		require.Equal(t, "ok", r.Status, r.Message)
		data(t, r, &out)
		assert.Equal(t, "unknown", out.Update.Latest)
	}
	assert.Equal(t, 1, hits)
}

func TestDependencyDown(t *testing.T) {
	v := newEnv(t, true, nil)
	e := engine.New(v.t.cfg, store.NewMemory(), downEmbedder{&embed.Fake{}}, nil)
	t.Cleanup(e.Close)
	v.t.e = e
	r, _ := v.call(t, "rag_search", map[string]any{"workspace_root": v.root, "query": "Foo"})
	assert.Equal(t, "dependency_down", r.Status)
	assert.Contains(t, r.Message, "Ollama")
}

// An object of functions with no direct usage is not dead code when its
// members are called: the answer names them.
func TestUsagesOfObjectNamesUsedMembers(t *testing.T) {
	v := newEnv(t, false, nil)
	require.NoError(t, os.MkdirAll(filepath.Join(v.root, "obj"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(v.root, "obj/a.fk"), []byte("const Api\nfunc Api.list\nfunc Api.drop\nfunc User\n  call m/obj.Api.list\n  call drop\nconst Lonely\n"), 0o644))
	w, err := v.e.Workspace(v.root)
	require.NoError(t, err)
	w.Index(false)
	require.Eventually(t, func() bool { s := w.Status(); return s.Phase == "done" && !s.Running && !s.Queued }, 10*time.Second, 20*time.Millisecond)
	r, _ := v.call(t, "rag_find_usages", map[string]any{"workspace_root": v.root, "symbol": "Api"})
	require.Equal(t, "ok", r.Status, r.Message)
	assert.Contains(t, r.Message, "its members are: Api.list (")
	assert.NotContains(t, r.Message, "Api.drop")
	r, _ = v.call(t, "rag_find_usages", map[string]any{"workspace_root": v.root, "symbol": "Lonely"})
	assert.Contains(t, r.Message, "reads of constants and variables are not tracked")
}

// A name the index does not know (a field, a local) comes back with the
// source lines that hold it as a whole word; a known symbol needs none.
func TestGrepBesideIndex(t *testing.T) {
	v := newEnv(t, false, nil)
	require.NoError(t, os.MkdirAll(filepath.Join(v.root, ".hidden"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(v.root, ".hidden/h.fk"), []byte("  uses fieldX\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(v.root, "pkg/g.fk"), []byte("func Holder\n  uses fieldX here\n  uses fieldXY and myfieldX\nconst Unused\n"), 0o644))
	w, err := v.e.Workspace(v.root)
	require.NoError(t, err)
	w.Index(false)
	require.Eventually(t, func() bool { s := w.Status(); return s.Phase == "done" && !s.Running && !s.Queued }, 10*time.Second, 20*time.Millisecond)

	var out struct {
		Grep      []GrepHit `json:"grep"`
		Truncated bool      `json:"grep_truncated"`
	}
	want := []GrepHit{{FilePath: filepath.Join(v.root, "pkg/g.fk"), Line: 2, Text: "uses fieldX here", Match: "grep"}}
	for tool, args := range map[string]map[string]any{
		"rag_search":         {"query": "fieldX"},
		"rag_find_usages":    {"symbol": "fieldX"},
		"rag_call_hierarchy": {"symbol": "pkg.fieldX"},
	} {
		args["workspace_root"] = v.root
		r, _ := v.call(t, tool, args)
		require.Equal(t, "ok", r.Status, tool)
		assert.Contains(t, r.Message, `a text search of the source files finds "fieldX" on 1 lines`, tool)
		out.Grep = nil
		data(t, r, &out)
		assert.Equal(t, want, out.Grep, tool)
		assert.False(t, out.Truncated, tool)
	}
	// Known to the index: no text search in the answer.
	r, _ := v.call(t, "rag_search", map[string]any{"workspace_root": v.root, "query": "Holder"})
	assert.NotContains(t, fmt.Sprint(r.Data), "grep:")
	r, _ = v.call(t, "rag_find_usages", map[string]any{"workspace_root": v.root, "symbol": "Foo"})
	assert.NotContains(t, fmt.Sprint(r.Data), "grep:")
	// A symbol nothing uses: the lines that name it (here its definition).
	r, _ = v.call(t, "rag_find_usages", map[string]any{"workspace_root": v.root, "symbol": "Unused"})
	out.Grep = nil
	data(t, r, &out)
	require.Len(t, out.Grep, 1)
	assert.Equal(t, 4, out.Grep[0].Line)
}

// The text search stops at grepHits lines and says it was cut short.
func TestGrepTruncated(t *testing.T) {
	v := newEnv(t, false, nil)
	require.NoError(t, os.WriteFile(filepath.Join(v.root, "pkg/many.fk"), []byte("func Many\n"+strings.Repeat("  uses manyField\n", grepHits+5)), 0o644))
	hits, cut := v.t.grep(context.Background(), v.root, v.root, "manyField")
	assert.Len(t, hits, grepHits)
	assert.True(t, cut)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	hits, cut = v.t.grep(ctx, v.root, v.root, "manyField")
	assert.Empty(t, hits)
	assert.True(t, cut, "a cancelled search is not a complete one")
	g := &grepRun{word: "manyField", cancel: func() {}, done: make(chan struct{}), truncated: true}
	close(g.done)
	d := map[string]any{}
	assert.Contains(t, g.add("", d), "was cut short before it found anything")
	assert.Equal(t, true, d["grep_truncated"])
}

type lowMemEmbedder struct{ *embed.Fake }

func (lowMemEmbedder) Dimension(context.Context) (int, error) {
	return 0, fmt.Errorf("%w: 500 MB free, at least 1536 MB needed", embed.ErrLowMemory)
}

// Too little free memory is its own status with what to free, not "start Ollama".
func TestLowMemory(t *testing.T) {
	v := newEnv(t, true, nil)
	e := engine.New(v.t.cfg, store.NewMemory(), lowMemEmbedder{&embed.Fake{}}, nil)
	t.Cleanup(e.Close)
	v.t.e = e
	r, _ := v.call(t, "rag_search", map[string]any{"workspace_root": v.root, "query": "Foo"})
	assert.Equal(t, "low_memory", r.Status)
	assert.Contains(t, r.Message, "at least 1536 MB needed")
	assert.NotContains(t, r.Message, "start Ollama")
}

func TestToolSchemas(t *testing.T) {
	v := newEnv(t, false, nil)
	res, err := v.cs.ListTools(context.Background(), nil)
	require.NoError(t, err)
	names := map[string]string{}
	for _, tl := range res.Tools {
		b, err := json.Marshal(tl.InputSchema)
		require.NoError(t, err)
		names[tl.Name] = string(b)
	}
	assert.Len(t, names, 7)
	assert.Contains(t, names["rag_find_usages"], `App\\Models`, "backslashes in the tag survive")
	assert.Contains(t, names["rag_search"], `"required":["query"]`, "workspace_root is optional in the schema so the roots fallback can run")
}

func TestHelpers(t *testing.T) {
	for q, want := range map[string]bool{"Foo": true, "pkg.Foo": true, `App\Models\User::find`: true, "a/b.C": true, "how does foo work": false, "123": false, "": false} {
		assert.Equal(t, want, isIdentifier(q), q)
	}
	assert.True(t, isTestFile("/w/pkg/a_test.go"))
	assert.True(t, isTestFile("/w/tests/a.php"))
	assert.False(t, isTestFile("/w/pkg/a.go"))
	assert.Equal(t, "a\nb\n… (truncated)", capLines("a\nb\nc", 2))
	assert.Equal(t, "a\nb", capLines("a\nb", 2))

	hits := []SearchHit{
		{QualifiedName: "t", FilePath: "/w/pkg/a_test.go", Score: 0.6},
		{QualifiedName: "same", FilePath: "/w/pkg/b.go", Score: 0.7},
		{QualifiedName: "top", FilePath: "/w/other/c.go", Score: 0.75},
	}
	got := rank(hits, "/w", "/w/pkg/x.go", 2, false)
	require.Len(t, got, 2)
	assert.Equal(t, "same", got[0].QualifiedName) // 0.7*1.15 beats 0.75*1.05 and 0.6*1.15*0.9
	assert.Equal(t, "top", got[1].QualifiedName)
}

// A file git ignores is never indexed, so the fallback must not serve it either.
func TestSearchFallbackSkipsGitIgnored(t *testing.T) {
	v := newEnv(t, false, func(c *config.Config) { c.AutoIndex = false })
	require.NoError(t, exec.Command("git", "-C", v.root, "init", "-q").Run())
	require.NoError(t, os.WriteFile(filepath.Join(v.root, ".gitignore"), []byte("local.fk\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(v.root, "local.fk"), []byte("func Password\n"), 0o644))
	r, _ := v.call(t, "rag_search", map[string]any{"workspace_root": v.root, "query": "Password"})
	var out struct{ Results []SearchHit }
	data(t, r, &out)
	assert.Empty(t, out.Results)
}

// An edit that moves a callee keeps its callers resolved, and a deleted file
// drops out of the results before the watcher (off here) catches up.
func TestEditAndDeleteKeepResultsRight(t *testing.T) {
	v := newEnv(t, true, nil)
	w, err := v.e.Workspace(v.root)
	require.NoError(t, err)
	a := filepath.Join(v.root, "pkg", "a.fk")
	require.NoError(t, os.WriteFile(a, []byte("\nfunc Foo\nfunc foo\nfunc Bar\n  call m/pkg.Foo\n"), 0o644))
	require.NoError(t, os.Chtimes(a, time.Now(), time.Now().Add(time.Second)))
	w.IndexFiles([]string{a}, nil)
	require.Eventually(t, func() bool { s := w.Status(); return !s.Running && !s.Queued && s.Done > 0 }, 10*time.Second, 20*time.Millisecond)

	var out struct {
		Outgoing *Node `json:"outgoing"`
	}
	r, _ := v.call(t, "rag_call_hierarchy", map[string]any{"workspace_root": v.root, "symbol": "Baz", "direction": "outgoing", "depth": 1})
	require.Equal(t, "ok", r.Status, r.Message)
	data(t, r, &out)
	require.Len(t, out.Outgoing.Children, 2, "Baz still calls the moved Bar")
	assert.Equal(t, 4, out.Outgoing.Children[0].StartLine)

	require.NoError(t, os.Remove(filepath.Join(v.root, "other", "c.fk")))
	r, _ = v.call(t, "rag_search", map[string]any{"workspace_root": v.root, "query": "Baz"})
	var hits struct{ Results []SearchHit }
	data(t, r, &hits)
	for _, h := range hits.Results {
		assert.NotContains(t, h.FilePath, "c.fk")
	}
}

func TestServerInfoHasNoQdrant(t *testing.T) {
	v := newEnv(t, false, func(c *config.Config) { c.OllamaBaseURL = "http://127.0.0.1:1"; c.AutoIndex = false })
	v.t.releases = "http://127.0.0.1:1/none"
	r, _ := v.call(t, "rag_server_info", map[string]any{})
	require.Equal(t, "ok", r.Status, r.Message)
	var out map[string]json.RawMessage
	data(t, r, &out)
	assert.NotContains(t, out, "qdrant")
	assert.Contains(t, out, "ollama")
}

// Build-tagged variants (lock_unix.go, lock_windows.go) share one qualified
// name, so calls to them stay ambiguous by id but match by qualified name:
// find_usages lists them once, not again as unresolved, and call_hierarchy
// shows them as callees too.
func TestSameQualifiedNameVariants(t *testing.T) {
	v := newEnv(t, false, func(c *config.Config) { c.AutoIndex = false })
	files := map[string]string{"pkg/lk_unix.fk": "func Lk\n", "pkg/lk_windows.fk": "func Lk\n", "pkg/use.fk": "func UseLk\n  call m/pkg.Lk\n",
		"pkg/lk.fk2": "func Lk\n"} // same qualified name in another language: never a callee
	for p, c := range files {
		require.NoError(t, os.WriteFile(filepath.Join(v.root, p), []byte(c), 0o644))
	}
	w, err := v.e.Workspace(v.root)
	require.NoError(t, err)
	w.Index(false)
	require.Eventually(t, func() bool { s := w.Status(); return s.Phase == "done" && !s.Running && !s.Queued }, 10*time.Second, 20*time.Millisecond)

	var out struct {
		Total           int `json:"total"`
		AmbiguousByName int `json:"ambiguous_by_name"`
	}
	r, _ := v.call(t, "rag_find_usages", map[string]any{"workspace_root": v.root, "symbol": "Lk", "file_path": "pkg/lk_unix.fk"})
	require.Equal(t, "ok", r.Status, r.Message)
	data(t, r, &out)
	assert.Equal(t, 1, out.Total)
	assert.Zero(t, out.AmbiguousByName, r.Message)

	var h struct{ Outgoing *Node }
	r, _ = v.call(t, "rag_call_hierarchy", map[string]any{"workspace_root": v.root, "symbol": "UseLk", "direction": "outgoing"})
	require.Equal(t, "ok", r.Status, r.Message)
	data(t, r, &h)
	require.Len(t, h.Outgoing.Children, 2, "both variants, not the other language's")
	for _, c := range h.Outgoing.Children {
		assert.NotEqual(t, ".fk2", filepath.Ext(c.FilePath))
	}
}

// A symbol whose name is not the end of its qualified name (a JS default
// export) is found by name; file_path is a hard filter, never a hint.
func TestResolveByNameAndFile(t *testing.T) {
	v := newEnv(t, false, func(c *config.Config) { c.AutoIndex = false })
	files := map[string]string{"web/sleep.fk": "func sleep as default\n", "web/store.fk": "func sleep\nfunc tick\n  call m/web.default\n  call Close\n",
		"gem/client.fk": "func Close\n"}
	for p, c := range files {
		require.NoError(t, os.MkdirAll(filepath.Dir(filepath.Join(v.root, p)), 0o755))
		require.NoError(t, os.WriteFile(filepath.Join(v.root, p), []byte(c), 0o644))
	}
	w, err := v.e.Workspace(v.root)
	require.NoError(t, err)
	w.Index(false)
	require.Eventually(t, func() bool { s := w.Status(); return s.Phase == "done" && !s.Running && !s.Queued }, 10*time.Second, 20*time.Millisecond)

	var out struct {
		Symbol Candidate    `json:"symbol"`
		Files  []FileUsages `json:"files"`
		Total  int          `json:"total"`
		ByName int          `json:"by_name"`
	}
	r, _ := v.call(t, "rag_find_usages", map[string]any{"workspace_root": v.root, "symbol": "sleep", "file_path": "web/sleep.fk"})
	require.Equal(t, "ok", r.Status, r.Message)
	data(t, r, &out)
	assert.Equal(t, "m/web.default", out.Symbol.QualifiedName)
	assert.Equal(t, 1, out.Total)

	r, _ = v.call(t, "rag_find_usages", map[string]any{"workspace_root": v.root, "symbol": "tick", "file_path": "web/sleep.fk"})
	assert.Equal(t, "ok", r.Status)
	assert.Contains(t, r.Message, "web/sleep.fk", "no tick there: say so, do not pick the one elsewhere")

	var s struct{ Results []SearchHit }
	r, _ = v.call(t, "rag_search", map[string]any{"workspace_root": v.root, "query": "sleep", "path_prefix": "web"})
	require.Equal(t, "ok", r.Status, r.Message)
	data(t, r, &s)
	assert.True(t, slices.ContainsFunc(s.Results, func(h SearchHit) bool { return h.QualifiedName == "m/web.default" && h.Match == "exact" }), s.Results)

	// path_prefix names a directory: a file there would match nothing, silently.
	_, res := v.call(t, "rag_search", map[string]any{"workspace_root": v.root, "query": "sleep", "path_prefix": "web/sleep.fk"})
	require.NotNil(t, res)
	assert.True(t, res.IsError)

	// A call matched by name only is a guess: marked, and counted.
	r, _ = v.call(t, "rag_find_usages", map[string]any{"workspace_root": v.root, "symbol": "Close"})
	require.Equal(t, "ok", r.Status, r.Message)
	data(t, r, &out)
	require.Equal(t, 1, out.Total)
	assert.True(t, out.Files[0].Usages[0].ByName)
	assert.Equal(t, 1, out.ByName)
	assert.Contains(t, r.Message, "by name only")

	var h struct {
		Outgoing *Node
		Text     string
	}
	r, _ = v.call(t, "rag_call_hierarchy", map[string]any{"workspace_root": v.root, "symbol": "tick", "direction": "outgoing"})
	require.Equal(t, "ok", r.Status, r.Message)
	data(t, r, &h)
	require.Len(t, h.Outgoing.Children, 2)
	for _, c := range h.Outgoing.Children {
		assert.Equal(t, c.QualifiedName == "m/gem.Close", c.ByName, c.QualifiedName)
	}
	assert.Contains(t, h.Text, "client.fk:1) [by name only]")
	var in struct{ Incoming *Node }
	r, _ = v.call(t, "rag_call_hierarchy", map[string]any{"workspace_root": v.root, "symbol": "Close"})
	require.Equal(t, "ok", r.Status, r.Message)
	data(t, r, &in)
	require.Len(t, in.Incoming.Children, 1)
	assert.True(t, in.Incoming.Children[0].ByName)
}

// rag_server_info shows a root this process is indexing or has queued with
// its live state and progress, as the other tools' index block does, not the
// persisted phase (or not_indexed before the first status is written).
func TestServerInfoLiveState(t *testing.T) {
	g := gateEmbedder{Fake: &embed.Fake{}, open: make(chan struct{})}
	v := newEnvWith(t, g, false, func(c *config.Config) { c.AutoIndex = false })
	w, err := v.e.Workspace(v.root)
	require.NoError(t, err)
	w.Index(true)
	t.Cleanup(func() {
		close(g.open)
		require.Eventually(t, func() bool { s := w.Status(); return !s.Running && !s.Queued }, 10*time.Second, 20*time.Millisecond)
	})
	require.Eventually(t, func() bool { s := w.Status(); return s.Running && s.Total > 0 }, 10*time.Second, 20*time.Millisecond)
	r, _ := v.call(t, "rag_server_info", map[string]any{})
	var out struct {
		Roots []KnownRoot `json:"roots"`
	}
	data(t, r, &out)
	require.Len(t, out.Roots, 1)
	assert.Equal(t, "indexing", out.Roots[0].State)
	assert.Equal(t, 3, out.Roots[0].Total)
}

// After a restart the last run's error comes from index_status.json, so the
// "last index run failed" message is not empty.
func TestFailedRunErrorSurvivesRestart(t *testing.T) {
	v := newEnv(t, false, func(c *config.Config) { c.AutoIndex = false })
	require.NoError(t, os.MkdirAll(index.Dir(v.root), 0o755))
	status, _ := json.Marshal(index.Status{Root: v.root, StartedAt: time.Now(), EndedAt: time.Now(), Phase: "failed", Error: "embed: connection refused"})
	require.NoError(t, os.WriteFile(filepath.Join(index.Dir(v.root), "index_status.json"), status, 0o644))
	r, _ := v.call(t, "rag_read_file_context", map[string]any{"workspace_root": v.root, "file_path": "pkg/a.fk", "start_line": 1})
	assert.Equal(t, "embed: connection refused", r.Index.Error)
	assert.Contains(t, r.Message, "(embed: connection refused)")
}

// A registered root that no longer exists on disk keeps its index status under
// ~/.ragcode, but it must not be reported as done.
func TestKnownRootsMissingRoot(t *testing.T) {
	t.Setenv("RAGCODE_HOME", t.TempDir())
	root := filepath.Join(t.TempDir(), "gone")
	require.NoError(t, os.MkdirAll(root, 0o755))
	dir := index.Dir(root)
	require.NoError(t, os.MkdirAll(dir, 0o755))
	roots, _ := json.Marshal([]string{root})
	require.NoError(t, os.WriteFile(filepath.Join(config.HomeDir(), "roots.json"), roots, 0o644))
	status, _ := json.Marshal(index.Status{Root: root, StartedAt: time.Now(), EndedAt: time.Now(), Phase: "done", Done: 3})
	require.NoError(t, os.WriteFile(filepath.Join(dir, "index_status.json"), status, 0o644))
	require.Equal(t, "done", knownRoots(nil)[0].State)

	require.NoError(t, os.RemoveAll(root))
	got := knownRoots(nil)
	require.Equal(t, "missing", got[0].State)
	require.Empty(t, got[0].Ended)
}

// Overload signatures and the implementation (or a get/set pair) share the
// qualified name and the file: they are one symbol, never ambiguous; the
// widest one (the implementation) is picked and the relations of all count.
func TestOverloadsAreOneSymbol(t *testing.T) {
	v := newEnv(t, false, func(c *config.Config) { c.AutoIndex = false })
	v.addFiles(t, map[string]string{
		"ts/h.fk": "func useRef\nfunc useRef\nfunc useRef\n  call m/ts.helper\nfunc helper\nfunc res\n  call m/ts.helper\nfunc res\n  call m/ts.other\nfunc other\n",
		"ts/u.fk": "func caller\n  call m/ts.useRef\n  call m/ts.res\n",
	})
	var out struct {
		Symbol Candidate `json:"symbol"`
		Total  int       `json:"total"`
	}
	for _, args := range []map[string]any{{"symbol": "m/ts.useRef", "file_path": "ts/h.fk"}, {"symbol": "useRef"}} {
		args["workspace_root"] = v.root
		r, _ := v.call(t, "rag_find_usages", args)
		require.Equal(t, "ok", r.Status, r.Message)
		data(t, r, &out)
		assert.Equal(t, 3, out.Symbol.StartLine, "the implementation")
		assert.Equal(t, 1, out.Total)
	}
	var h struct{ Outgoing *Node }
	r, _ := v.call(t, "rag_call_hierarchy", map[string]any{"workspace_root": v.root, "symbol": "useRef", "direction": "outgoing"})
	require.Equal(t, "ok", r.Status, r.Message)
	data(t, r, &h)
	require.Len(t, h.Outgoing.Children, 1)
	assert.Equal(t, "m/ts.helper", h.Outgoing.Children[0].QualifiedName)
	r, _ = v.call(t, "rag_call_hierarchy", map[string]any{"workspace_root": v.root, "symbol": "m/ts.res", "file_path": "ts/h.fk", "direction": "both"})
	require.Equal(t, "ok", r.Status, r.Message)
	var hb struct{ Incoming, Outgoing *Node }
	data(t, r, &hb)
	assert.Len(t, hb.Outgoing.Children, 2, "getter and setter callees")
	assert.Len(t, hb.Incoming.Children, 1)
}

// A short directory name lists the files directly in that directory even
// when every file is its own module (JS); a file without exported symbols
// says so instead of "no match"; zero usages of a constant do not claim
// imports are tracked.
func TestExportsShortDirOfFileModules(t *testing.T) {
	v := newEnv(t, false, func(c *config.Config) { c.AutoIndex = false })
	v.addFiles(t, map[string]string{"web/ui/button.fkm": "func Button\n", "web/ui/card.fkm": "func Card\n", "web/ui/deep/z.fkm": "func Deep\n",
		"lib/ssg.fkm": "func Ssg\n", "lib/ssg/utils.fkm": "func Util\n", "src/index.fkm": "func internal\n", "src/empty.fkm": "\n"})
	names := func(pkg string) []string {
		var out struct {
			Exports map[string][]Export `json:"exports"`
		}
		r, res := v.call(t, "rag_list_package_exports", map[string]any{"workspace_root": v.root, "package": pkg})
		require.False(t, res.IsError, r.Message)
		data(t, r, &out)
		var n []string
		for _, e := range out.Exports["function"] {
			n = append(n, e.Name)
		}
		return n
	}
	assert.Equal(t, []string{"Button", "Card"}, names("ui"))
	assert.Equal(t, []string{"Ssg", "Util"}, names("ssg"))
	assert.Equal(t, []string{"Button"}, names("web/ui/button.fkm"), "a file lists its own exports")
	for _, f := range []string{"src/index.fkm", "src/empty.fkm"} {
		r, _ := v.call(t, "rag_list_package_exports", map[string]any{"workspace_root": v.root, "package": f})
		assert.Contains(t, r.Message, f+" has no exported symbols of its own")
	}
	// A Go-like root package: the file named like it leads to its whole module.
	v.addFiles(t, map[string]string{"pk.fk": "package ex.com/pk\nfunc RootA\n", "other.fk": "package ex.com/pk\nfunc RootB\n"})
	assert.Equal(t, []string{"RootA", "RootB"}, names("pk"))
	base := filepath.Base(v.root) // a package named like the root: its directory, never the root's own files
	v.addFiles(t, map[string]string{"top.fkm": "func Top\n", base + "/in.fkm": "func Inner\n"})
	assert.Equal(t, []string{"Inner"}, names(base))
	// A directory whose files export nothing exists: not "no match".
	v.addFiles(t, map[string]string{"config/app.fkm": "\n"})
	r, _ := v.call(t, "rag_list_package_exports", map[string]any{"workspace_root": v.root, "package": "config"})
	assert.Contains(t, r.Message, "directory config has no exported symbols")
	if os.Geteuid() != 0 { // root reads a 000 directory
		cfgDir := filepath.Join(v.root, "config")
		require.NoError(t, os.Chmod(cfgDir, 0o000))
		r, _ = v.call(t, "rag_list_package_exports", map[string]any{"workspace_root": v.root, "package": "config"})
		require.NoError(t, os.Chmod(cfgDir, 0o755))
		assert.Contains(t, r.Message, "directory config is unreadable")
	}
	// An excluded directory (build/ is in the default exclude_patterns) and its
	// file are "not indexed", never "no exported symbols".
	v.addFiles(t, map[string]string{"build/keep.fkm": "func Kept\n"})
	for pkg, want := range map[string]string{"build": "directory build has no indexed files", "build/keep.fkm": "build/keep.fkm is not indexed"} {
		r, _ = v.call(t, "rag_list_package_exports", map[string]any{"workspace_root": v.root, "package": pkg})
		assert.Contains(t, r.Message, want, pkg)
		assert.NotContains(t, r.Message, "no exported symbols", pkg)
	}
	// A directory holding only subdirectories is not "ignored".
	r, _ = v.call(t, "rag_list_package_exports", map[string]any{"workspace_root": v.root, "package": "web"})
	assert.Contains(t, r.Message, "directory web has no exported symbols", "web/ holds only ui/")
	v.addFiles(t, map[string]string{"g/k.fk": "const Max\n"})
	r, _ = v.call(t, "rag_find_usages", map[string]any{"workspace_root": v.root, "symbol": "m/g.Max"})
	assert.NotContains(t, r.Message, "imports")
}

// A call that names a qualified name the index could not match, bound by
// the name alone (User::backWithError on a class without that method), is a
// guess: by_name in usages and in the hierarchy, both directions.
func TestQualifiedCallBoundByNameIsByName(t *testing.T) {
	v := newEnv(t, false, func(c *config.Config) { c.AutoIndex = false })
	v.addFiles(t, map[string]string{"a/u.fk": "func Owner\nfunc Caller\n  call m/a.Owner.backWithError\n", "b/s.fk": "func backWithError\n"})
	var out struct {
		Files  []FileUsages `json:"files"`
		ByName int          `json:"by_name"`
	}
	r, _ := v.call(t, "rag_find_usages", map[string]any{"workspace_root": v.root, "symbol": "m/b.backWithError"})
	require.Equal(t, "ok", r.Status, r.Message)
	data(t, r, &out)
	require.Len(t, out.Files, 1, r.Message)
	assert.True(t, out.Files[0].Usages[0].ByName)
	assert.Equal(t, 1, out.ByName)
	var h struct{ Incoming, Outgoing *Node }
	r, _ = v.call(t, "rag_call_hierarchy", map[string]any{"workspace_root": v.root, "symbol": "m/b.backWithError"})
	require.Equal(t, "ok", r.Status, r.Message)
	data(t, r, &h)
	require.Len(t, h.Incoming.Children, 1)
	assert.True(t, h.Incoming.Children[0].ByName)
	r, _ = v.call(t, "rag_call_hierarchy", map[string]any{"workspace_root": v.root, "symbol": "m/a.Caller", "direction": "outgoing"})
	require.Equal(t, "ok", r.Status, r.Message)
	data(t, r, &h)
	require.Len(t, h.Outgoing.Children, 1)
	assert.True(t, h.Outgoing.Children[0].ByName)
}

// switchEmbedder blocks multi-text (indexing) batches while block is set,
// until release is closed.
type switchEmbedder struct {
	*embed.Fake
	block   *atomic.Bool
	release chan struct{}
}

func (s switchEmbedder) Embed(ctx context.Context, texts []string) ([][]float32, error) {
	if len(texts) > 1 && s.block.Load() {
		<-s.release
	}
	return s.Fake.Embed(ctx, texts)
}

// While a run is in progress on an existing index, the index tools answer
// indexing (results may be incomplete), a search with no hits is not sent to
// the not-indexed fallback, and rag_read_file_context stays ok.
func TestStatusIndexingDuringRun(t *testing.T) {
	sw := switchEmbedder{Fake: &embed.Fake{}, block: &atomic.Bool{}, release: make(chan struct{})}
	v := newEnvWith(t, sw, true, func(c *config.Config) { c.AutoIndex = false })
	w, err := v.e.Workspace(v.root)
	require.NoError(t, err)
	sw.block.Store(true)
	require.NoError(t, os.WriteFile(filepath.Join(v.root, "pkg/n.fk"), []byte("func N1\nfunc N2\n"), 0o644))
	w.Index(false)
	released := false
	release := func() {
		if !released {
			released = true
			close(sw.release)
			require.Eventually(t, func() bool { s := w.Status(); return !s.Running && !s.Queued }, 10*time.Second, 20*time.Millisecond)
		}
	}
	t.Cleanup(release)
	require.Eventually(t, func() bool { return w.Status().Running }, 10*time.Second, 20*time.Millisecond)

	// A name not found yet may sit in a file the run has not reached: not a field.
	r, _ := v.call(t, "rag_search", map[string]any{"workspace_root": v.root, "query": "N1"})
	assert.Contains(t, r.Message, "not indexed yet", r.Message)
	assert.NotContains(t, r.Message, "struct fields", r.Message)

	for name, args := range map[string]map[string]any{
		"rag_find_usages":          {"symbol": "Foo"},
		"rag_call_hierarchy":       {"symbol": "Foo"},
		"rag_list_package_exports": {"package": "m/pkg"},
		"rag_search":               {"query": "zzzNoSuchThing", "kinds": []string{"route"}},
	} {
		args["workspace_root"] = v.root
		r, _ := v.call(t, name, args)
		assert.Equal(t, "indexing", r.Status, name+": "+r.Message)
		assert.Contains(t, r.Message, "may be incomplete", name)
		assert.NotContains(t, r.Message, "on-the-fly", name)
	}
	r, _ = v.call(t, "rag_read_file_context", map[string]any{"workspace_root": v.root, "file_path": "pkg/a.fk"})
	assert.Equal(t, "ok", r.Status, r.Message)

	release()
	r, _ = v.call(t, "rag_search", map[string]any{"workspace_root": v.root, "query": "zzzNoSuchThing", "kinds": []string{"route"}})
	assert.Equal(t, "ok", r.Status, r.Message)
}

// A kinds filter matching nothing in an existing module says so; an unknown
// kind is an input error listing the valid ones.
func TestKindsFilterAndValidation(t *testing.T) {
	v := newEnv(t, true, nil)
	for _, pkg := range []string{"m/pkg", "pkg"} {
		r, res := v.call(t, "rag_list_package_exports", map[string]any{"workspace_root": v.root, "package": pkg, "kinds": []string{"interface"}})
		require.False(t, res.IsError, r.Message)
		assert.Contains(t, r.Message, "no exported symbols of kinds [interface]", pkg)
	}
	for name, args := range map[string]map[string]any{
		"rag_list_package_exports": {"package": "pkg", "kinds": []string{"nope"}},
		"rag_search":               {"query": "Foo", "kinds": []string{"function", "nope"}},
	} {
		args["workspace_root"] = v.root
		r, res := v.call(t, name, args)
		require.True(t, res.IsError, name)
		assert.Contains(t, r.Message, `unknown kind "nope"`, name)
		assert.Contains(t, r.Message, "function, method", name)
	}
	// An unknown language is an input error too, not a silent empty answer.
	r, res := v.call(t, "rag_search", map[string]any{"workspace_root": v.root, "query": "Foo", "language": "typescript"})
	require.True(t, res.IsError)
	assert.Contains(t, r.Message, `unknown language "typescript"`)
}

// limit caps the referencing symbols exactly and says when it cut; the
// hierarchy flags a node whose callers were cut at 50.
func TestUsagesLimitAndTruncated(t *testing.T) {
	v := newEnv(t, false, func(c *config.Config) { c.AutoIndex = false })
	var many strings.Builder
	for i := range 52 { // half by name only (target id, no qualified name): the two lookups return different callers
		target := map[bool]string{true: "T", false: "m/lim.T"}[i%2 == 0]
		fmt.Fprintf(&many, "func C%d\n  call %s\n", i, target)
	}
	v.addFiles(t, map[string]string{"lim/t.fk": "func T\n", "lim/c.fk": many.String()})
	var out struct {
		Files     []FileUsages `json:"files"`
		Total     int          `json:"total"`
		Truncated bool         `json:"truncated"`
	}
	for _, c := range []struct {
		limit int
		cut   bool
	}{{1, true}, {3, true}, {52, false}, {100, false}} {
		r, _ := v.call(t, "rag_find_usages", map[string]any{"workspace_root": v.root, "symbol": "m/lim.T", "limit": c.limit})
		require.Equal(t, "ok", r.Status, r.Message)
		data(t, r, &out)
		assert.Equal(t, min(c.limit, 52), out.Total, c.limit)
		assert.Equal(t, c.cut, out.Truncated, c.limit)
		assert.Equal(t, c.cut, strings.Contains(r.Message, "raise limit"), r.Message)
	}
	var h struct {
		Incoming          *Node
		IncomingTruncated bool `json:"incoming_truncated"`
		Text              string
	}
	r, _ := v.call(t, "rag_call_hierarchy", map[string]any{"workspace_root": v.root, "symbol": "m/lim.T", "depth": 1})
	require.Equal(t, "ok", r.Status, r.Message)
	data(t, r, &h)
	assert.Len(t, h.Incoming.Children, 50)
	assert.True(t, h.IncomingTruncated)
	assert.Contains(t, h.Text, "first 50 callers")
}

// A binary file with an extension no parser handles is not dumped.
func TestReadFileBinaryUnknownExtension(t *testing.T) {
	v := newEnv(t, false, nil)
	require.NoError(t, os.MkdirAll(filepath.Join(v.root, "edge"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(v.root, "edge/bin.dat"), []byte("ab\x00\xff\xfe\ncd\x01\n"), 0o644))
	var out struct{ Lines []string }
	r, res := v.call(t, "rag_read_file_context", map[string]any{"workspace_root": v.root, "file_path": "edge/bin.dat"})
	require.False(t, res.IsError, r.Message)
	assert.Contains(t, r.Message, "not parsed (binary)")
	data(t, r, &out)
	assert.Empty(t, out.Lines)

	// A line range of a binary file returns no raw bytes either.
	out.Lines = nil
	r, res = v.call(t, "rag_read_file_context", map[string]any{"workspace_root": v.root, "file_path": "edge/bin.dat", "start_line": 1, "end_line": 2})
	require.False(t, res.IsError, r.Message)
	assert.Contains(t, r.Message, "binary")
	data(t, r, &out)
	assert.Empty(t, out.Lines)
}

// Twins (overload signatures, a get/set pair: one qualified name in one
// file) are one entry everywhere: one hierarchy node in both directions and
// at every depth, one export, one search hit. A call naming another
// symbol's twins is not "unresolved" for this one. The module spelling of a
// file (src/index for src/index.fkm) is that file.
func TestTwinsAreOneEntry(t *testing.T) {
	v := newEnv(t, false, func(c *config.Config) { c.AutoIndex = false })
	v.addFiles(t, map[string]string{
		"t/types.fk":    "func IsPlain\n  call m/t.Deep\nfunc IsPlain\n  call m/t.Deep\nfunc Deep\nfunc Deep\n",
		"x/testing.fk":  "func IsPlain\n",
		"t/use.fk":      "func User\n  call m/t.IsPlain\nfunc Top\n  call m/t.User\nfunc Guess\n  call IsPlain\n  call m/t.User.IsPlain\n",
		"src/index.fkm": "func internal\n",
		"t/lk_a.fk":     "func Lk\n", "t/lk_b.fk": "func Lk\n", "x/lk.fk": "func Lk\n", "t/uselk.fk": "func UseLk\n  call m/t.Lk\n",
	})
	qns := func(ns []*Node) (out []string) {
		for _, n := range ns {
			out = append(out, n.QualifiedName)
		}
		return out
	}
	var h struct{ Incoming, Outgoing *Node }
	r, _ := v.call(t, "rag_call_hierarchy", map[string]any{"workspace_root": v.root, "symbol": "Top", "direction": "outgoing", "depth": 4})
	require.Equal(t, "ok", r.Status, r.Message)
	data(t, r, &h)
	require.Equal(t, []string{"m/t.User"}, qns(h.Outgoing.Children))
	require.Equal(t, []string{"m/t.IsPlain"}, qns(h.Outgoing.Children[0].Children))
	assert.Equal(t, []string{"m/t.Deep"}, qns(h.Outgoing.Children[0].Children[0].Children))
	r, _ = v.call(t, "rag_call_hierarchy", map[string]any{"workspace_root": v.root, "symbol": "m/t.Deep", "depth": 3})
	require.Equal(t, "ok", r.Status, r.Message)
	data(t, r, &h)
	require.Equal(t, []string{"m/t.IsPlain"}, qns(h.Incoming.Children), "both twins call Deep: one caller")
	assert.Equal(t, []string{"m/t.User"}, qns(h.Incoming.Children[0].Children))

	var u struct {
		Total           int `json:"total"`
		AmbiguousByName int `json:"ambiguous_by_name"`
	}
	r, _ = v.call(t, "rag_find_usages", map[string]any{"workspace_root": v.root, "symbol": "m/x.IsPlain"})
	require.Equal(t, "ok", r.Status, r.Message)
	data(t, r, &u)
	assert.Equal(t, 0, u.Total)
	assert.Equal(t, 2, u.AmbiguousByName, "Guess's calls by bare name and by a qualified name nothing has; User's names m/t.IsPlain")
	r, _ = v.call(t, "rag_find_usages", map[string]any{"workspace_root": v.root, "symbol": "m/t.IsPlain"})
	require.Equal(t, "ok", r.Status, r.Message)
	data(t, r, &u)
	assert.Equal(t, 1, u.Total)
	assert.Equal(t, 2, u.AmbiguousByName)
	r, _ = v.call(t, "rag_find_usages", map[string]any{"workspace_root": v.root, "symbol": "m/x.Lk"})
	require.Equal(t, "ok", r.Status, r.Message)
	data(t, r, &u)
	assert.Zero(t, u.AmbiguousByName, "UseLk's call names m/t.Lk (two build variants), not this Lk")

	var ex struct {
		Exports map[string][]Export `json:"exports"`
	}
	r, _ = v.call(t, "rag_list_package_exports", map[string]any{"workspace_root": v.root, "package": "m/t"})
	require.Equal(t, "ok", r.Status, r.Message)
	data(t, r, &ex)
	var names []string
	for _, e := range ex.Exports["function"] {
		names = append(names, fmt.Sprintf("%s:%d", e.Name, e.StartLine))
	}
	assert.Equal(t, []string{"Deep:5", "Guess:5", "IsPlain:1", "Lk:1", "Lk:1", "Top:3", "UseLk:1", "User:1"}, names, "two Lk in two files are not twins")
	assert.Contains(t, r.Message, "8 exported symbols")
	r, _ = v.call(t, "rag_list_package_exports", map[string]any{"workspace_root": v.root, "package": "src/index"})
	assert.Contains(t, r.Message, "src/index.fkm has no exported symbols of its own")

	var s struct{ Results []SearchHit }
	r, _ = v.call(t, "rag_search", map[string]any{"workspace_root": v.root, "query": "IsPlain"})
	require.Equal(t, "ok", r.Status, r.Message)
	data(t, r, &s)
	var exact []string
	for _, hit := range s.Results {
		if hit.Name == "IsPlain" {
			exact = append(exact, hit.QualifiedName)
		}
	}
	assert.ElementsMatch(t, []string{"m/t.IsPlain", "m/x.IsPlain"}, exact)
}

// A query equal to one symbol's full qualified name picks it over symbols
// whose qualified name only ends with it (components.zzalert against
// zzprobe.card.components.zzalert).
func TestExactQualifiedNameWins(t *testing.T) {
	v := newEnv(t, false, func(c *config.Config) { c.AutoIndex = false })
	v.addFiles(t, map[string]string{"a/x.fk": "func Hook\n", "b/m/a/y.fk": "func Hook\n"})
	for _, tool := range []string{"rag_find_usages", "rag_call_hierarchy"} {
		r, _ := v.call(t, tool, map[string]any{"workspace_root": v.root, "symbol": "m/a.Hook"})
		require.Equal(t, "ok", r.Status, r.Message)
		var out struct{ Symbol Candidate }
		data(t, r, &out)
		assert.Equal(t, "m/a.Hook", out.Symbol.QualifiedName, tool)
	}
	r, _ := v.call(t, "rag_find_usages", map[string]any{"workspace_root": v.root, "symbol": "a.Hook"})
	assert.Equal(t, "ambiguous", r.Status, "neither equals a.Hook: both only end with it")
}

// A documentation heading named like a code symbol does not make the symbol
// ambiguous: the code symbol wins. Only doc chunks still resolve to a chunk.
func TestResolveSymbolCodeBeatsDocChunk(t *testing.T) {
	v := newEnv(t, false, func(c *config.Config) { c.AutoIndex = false })
	v.addFiles(t, map[string]string{
		"t/t.fk":    "func Task\nfunc Run\n  call m/t.Task\n",
		"doc/r.fk":  "package doc/README.Tasks\nchunk Task\n",
		"doc2/s.fk": "package doc2/README\nchunk Only\n",
	})
	r, _ := v.call(t, "rag_find_usages", map[string]any{"workspace_root": v.root, "symbol": "Task"})
	require.Equal(t, "ok", r.Status, r.Message)
	assert.Contains(t, r.Message, "1 usages")
	r, _ = v.call(t, "rag_call_hierarchy", map[string]any{"workspace_root": v.root, "symbol": "Task"})
	require.Equal(t, "ok", r.Status, r.Message)
	r, _ = v.call(t, "rag_find_usages", map[string]any{"workspace_root": v.root, "symbol": "Only"})
	assert.NotEqual(t, "ambiguous", r.Status, r.Message)
}

// An identifier with no symbol of that name (a struct field) says so: the
// semantic neighbours that follow are not that name.
func TestSearchIdentifierWithoutExactMatchSaysSo(t *testing.T) {
	v := newEnv(t, true, nil)
	r, _ := v.call(t, "rag_search", map[string]any{"workspace_root": v.root, "query": "ElectionDate"})
	require.Equal(t, "ok", r.Status, r.Message)
	assert.Contains(t, r.Message, `no symbol named "ElectionDate"`)
	r, _ = v.call(t, "rag_search", map[string]any{"workspace_root": v.root, "query": "Foo"})
	require.Equal(t, "ok", r.Status, r.Message)
	assert.NotContains(t, r.Message, "no symbol named")
}

// A filtered search with no hit says the filters left nothing; Go routes,
// which are not indexed, are named.
func TestSearchEmptyFilteredSaysWhy(t *testing.T) {
	v := newEnv(t, true, nil)
	r, _ := v.call(t, "rag_search", map[string]any{"workspace_root": v.root, "query": "router setup", "kinds": []string{"route"}})
	require.Equal(t, "ok", r.Status, r.Message)
	assert.Contains(t, r.Message, "no symbol matches these filters")
	assert.NotContains(t, r.Message, "Go route")
	assert.Contains(t, emptyFilterMsg("go", []parser.Kind{parser.KindRoute}), "Go route registrations are not indexed")
	assert.NotContains(t, emptyFilterMsg("php", []parser.Kind{parser.KindRoute}), "Go route")
}

// After the installed binary is replaced, every answer asks for a restart.
func TestAnswerAsksRestartAfterUpdate(t *testing.T) {
	v := newEnv(t, false, func(c *config.Config) { c.OllamaBaseURL = "http://127.0.0.1:1"; c.AutoIndex = false })
	bin := filepath.Join(t.TempDir(), "rag-code-mcp")
	require.NoError(t, os.WriteFile(bin, []byte("old"), 0o755))
	update.Remember(bin)
	t.Cleanup(func() { update.Remember("") })
	r, _ := v.call(t, "rag_server_info", map[string]any{})
	assert.NotContains(t, r.Message, "restart")
	tmp := bin + ".new"
	require.NoError(t, os.WriteFile(tmp, []byte("new"), 0o755))
	require.NoError(t, os.Rename(tmp, bin))
	r, _ = v.call(t, "rag_server_info", map[string]any{})
	assert.Contains(t, r.Message, update.RestartNote)
}

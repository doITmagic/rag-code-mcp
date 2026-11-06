package tools

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
	"unicode"

	"github.com/doITmagic/rag-code-mcp/v3/embed"
	"github.com/doITmagic/rag-code-mcp/v3/engine"
	"github.com/doITmagic/rag-code-mcp/v3/internal/config"
	"github.com/doITmagic/rag-code-mcp/v3/parser"
	"github.com/doITmagic/rag-code-mcp/v3/store"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeAnalyzer parses .fk files: "func NAME" starts a function in module
// "m/<dir>", an indented "call m/dir.NAME" line adds a call relation to it.
type fakeAnalyzer struct{}

func (fakeAnalyzer) Language() string     { return "fake" }
func (fakeAnalyzer) Extensions() []string { return []string{".fk"} }
func (fakeAnalyzer) Analyze(f parser.File) ([]parser.Symbol, error) {
	rel, _ := filepath.Rel(f.Root, filepath.Dir(f.Path))
	mod := "m/" + filepath.ToSlash(rel)
	var syms []parser.Symbol
	for i, line := range strings.Split(string(f.Source), "\n") {
		if name, ok := strings.CutPrefix(line, "func "); ok {
			syms = append(syms, parser.Symbol{Name: name, Kind: parser.KindFunction, Module: mod, Content: line, StartLine: i + 1,
				EndLine: i + 1, Exported: unicode.IsUpper(rune(name[0])), Signature: "func " + name + "()"})
		} else if target, ok := strings.CutPrefix(strings.TrimSpace(line), "call "); ok && len(syms) > 0 {
			s := &syms[len(syms)-1]
			s.Relations = append(s.Relations, parser.Relation{Kind: parser.RelCall, TargetName: target[strings.LastIndex(target, ".")+1:],
				TargetQualifiedName: target, Line: i + 1})
			s.Content += "\n" + line
			s.EndLine = i + 1
		}
	}
	return parser.Finalize(f, syms), nil
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
	assert.Equal(t, 5, out.TotalLines)

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
		Root           string `json:"root"`
		Collection     string `json:"collection"`
		EstimatedFiles int    `json:"estimated_files"`
		Mode           string `json:"mode"`
	}
	r, _ := v.call(t, "rag_index_workspace", map[string]any{"workspace_root": v.root})
	require.Equal(t, "confirmation_required", r.Status, r.Message)
	data(t, r, &out)
	assert.Equal(t, v.root, out.Root)
	assert.True(t, strings.HasPrefix(out.Collection, "ragcode-"))
	assert.Equal(t, 3, out.EstimatedFiles)
	assert.Equal(t, "incremental", out.Mode)
	assert.Equal(t, "not_indexed", r.Index.State)

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

func TestDependencyDown(t *testing.T) {
	v := newEnv(t, true, nil)
	e := engine.New(v.t.cfg, store.NewMemory(), downEmbedder{&embed.Fake{}}, nil)
	t.Cleanup(e.Close)
	v.t.e = e
	r, _ := v.call(t, "rag_search", map[string]any{"workspace_root": v.root, "query": "Foo"})
	assert.Equal(t, "dependency_down", r.Status)
	assert.Contains(t, r.Message, "Ollama")
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

// The fallback keeps the index skip rules: no symlink out of the root, no
// fifo (it would block the read), no secret file.
func TestSearchFallbackSkipRules(t *testing.T) {
	v := newEnv(t, false, func(c *config.Config) { c.AutoIndex = false })
	outside := filepath.Join(t.TempDir(), "s.fk")
	require.NoError(t, os.WriteFile(outside, []byte("func Secret\n"), 0o644))
	require.NoError(t, os.Symlink(outside, filepath.Join(v.root, "link.fk")))
	require.NoError(t, syscall.Mkfifo(filepath.Join(v.root, "x.fk"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(v.root, ".env.fk"), []byte("func Secret\n"), 0o644))
	r, _ := v.call(t, "rag_search", map[string]any{"workspace_root": v.root, "query": "Secret"})
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

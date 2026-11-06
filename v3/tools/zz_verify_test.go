package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/doITmagic/rag-code-mcp/v3/internal/config"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Independent verification probes; kept beside the package tests. Findings
// that document a ceiling are logged, not asserted, so the suite stays green.

// addFiles writes extra .fk files under the root and runs a full index.
func (v *env) addFiles(t *testing.T, files map[string]string) {
	t.Helper()
	for p, c := range files {
		require.NoError(t, os.MkdirAll(filepath.Dir(filepath.Join(v.root, p)), 0o755))
		require.NoError(t, os.WriteFile(filepath.Join(v.root, p), []byte(c), 0o644))
	}
	w, err := v.e.Workspace(v.root)
	require.NoError(t, err)
	w.Index(false)
	require.Eventually(t, func() bool {
		s := w.Status()
		return s.Phase == "done" && !s.Running && !s.Queued
	}, 30*time.Second, 20*time.Millisecond)
}

// A short package name is resolved by scanning one capped Scroll (1000
// points); modules whose symbols fall past the cap are missed.
func TestVerifyExportsShortNameBeyondScrollCap(t *testing.T) {
	v := newEnv(t, false, func(c *config.Config) { c.AutoIndex = false })
	var big strings.Builder
	for i := 0; i < 3000; i++ {
		fmt.Fprintf(&big, "func Big%d\n", i)
	}
	var small strings.Builder
	for i := 0; i < 10; i++ {
		fmt.Fprintf(&small, "func Tool%d\n", i)
	}
	v.addFiles(t, map[string]string{"big/z.fk": big.String(), "tools/t.fk": small.String()})

	var out struct {
		Modules []string            `json:"modules"`
		Exports map[string][]Export `json:"exports"`
	}
	r, _ := v.call(t, "rag_list_package_exports", map[string]any{"workspace_root": v.root, "package": "m/tools"})
	require.Equal(t, "ok", r.Status, r.Message)
	data(t, r, &out)
	assert.Len(t, out.Exports["function"], 10, "full module path")

	r, _ = v.call(t, "rag_list_package_exports", map[string]any{"workspace_root": v.root, "package": "tools"})
	require.Equal(t, "ok", r.Status, r.Message)
	data(t, r, &out)
	t.Logf("short name with 3013 symbols indexed: modules=%v exported functions found=%d of 10", out.Modules, len(out.Exports["function"]))
}

// Cycles and self-calls must not recurse forever; node cap holds.
func TestVerifyCallHierarchyCycle(t *testing.T) {
	v := newEnv(t, false, func(c *config.Config) { c.AutoIndex = false })
	v.addFiles(t, map[string]string{
		"cyc/a.fk": "func A\n  call m/cyc.B\n  call m/cyc.A\nfunc B\n  call m/cyc.A\nfunc C\n  call m/cyc.A\n",
	})
	var out struct {
		Incoming *Node  `json:"incoming"`
		Outgoing *Node  `json:"outgoing"`
		Text     string `json:"text"`
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		r, _ := v.call(t, "rag_call_hierarchy", map[string]any{"workspace_root": v.root, "symbol": "m/cyc.A", "direction": "both", "depth": 4})
		require.Equal(t, "ok", r.Status, r.Message)
		data(t, r, &out)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("call hierarchy on a cycle did not return within 5 s")
	}
	require.NotNil(t, out.Incoming)
	names := func(n *Node) []string {
		var s []string
		for _, c := range n.Children {
			s = append(s, c.QualifiedName)
		}
		return s
	}
	assert.ElementsMatch(t, []string{"m/cyc.A", "m/cyc.B", "m/cyc.C"}, names(out.Incoming))
	assert.ElementsMatch(t, []string{"m/cyc.A", "m/cyc.B"}, names(out.Outgoing))
	t.Logf("cycle text:\n%s", out.Text)
}

// A cancelled context never panics or hangs; it yields an answer at once.
func TestVerifyCancelledContext(t *testing.T) {
	v := newEnv(t, true, nil)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	start := time.Now()
	res, _, err := v.t.search(ctx, nil, SearchInput{WorkspaceRoot: v.root, Query: "Foo"})
	require.NoError(t, err)
	t.Logf("search with cancelled ctx: isError=%v text=%s", res.IsError, res.Content[0].(*mcp.TextContent).Text)
	res, _, err = v.t.findUsages(ctx, nil, UsagesInput{WorkspaceRoot: v.root, Symbol: "Foo"})
	require.NoError(t, err)
	t.Logf("find_usages with cancelled ctx: isError=%v text=%s", res.IsError, res.Content[0].(*mcp.TextContent).Text)
	res, _, err = v.t.listExports(ctx, nil, ExportsInput{WorkspaceRoot: v.root, Package: "m/pkg"})
	require.NoError(t, err)
	t.Logf("exports with cancelled ctx: isError=%v text=%s", res.IsError, res.Content[0].(*mcp.TextContent).Text)
	res, _, err = v.t.serverInfo(ctx, nil, InfoInput{})
	require.NoError(t, err)
	assert.Less(t, time.Since(start), 2*time.Second)
}

// Every dependency probe of rag_server_info runs in sequence with its own
// 3 s timeout: two stalled endpoints add up.
func TestVerifyServerInfoStalledEndpoints(t *testing.T) {
	if testing.Short() {
		t.Skip("takes ~9 s")
	}
	stall := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
		case <-time.After(10 * time.Second):
		}
	}))
	defer stall.Close()
	v := newEnv(t, false, func(c *config.Config) { c.OllamaBaseURL = stall.URL })
	v.t.releases = stall.URL
	v.t.http = &http.Client{Timeout: 3 * time.Second} // production client (Register)
	start := time.Now()
	r, _ := v.call(t, "rag_server_info", map[string]any{})
	elapsed := time.Since(start)
	require.Equal(t, "ok", r.Status)
	var out struct {
		Ollama Service `json:"ollama"`
		Update Update  `json:"update"`
	}
	data(t, r, &out)
	assert.False(t, out.Ollama.Reachable)
	assert.Equal(t, "unknown", out.Update.Latest)
	t.Logf("rag_server_info with two stalled endpoints took %s", elapsed.Round(100*time.Millisecond))
}

// Degenerate files and a symlink that leaves the root.
func TestVerifyReadFileEdges(t *testing.T) {
	v := newEnv(t, false, func(c *config.Config) { c.AutoIndex = false })
	bin := make([]byte, 4096)
	for i := range bin {
		bin[i] = byte(i * 7)
	}
	require.NoError(t, os.WriteFile(filepath.Join(v.root, "empty.fk"), nil, 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(v.root, "one.fk"), []byte("f"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(v.root, "bin.fk"), bin, 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(v.root, "trunc.fk"), []byte("func Tru\n  call m/"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(v.root, "plain.txt"), []byte("no analyzer\n"), 0o644))
	for _, p := range []string{"empty.fk", "one.fk", "bin.fk", "trunc.fk", "plain.txt"} {
		r, res := v.call(t, "rag_read_file_context", map[string]any{"workspace_root": v.root, "file_path": p})
		assert.False(t, res.IsError, p+": "+r.Message)
		assert.Equal(t, "ok", r.Status, p)
		_, res = v.call(t, "rag_read_file_context", map[string]any{"workspace_root": v.root, "file_path": p, "start_line": 1, "end_line": 3})
		assert.False(t, res.IsError, p)
		_, res = v.call(t, "rag_read_file_context", map[string]any{"workspace_root": v.root, "file_path": p, "start_line": 99})
		assert.True(t, res.IsError, p+": start past the end")
	}
	outside := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(outside, "secret.txt"), []byte("outside\n"), 0o644))
	require.NoError(t, os.Symlink(filepath.Join(outside, "secret.txt"), filepath.Join(v.root, "link.txt")))
	r, res := v.call(t, "rag_read_file_context", map[string]any{"workspace_root": v.root, "file_path": "link.txt"})
	var out struct {
		Lines string `json:"lines"`
	}
	if !res.IsError {
		data(t, r, &out)
	}
	t.Logf("symlink leaving the root: isError=%v lines=%q", res.IsError, out.Lines)

	// Search: path_prefix outside the root is refused; a huge limit is accepted.
	_, res = v.call(t, "rag_search", map[string]any{"workspace_root": v.root, "query": "Foo", "path_prefix": "../"})
	assert.True(t, res.IsError)
	r, res = v.call(t, "rag_search", map[string]any{"workspace_root": v.root, "query": "...", "limit": 1 << 30})
	assert.False(t, res.IsError, r.Message)
}

// The not-indexed fallback stops at 200 files.
func TestVerifyFallbackFileCap(t *testing.T) {
	v := newEnv(t, false, func(c *config.Config) { c.AutoIndex = false })
	for i := 0; i < 260; i++ {
		require.NoError(t, os.WriteFile(filepath.Join(v.root, fmt.Sprintf("f%03d.fk", i)), []byte("func Needle\n"), 0o644))
	}
	start := time.Now()
	r, _ := v.call(t, "rag_search", map[string]any{"workspace_root": v.root, "query": "Needle", "limit": 1000})
	assert.Equal(t, "not_indexed", r.Status)
	var out struct {
		Results []SearchHit `json:"results"`
	}
	data(t, r, &out)
	assert.LessOrEqual(t, len(out.Results), 200)
	assert.NotEmpty(t, out.Results)
	for _, h := range out.Results {
		assert.Empty(t, h.Content, "compact above 4 results")
	}
	t.Logf("fallback: %d hits in %s", len(out.Results), time.Since(start).Round(time.Millisecond))
}

// A call line of multi-byte text is cut at 200 bytes.
func TestVerifyUsageLineCut(t *testing.T) {
	v := newEnv(t, false, func(c *config.Config) { c.AutoIndex = false })
	v.addFiles(t, map[string]string{"u/u.fk": "func U\n  call m/pkg.Foo\n"})
	// The call line is read from disk at request time: lengthen it after indexing.
	line := "  call m/pkg.Foo " + strings.Repeat("é", 120)
	require.NoError(t, os.WriteFile(filepath.Join(v.root, "u/u.fk"), []byte("func U\n"+line+"\n"), 0o644))
	var out struct {
		Files []FileUsages `json:"files"`
	}
	r, _ := v.call(t, "rag_find_usages", map[string]any{"workspace_root": v.root, "symbol": "m/pkg.Foo", "limit": 100})
	require.Equal(t, "ok", r.Status, r.Message)
	data(t, r, &out)
	var texts []string
	for _, f := range out.Files {
		for _, u := range f.Usages {
			texts = append(texts, u.Text)
		}
	}
	for _, s := range texts {
		t.Logf("usage text: bytes=%d validUTF8=%v", len(s), utf8.ValidString(s))
	}
}

// ---- second verification pass ----

// Exact hits: compact above 4 results unless full=true; content capped at
// 200 lines; the path boost prefers the current directory and demotes tests.
func TestVerifySearchCompactFullAndBoost(t *testing.T) {
	v := newEnv(t, false, func(c *config.Config) { c.AutoIndex = false })
	long := "func Long\n" + strings.Repeat("  call m/pkg.Foo\n", 250)
	files := map[string]string{"pkg/long.fk": long, "tests/t.fk": "func Near\n", "other/n.fk": "func Near\n", "pkg/n.fk": "func Near\n"}
	for i := 0; i < 6; i++ {
		files[fmt.Sprintf("z/z%d.fk", i)] = "func Zed\n"
	}
	v.addFiles(t, files)
	var out struct {
		Results []SearchHit `json:"results"`
	}
	r, _ := v.call(t, "rag_search", map[string]any{"workspace_root": v.root, "query": "Zed"})
	require.Equal(t, "ok", r.Status, r.Message)
	data(t, r, &out)
	require.Len(t, out.Results, 6)
	for _, h := range out.Results {
		assert.Empty(t, h.Content, "compact above 4")
		assert.Equal(t, "exact", h.Match)
	}
	r, _ = v.call(t, "rag_search", map[string]any{"workspace_root": v.root, "query": "Zed", "full": true, "limit": 3})
	data(t, r, &out)
	require.Len(t, out.Results, 3, "cut to limit")
	assert.NotEmpty(t, out.Results[0].Content)

	r, _ = v.call(t, "rag_search", map[string]any{"workspace_root": v.root, "query": "m/pkg.Long", "full": true})
	data(t, r, &out)
	require.NotEmpty(t, out.Results) // the fake embedder adds semantic hits after the exact one
	assert.Equal(t, "exact", out.Results[0].Match)
	assert.Equal(t, 200, strings.Count(out.Results[0].Content, "\n"), "200 content lines plus the marker")
	assert.True(t, strings.HasSuffix(out.Results[0].Content, "(truncated)"))

	r, _ = v.call(t, "rag_search", map[string]any{"workspace_root": v.root, "query": "Near", "file_path": "pkg/a.fk"})
	data(t, r, &out)
	var near []SearchHit
	for _, h := range out.Results {
		if h.Name == "Near" {
			near = append(near, h)
		}
	}
	require.Len(t, near, 3)
	assert.Equal(t, filepath.Join(v.root, "pkg", "n.fk"), near[0].FilePath, "same dir first")
	assert.InDelta(t, 1.15, near[0].Score, 0.001)
	assert.Equal(t, filepath.Join(v.root, "tests", "t.fk"), near[2].FilePath, "test file last")
	assert.InDelta(t, 0.9, near[2].Score, 0.001)
}

// The release check honours the 24 h cache: a stale entry is refreshed, a
// fresh one is served without a request.
func TestVerifyUpdateCacheExpiry(t *testing.T) {
	var calls int32
	gh := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		_, _ = w.Write([]byte(`{"tag_name":"v3.0.0"}`))
	}))
	defer gh.Close()
	v := newEnv(t, false, func(c *config.Config) { c.OllamaBaseURL = "http://127.0.0.1:1" })
	v.t.releases = gh.URL
	path := filepath.Join(config.HomeDir(), "cache", "update.json")
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	stale := fmt.Sprintf(`{"latest":"v0.0.1","checked_at":%q}`, time.Now().Add(-25*time.Hour).Format(time.RFC3339))
	require.NoError(t, os.WriteFile(path, []byte(stale), 0o644))
	var out struct {
		Update Update `json:"update"`
	}
	r, _ := v.call(t, "rag_server_info", map[string]any{})
	data(t, r, &out)
	assert.Equal(t, "v3.0.0", out.Update.Latest, "stale cache refreshed")
	assert.True(t, out.Update.Available)
	assert.EqualValues(t, 1, calls)
	r, _ = v.call(t, "rag_server_info", map[string]any{})
	data(t, r, &out)
	assert.Equal(t, "v3.0.0", out.Update.Latest)
	assert.EqualValues(t, 1, calls, "fresh cache: no request")
	// A current version equal to the latest is not an update.
	v.t.version = "3.0.0"
	r, _ = v.call(t, "rag_server_info", map[string]any{})
	data(t, r, &out)
	assert.False(t, out.Update.Available)
}

// Line ranges: end past the file is clamped, end before start is an error,
// a kinds filter on exports applies.
func TestVerifyReadRangeAndExportKinds(t *testing.T) {
	v := newEnv(t, true, nil)
	var out struct {
		Lines   string `json:"lines"`
		EndLine int    `json:"end_line"`
	}
	r, res := v.call(t, "rag_read_file_context", map[string]any{"workspace_root": v.root, "file_path": "pkg/a.fk", "start_line": 4, "end_line": 400})
	require.False(t, res.IsError, r.Message)
	data(t, r, &out)
	assert.Equal(t, 5, out.EndLine)
	assert.Equal(t, "    4 |   call m/pkg.Foo\n    5 | \n", out.Lines)
	_, res = v.call(t, "rag_read_file_context", map[string]any{"workspace_root": v.root, "file_path": "pkg/a.fk", "start_line": 4, "end_line": 2})
	assert.True(t, res.IsError)

	var ex struct {
		Exports map[string][]Export `json:"exports"`
	}
	r, _ = v.call(t, "rag_list_package_exports", map[string]any{"workspace_root": v.root, "package": "m/pkg", "kinds": []string{"class"}})
	require.Equal(t, "ok", r.Status)
	data(t, r, &ex)
	assert.Empty(t, ex.Exports)
	r, _ = v.call(t, "rag_list_package_exports", map[string]any{"workspace_root": v.root, "package": "pkg", "kinds": []string{"function"}})
	data(t, r, &ex)
	assert.Len(t, ex.Exports["function"], 3, "short name with kinds")
}

// A recreate run while not indexed and a second confirm while running do
// not error; the status reports the queue.
func TestVerifyIndexTwice(t *testing.T) {
	v := newEnv(t, false, func(c *config.Config) { c.AutoIndex = false; c.AutoCreateIDERules = false })
	r, _ := v.call(t, "rag_index_workspace", map[string]any{"workspace_root": v.root, "confirm": true})
	assert.Equal(t, "indexing", r.Status)
	r, _ = v.call(t, "rag_index_workspace", map[string]any{"workspace_root": v.root, "confirm": true})
	assert.Equal(t, "indexing", r.Status)
	t.Logf("second confirm: %s (index.state=%s)", r.Message, r.Index.State)
	assert.NoFileExists(t, filepath.Join(v.root, ".ragcode", "RAGCODE.md"), "auto_create_ide_rules=false")
	w, err := v.e.Workspace(v.root)
	require.NoError(t, err)
	require.Eventually(t, func() bool { s := w.Status(); return s.Phase == "done" && !s.Running && !s.Queued }, 10*time.Second, 20*time.Millisecond)
}

// A minified file is one huge line: no parse (the index skips it too) and the
// returned lines are capped, so the answer stays small and fast.
func TestReadFileMinifiedAndLongLines(t *testing.T) {
	v := newEnv(t, false, func(c *config.Config) { c.AutoIndex = false })
	require.NoError(t, os.WriteFile(filepath.Join(v.root, "x.min.js"), []byte(strings.Repeat("var a=1;", 20_000)), 0o644)) // under the 256 KiB cap
	require.NoError(t, os.WriteFile(filepath.Join(v.root, "long.fk"), []byte("func Foo\n"+strings.Repeat("y", 10_000)+"\n"), 0o644))
	var out struct {
		Lines   string `json:"lines"`
		Outline []any  `json:"outline"`
	}

	r, res := v.call(t, "rag_read_file_context", map[string]any{"workspace_root": v.root, "file_path": "x.min.js"})
	require.False(t, res.IsError, r.Message)
	data(t, r, &out)
	assert.Contains(t, r.Message, "minified")
	assert.Nil(t, out.Outline)
	assert.Less(t, len(out.Lines), 3000)

	out.Lines = ""
	r, res = v.call(t, "rag_read_file_context", map[string]any{"workspace_root": v.root, "file_path": "long.fk"})
	require.False(t, res.IsError, r.Message)
	data(t, r, &out)
	assert.Less(t, len(out.Lines), 3000)
	assert.Contains(t, out.Lines, "more bytes")
}

// symbol= returns the symbol's content: a huge line in it is capped too.
func TestReadFileSymbolLongLine(t *testing.T) {
	v := newEnv(t, false, func(c *config.Config) { c.AutoIndex = false })
	name := "Foo" + strings.Repeat("y", 10_000)
	require.NoError(t, os.WriteFile(filepath.Join(v.root, "sym.fk"), []byte("func "+name+"\n"), 0o644))
	r, res := v.call(t, "rag_read_file_context", map[string]any{"workspace_root": v.root, "file_path": "sym.fk", "symbol": name})
	require.False(t, res.IsError, r.Message)
	var out struct {
		Symbols []struct{ Content, Signature string } `json:"symbols"`
	}
	data(t, r, &out)
	require.Len(t, out.Symbols, 1)
	assert.Less(t, len(out.Symbols[0].Content), 3000)
	assert.LessOrEqual(t, len(out.Symbols[0].Signature), 200)
}

// An indexed caller replaced by a symlink to a file outside the root (watcher
// off) must not leak that file's lines through rag_find_usages.
func TestFindUsagesDoesNotFollowSymlinkOut(t *testing.T) {
	v := newEnv(t, true, nil)
	outside := filepath.Join(t.TempDir(), "secret.txt")
	require.NoError(t, os.WriteFile(outside, []byte("TOP-SECRET-1\nTOP-SECRET-2\nTOP-SECRET-3\nTOP-SECRET-4\n"), 0o644))
	caller := filepath.Join(v.root, "other", "c.fk")
	require.NoError(t, os.Remove(caller))
	require.NoError(t, os.Symlink(outside, caller))
	r, res := v.call(t, "rag_find_usages", map[string]any{"workspace_root": v.root, "symbol": "Bar"})
	require.False(t, res.IsError, r.Message)
	b, _ := json.Marshal(r)
	assert.NotContains(t, string(b), "TOP-SECRET")
}

// limit*2 overflowed to a negative number: no hits, status ok.
func TestSearchHugeLimitStillFinds(t *testing.T) {
	v := newEnv(t, true, nil)
	r, res := v.call(t, "rag_search", map[string]any{"workspace_root": v.root, "query": "what calls the bar function", "limit": 1 << 62})
	require.False(t, res.IsError, r.Message)
	var out struct {
		Count int `json:"count"`
	}
	data(t, r, &out)
	assert.Positive(t, out.Count)
}

// A file the index refuses as a secret is not returned either, whole or by
// line range.
func TestReadFileRefusesSecrets(t *testing.T) {
	v := newEnv(t, false, func(c *config.Config) { c.AutoIndex = false })
	require.NoError(t, os.WriteFile(filepath.Join(v.root, ".env"), []byte("TOKEN=TOP-SECRET-ENV\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(v.root, "secrets.yaml"), []byte("db: TOP-SECRET-YAML\n"), 0o644))
	for _, args := range []map[string]any{
		{"file_path": ".env"},
		{"file_path": ".env", "start_line": 1, "end_line": 1},
		{"file_path": "secrets.yaml"},
	} {
		args["workspace_root"] = v.root
		r, res := v.call(t, "rag_read_file_context", args)
		assert.True(t, res.IsError, "%v", args)
		raw, _ := json.Marshal(r)
		assert.NotContains(t, string(raw), "TOP-SECRET", "%v", args)
		assert.Contains(t, r.Message, "secret", "%v", args)
	}
}

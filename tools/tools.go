// Package tools registers the rag_* MCP tools on top of the engine. Every
// tool takes workspace_root (the absolute project path), answers with one
// JSON text block {status, message, data, index} and reports a dependency
// that is down instead of hanging.
package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/doITmagic/rag-code-mcp/v2/engine"
	"github.com/doITmagic/rag-code-mcp/v2/internal/config"
	"github.com/doITmagic/rag-code-mcp/v2/store"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Response is the JSON body of every tool answer.
type Response struct {
	Status  string     `json:"status"` // ok|not_indexed|indexing|dependency_down|ambiguous|error
	Message string     `json:"message,omitempty"`
	Data    any        `json:"data,omitempty"`
	Index   *IndexInfo `json:"index,omitempty"`
}

// IndexInfo summarizes the workspace index state inside a response.
type IndexInfo struct {
	State  string `json:"state"` // not_indexed|queued|indexing|done|failed
	Done   int    `json:"done"`
	Total  int    `json:"total"`
	Branch string `json:"branch,omitempty"`
	Age    string `json:"age,omitempty"` // time since the last finished run
	Error  string `json:"error,omitempty"`
}

type tools struct {
	e        *engine.Engine
	cfg      config.Config
	version  string
	started  time.Time
	releases string // GitHub latest-release URL; tests point it at a local server
	http     *http.Client
}

// Register adds every rag_* tool to server.
func Register(server *mcp.Server, e *engine.Engine, cfg config.Config, version string, startedAt time.Time) {
	t := &tools{e: e, cfg: cfg, version: version, started: startedAt,
		releases: "https://api.github.com/repos/doITmagic/rag-code-mcp/releases/latest",
		http:     &http.Client{Timeout: 3 * time.Second}}
	t.register(server)
}

func (t *tools) register(server *mcp.Server) {
	mcp.AddTool(server, &mcp.Tool{Name: "rag_search", Description: "Search the indexed code: exact match by (qualified) name when the query is an identifier, semantic search otherwise. Narrow it with language (e.g. go), path_prefix or kinds when other languages or folders crowd the results. Results carry file, lines, kind and score; compact when there are more than 4 unless full=true."}, t.search)
	mcp.AddTool(server, &mcp.Tool{Name: "rag_find_usages", Description: "Every indexed reference to a symbol (calls, extends, implements, uses), grouped by file with the calling symbol and the call line."}, t.findUsages)
	mcp.AddTool(server, &mcp.Tool{Name: "rag_call_hierarchy", Description: "Callers (incoming) and callees (outgoing) of a function as a tree, up to depth 4."}, t.callHierarchy)
	mcp.AddTool(server, &mcp.Tool{Name: "rag_list_package_exports", Description: "The exported symbols of a package or module, grouped by kind."}, t.listExports)
	mcp.AddTool(server, &mcp.Tool{Name: "rag_read_file_context", Description: "Read a file of the workspace: a symbol with its relations, a line range, or the outline plus the first 120 lines."}, t.readFile)
	mcp.AddTool(server, &mcp.Tool{Name: "rag_index_workspace", Description: "Index the workspace: queues a run and returns at once. mode=incremental re-indexes changed files; mode=recreate drops the collection and re-embeds everything."}, t.indexWorkspace)
	mcp.AddTool(server, &mcp.Tool{Name: "rag_server_info", Description: "Server version, uptime, Ollama reachability, update availability, registered languages and known workspaces."}, t.serverInfo)
}

// ---- responses ----

// reply answers from the index: an ok answer while a run is in progress or
// queued is indexing, since the results may be incomplete.
func reply(status, msg string, data any, w *engine.Workspace) (*mcp.CallToolResult, any, error) {
	return answer(status, msg, data, w, true)
}

// answer is reply; partial says whether the answer depends on the index.
func answer(status, msg string, data any, w *engine.Workspace, partial bool) (*mcp.CallToolResult, any, error) {
	r := Response{Status: status, Message: msg, Data: data}
	if w != nil {
		r.Index = indexInfo(w)
		if st := r.Index.State; partial && status == "ok" && (st == "indexing" || st == "queued") {
			r.Status = "indexing"
			r.Message = strings.TrimPrefix(r.Message+"; index run in progress: results may be incomplete until it ends", "; ")
		}
		if r.Message == "" && (r.Index.State == "failed" || r.Index.State == "interrupted") {
			why := ""
			if r.Index.Error != "" {
				why = " (" + r.Index.Error + ")"
			}
			r.Message = fmt.Sprintf("last index run %s%s; run rag_index_workspace to retry", r.Index.State, why)
		}
	}
	b, err := json.Marshal(r)
	if err != nil {
		return fail("cannot encode the response: " + err.Error())
	}
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: string(b)}}}, nil, nil
}

// fail is a tool error: one actionable sentence, IsError set.
func fail(msg string) (*mcp.CallToolResult, any, error) {
	return &mcp.CallToolResult{IsError: true, Content: []mcp.Content{&mcp.TextContent{Text: msg}}}, nil, nil
}

func indexInfo(w *engine.Workspace) *IndexInfo {
	s := w.Status()
	info := &IndexInfo{Done: s.Done, Total: s.Total, Branch: s.Branch, Error: s.LastError}
	if info.Error == "" && !s.Running && !s.Queued {
		info.Error = s.Error // after a restart: the error index_status.json kept
	}
	switch {
	case s.Running:
		info.State = "indexing"
	case s.Queued:
		info.State = "queued"
	case s.Phase == "":
		info.State = "not_indexed"
	default:
		info.State = s.Phase
	}
	if !s.EndedAt.IsZero() {
		info.Age = time.Since(s.EndedAt).Round(time.Second).String()
	}
	return info
}

// ---- workspace resolution ----

// resolveRoot returns the workspace for root; "" falls back to the client's
// roots when it lists exactly one.
func (t *tools) resolveRoot(ctx context.Context, req *mcp.CallToolRequest, root string) (*engine.Workspace, error) {
	if strings.TrimSpace(root) == "" {
		res, err := req.Session.ListRoots(ctx, nil)
		if err != nil || len(res.Roots) != 1 {
			n := 0
			if res != nil {
				n = len(res.Roots)
			}
			slog.Debug("tools: no usable client root", "err", err, "roots", n)
			return nil, errors.New("workspace_root is missing: pass the absolute path of the project directory")
		}
		u, err := url.Parse(res.Roots[0].URI)
		if err != nil || u.Scheme != "file" {
			return nil, errors.New("the client root is not a file:// URI: pass workspace_root, the absolute path of the project directory")
		}
		root = u.Path
		if runtime.GOOS == "windows" && len(root) > 2 && root[0] == '/' && root[2] == ':' {
			root = root[1:] // file:///c:/x parses to /c:/x
		}
		root = filepath.FromSlash(root)
	}
	return t.e.Workspace(root)
}

// open resolves the root and checks the dependencies. The result is nil when
// the tool can go on; otherwise it is the answer to return.
func (t *tools) open(ctx context.Context, req *mcp.CallToolRequest, root string) (*engine.Workspace, *mcp.CallToolResult) {
	w, err := t.resolveRoot(ctx, req, root)
	if err != nil {
		res, _, _ := fail(err.Error())
		return nil, res
	}
	if err := ctx.Err(); err != nil {
		res, _, _ := fail(err.Error())
		return nil, res
	}
	if ok, msg := w.Ready(ctx); !ok && msg != engine.ErrNotIndexed.Error() {
		res, _, _ := reply("dependency_down", msg, nil, w)
		return w, res
	}
	return w, nil
}

// notIndexed is the answer when a store call said the collection is missing;
// the engine has already queued the first run when auto_index is on.
func notIndexed(w *engine.Workspace) (*mcp.CallToolResult, any, error) {
	status := "not_indexed"
	if s := w.Status(); s.Running || s.Queued {
		status = "indexing"
	}
	return reply(status, engine.ErrNotIndexed.Error(), nil, w)
}

// storeErr maps an error from Search/Scroll to a response; a cancelled
// or expired request is a plain error, not a dependency that is down.
func storeErr(ctx context.Context, w *engine.Workspace, err error) (*mcp.CallToolResult, any, error) {
	if cerr := ctx.Err(); cerr != nil {
		return fail(cerr.Error())
	}
	if errors.Is(err, engine.ErrNotIndexed) {
		return notIndexed(w)
	}
	return reply("dependency_down", err.Error()+": check that Ollama is running (rag_server_info)", nil, w)
}

// insideRoot resolves p (absolute or relative to root, symlinks followed like
// the engine does for root) and checks it stays under root. A path that does
// not exist is only cleaned; the caller's open or stat fails on it anyway.
func insideRoot(root, p string) (string, error) {
	if p == "" {
		return "", errors.New("file_path is empty")
	}
	p = strings.TrimPrefix(p, "res://") // Godot resource path: relative to the project root
	if !filepath.IsAbs(p) {
		p = filepath.Join(root, p)
	}
	p = filepath.Clean(p)
	if real, err := filepath.EvalSymlinks(p); err == nil {
		p = real
	}
	if p != root && !strings.HasPrefix(p, root+string(filepath.Separator)) {
		return "", fmt.Errorf("%s is outside the workspace root %s", p, root)
	}
	return p, nil
}

// live drops the hits whose file is gone from disk (deleted with the watcher
// off, while the server was down or within the debounce) and queues their
// purge. Each path is stat'ed once.
func live(w *engine.Workspace, hits []store.Hit) []store.Hit {
	alive := map[string]bool{}
	var gone []string
	var out []store.Hit
	for _, h := range hits {
		p := h.Symbol.FilePath
		ok, seen := alive[p]
		if !seen {
			_, err := os.Stat(p)
			ok = !errors.Is(err, fs.ErrNotExist)
			alive[p] = ok
			if !ok {
				gone = append(gone, p)
			}
		}
		if ok {
			out = append(out, h)
		}
	}
	w.IndexFiles(nil, gone)
	return out
}

// readLines returns the lines of an indexed file, nil when it cannot be read.
// The file may have changed since indexing: a symlink out of the root, a FIFO
// or a huge file is not read.
func readLines(root, path string) []string {
	p, err := insideRoot(root, path)
	if err != nil {
		return nil
	}
	if info, err := os.Stat(p); err != nil || !info.Mode().IsRegular() || info.Size() > maxReadSize {
		return nil
	}
	data, err := os.ReadFile(p)
	if err != nil {
		return nil
	}
	return strings.Split(string(data), "\n")
}

// capLines keeps the first n lines of s.
func capLines(s string, n int) string {
	lines := strings.SplitN(s, "\n", n+1)
	if len(lines) <= n {
		return s
	}
	return strings.Join(lines[:n], "\n") + "\n… (truncated)"
}

func isTestFile(path string) bool {
	base := strings.ToLower(filepath.Base(path))
	dir := filepath.ToSlash(filepath.Dir(path)) + "/"
	return strings.Contains(base, "_test.") || strings.Contains(base, ".test.") || strings.Contains(base, ".spec.") ||
		strings.HasPrefix(base, "test_") || strings.Contains(dir, "/test/") || strings.Contains(dir, "/tests/") || strings.Contains(dir, "/__tests__/")
}

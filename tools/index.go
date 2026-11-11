package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/doITmagic/rag-code-mcp/v2/engine"
	"github.com/doITmagic/rag-code-mcp/v2/index"
	"github.com/doITmagic/rag-code-mcp/v2/internal/update"
	"github.com/doITmagic/rag-code-mcp/v2/parser"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// ---- rag_index_workspace ----

// IndexInput is the input of rag_index_workspace.
type IndexInput struct {
	WorkspaceRoot string `json:"workspace_root,omitempty" jsonschema:"Absolute path of the top directory of the project the user is working in (required), the same on every call. Find it as the folder opened in the editor, or the nearest parent directory that holds .git (in a monorepo that is the repository root, not a package inside it); without git, the nearest parent that holds a project file (go.mod, package.json, composer.json, pyproject.toml, project.godot); with neither, the smallest directory that holds the files being worked on. Never go up to a directory that holds other projects, the home or a system directory, and never pass a subdirectory of the project."`
	Mode          string `json:"mode,omitempty" jsonschema:"incremental (default: changed files only) or recreate (drop the collection and re-embed everything)."`
	Confirm       bool   `json:"confirm,omitempty" jsonschema:"Set true only when a tool answered that workspace_root is doubtful and the user then confirmed, in this conversation, that they want exactly this directory. Never set it on your own."`
}

func (t *tools) indexWorkspace(ctx context.Context, req *mcp.CallToolRequest, in IndexInput) (*mcp.CallToolResult, any, error) {
	switch in.Mode {
	case "":
		in.Mode = "incremental"
	case "incremental", "recreate":
	default:
		return fail("mode must be incremental or recreate")
	}
	if in.Confirm && strings.TrimSpace(in.WorkspaceRoot) != "" {
		_ = t.e.ConfirmRoot(in.WorkspaceRoot) // a refused root is reported by open below
	}
	w, res := t.open(ctx, req, in.WorkspaceRoot)
	if res != nil {
		return res, nil, nil
	}
	data := map[string]any{"root": w.Root, "collection": w.Collection, "mode": in.Mode}
	if st, err := index.LoadState(w.Root); in.Mode == "incremental" && err == nil && len(st.Files) > 0 && st.Format != index.Format {
		data["full_reindex"] = fmt.Sprintf("index format %d -> %d: every file is re-indexed", st.Format, index.Format)
	}
	var notes []string
	if t.cfg.AutoCreateIDERules {
		if err := engine.WriteIDERules(w.Root); err != nil {
			notes = append(notes, "IDE rules not written: "+err.Error())
		}
	}
	w.Index(in.Mode == "recreate")
	return reply("indexing", strings.Join(append([]string{"queued an index run (" + in.Mode + "); poll rag_server_info or any tool for progress"}, notes...), "; "), data, w)
}

// ---- rag_server_info ----

// InfoInput is the (empty) input of rag_server_info.
type InfoInput struct{}

// Service is the reachability of one dependency.
type Service struct {
	URL       string `json:"url"`
	Model     string `json:"model,omitempty"`
	Reachable bool   `json:"reachable"`
	Error     string `json:"error,omitempty"`
}

// Update is the result of the release check.
type Update struct {
	Current   string `json:"current"`
	Latest    string `json:"latest"` // "unknown" when the check failed
	Available bool   `json:"available"`
}

// KnownRoot is one workspace from roots.json with its persisted index state.
type KnownRoot struct {
	Root  string `json:"root"`
	State string `json:"state"` // not_indexed|queued|indexing|done|failed|interrupted|<phase>
	Files int    `json:"last_run_files"`
	Total int    `json:"total,omitempty"` // files of the run in progress
	Ended string `json:"ended_at,omitempty"`
}

func (t *tools) serverInfo(ctx context.Context, _ *mcp.CallToolRequest, _ InfoInput) (*mcp.CallToolResult, any, error) {
	// Ollama is waited for as long as the caller waits; the release check
	// never delays the answer (see update).
	ollama, update := t.probeOllama(ctx), t.update()
	data := map[string]any{
		"version":   t.version,
		"pid":       os.Getpid(),
		"uptime":    time.Since(t.started).Round(time.Second).String(),
		"ollama":    ollama,
		"update":    update,
		"languages": parser.Languages(),
		"roots":     knownRoots(t.e),
	}
	return reply("ok", "", data, nil)
}

func (t *tools) probeOllama(ctx context.Context) Service {
	s := Service{URL: t.cfg.OllamaBaseURL, Model: t.cfg.OllamaEmbed}
	var body struct {
		Models []struct {
			Name string `json:"name"`
		} `json:"models"`
	}
	if err := t.getJSON(ctx, strings.TrimRight(s.URL, "/")+"/api/tags", &body); err != nil {
		s.Error = err.Error() + ": start Ollama"
		if errors.Is(err, context.DeadlineExceeded) { // it runs but is slow: not a reason to start it
			s.Error = err.Error() + ": Ollama did not answer in time; the machine may be overloaded or low on memory"
		}
		return s
	}
	s.Reachable = true
	for _, m := range body.Models {
		if m.Name == s.Model || strings.TrimSuffix(m.Name, ":latest") == s.Model {
			return s
		}
	}
	s.Error = fmt.Sprintf("model %s not pulled: run ollama pull %s", s.Model, s.Model)
	return s
}

// getJSON GETs url within the client timeout and decodes the body into out
// (nil: only the status is checked).
func (t *tools) getJSON(ctx context.Context, url string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	resp, err := t.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("%s returned %s", url, resp.Status)
	}
	if out == nil {
		return nil
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

// update returns the latest release tag (cached for 24 h by update.Latest)
// and whether it is newer than this server.
// update answers from the cached release check. A missing or old cache is
// refreshed in the background, for the next answer: GitHub may be slow or
// unreachable, and no clock decides how long that is.
func (t *tools) update() Update {
	u := Update{Current: t.version, Latest: "unknown"}
	l, fresh := update.Cached()
	if !fresh && t.refreshing.CompareAndSwap(false, true) {
		t.bg.Add(1)
		go func() {
			defer t.bg.Done()
			defer t.refreshing.Store(false)
			update.Latest(context.Background(), t.http, t.releases)
		}()
	}
	if l != "" {
		u.Latest = l
		u.Available = update.Less(t.version, l)
	}
	return u
}

// knownRoots lists roots.json with the persisted status of each root, or the
// live one for a root this process is indexing or has queued.
func knownRoots(e *engine.Engine) []KnownRoot {
	roots, _ := engine.ReadRoots() // the engine logs an unreadable file
	out := make([]KnownRoot, 0, len(roots))
	for _, r := range roots {
		k := KnownRoot{Root: r, State: "not_indexed"}
		// The index status lives under ~/.ragcode, so it outlives a deleted root.
		if fi, err := os.Stat(r); err != nil || !fi.IsDir() {
			k.State = "missing"
			out = append(out, k)
			continue
		}
		if s, ok := index.ReadStatus(r); ok {
			k.State, k.Files = s.Phase, s.Done
			switch {
			case !s.EndedAt.IsZero():
				k.Ended = s.EndedAt.Format(time.RFC3339)
			case s.Phase != "" && !index.Running(r):
				k.State = "interrupted"
			}
		}
		if w := e.Loaded(r); w != nil {
			if s := w.Status(); s.Running || s.Queued {
				info := indexInfo(w)
				k.State, k.Files, k.Total, k.Ended = info.State, info.Done, info.Total, ""
			}
		}
		out = append(out, k)
	}
	return out
}

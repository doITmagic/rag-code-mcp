package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/doITmagic/rag-code-mcp/v2/engine"
	"github.com/doITmagic/rag-code-mcp/v2/index"
	"github.com/doITmagic/rag-code-mcp/v2/internal/config"
	"github.com/doITmagic/rag-code-mcp/v2/parser"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// ---- rag_index_workspace ----

// IndexInput is the input of rag_index_workspace.
type IndexInput struct {
	WorkspaceRoot string `json:"workspace_root,omitempty" jsonschema:"Absolute path of the project directory (required). Pass it on every call."`
	Mode          string `json:"mode,omitempty" jsonschema:"incremental (default: changed files only) or recreate (drop the collection and re-embed everything)."`
	Confirm       bool   `json:"confirm,omitempty" jsonschema:"Set true to start the run; false only reports the plan."`
}

func (t *tools) indexWorkspace(ctx context.Context, req *mcp.CallToolRequest, in IndexInput) (*mcp.CallToolResult, any, error) {
	switch in.Mode {
	case "":
		in.Mode = "incremental"
	case "incremental", "recreate":
	default:
		return fail("mode must be incremental or recreate")
	}
	w, res := t.open(ctx, req, in.WorkspaceRoot)
	if res != nil {
		return res, nil, nil
	}
	data := map[string]any{"root": w.Root, "collection": w.Collection, "mode": in.Mode}
	if st, err := index.LoadState(w.Root); in.Mode == "incremental" && err == nil && len(st.Files) > 0 && st.Format != index.Format {
		data["full_reindex"] = fmt.Sprintf("index format %d -> %d: every file is re-indexed", st.Format, index.Format)
	}
	if !in.Confirm {
		files, err := index.Walk(w.Root, t.cfg.ExcludePatterns)
		if err != nil {
			return fail(fmt.Sprintf("cannot walk %s: %v", w.Root, err))
		}
		data["estimated_files"] = len(files)
		msg := fmt.Sprintf("call again with confirm=true to run an index (%s) of %d files into %s", in.Mode, len(files), w.Collection)
		return reply("confirmation_required", msg, data, w)
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
	// The probes run together so two stalled endpoints cost one client
	// timeout, not two.
	var (
		ollama Service
		update Update
		wg     sync.WaitGroup
	)
	wg.Add(2)
	go func() { defer wg.Done(); ollama = t.probeOllama(ctx) }()
	go func() { defer wg.Done(); update = t.update(ctx) }()
	wg.Wait()
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

type updateCache struct {
	Latest    string    `json:"latest"`
	CheckedAt time.Time `json:"checked_at"`
}

// update returns the latest release tag, from HomeDir()/cache/update.json
// when checked in the last 24 h (1 h after a failed check, cached with an
// empty Latest), otherwise from GitHub.
func (t *tools) update(ctx context.Context) Update {
	u := Update{Current: t.version, Latest: "unknown"}
	path := filepath.Join(config.HomeDir(), "cache", "update.json")
	var c updateCache
	ttl := 24 * time.Hour
	data, err := os.ReadFile(path)
	if err == nil && json.Unmarshal(data, &c) == nil && c.Latest == "" {
		ttl = time.Hour
	}
	if err == nil && !c.CheckedAt.IsZero() && time.Since(c.CheckedAt) < ttl {
		if c.Latest != "" {
			u.Latest = c.Latest
		}
	} else {
		var body struct {
			TagName string `json:"tag_name"`
		}
		if err := t.getJSON(ctx, t.releases, &body); err == nil && body.TagName != "" {
			u.Latest = body.TagName
		}
		// A canceled request says nothing about GitHub; don't cache it.
		if ctx.Err() == nil {
			data, _ := json.Marshal(updateCache{Latest: body.TagName, CheckedAt: time.Now()})
			_ = index.WriteAtomic(path, data)
		}
	}
	u.Available = u.Latest != "unknown" && semverLess(t.version, u.Latest)
	return u
}

// semverLess reports a < b for "v1.2.3"-style tags; anything unparsable
// (dev builds) is never considered outdated.
func semverLess(a, b string) bool {
	parse := func(s string) ([3]int, bool) {
		var v [3]int
		s, _, _ = strings.Cut(strings.TrimPrefix(s, "v"), "-")
		parts := strings.Split(s, ".")
		if len(parts) != 3 {
			return v, false
		}
		for i, p := range parts {
			n, err := strconv.Atoi(p)
			if err != nil {
				return v, false
			}
			v[i] = n
		}
		return v, true
	}
	x, okA := parse(a)
	y, okB := parse(b)
	if !okA || !okB {
		return false
	}
	for i := range x {
		if x[i] != y[i] {
			return x[i] < y[i]
		}
	}
	return false
}

// knownRoots lists roots.json with the persisted status of each root, or the
// live one for a root this process is indexing or has queued.
func knownRoots(e *engine.Engine) []KnownRoot {
	var roots []string
	if data, err := os.ReadFile(filepath.Join(config.HomeDir(), "roots.json")); err == nil {
		_ = json.Unmarshal(data, &roots)
	}
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

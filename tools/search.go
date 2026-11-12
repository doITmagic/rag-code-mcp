package tools

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"unicode"

	"github.com/doITmagic/rag-code-mcp/v2/engine"
	"github.com/doITmagic/rag-code-mcp/v2/index"
	"github.com/doITmagic/rag-code-mcp/v2/parser"
	"github.com/doITmagic/rag-code-mcp/v2/store"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const (
	minScore       = 0.55
	exactLimit     = 20
	maxSearchLimit = 100
	compactAbove   = 4
	contentCap     = 200 // lines
	fallbackFiles  = 200
)

// SearchInput is the input of rag_search.
type SearchInput struct {
	WorkspaceRoot string   `json:"workspace_root,omitempty" jsonschema:"Absolute path of the top directory of the project the user is working in (required), the same on every call. Find it as the folder opened in the editor, or the nearest parent directory that holds .git (in a monorepo that is the repository root, not a package inside it); without git, the nearest parent that holds a project file (go.mod, package.json, composer.json, pyproject.toml, project.godot); with neither, the smallest directory that holds the files being worked on. Never go up to a directory that holds other projects, the home or a system directory, and never pass a subdirectory of the project."`
	Query         string   `json:"query" jsonschema:"An identifier or qualified name (exact match) or a natural-language description (semantic)."`
	Limit         int      `json:"limit,omitempty" jsonschema:"Maximum results, default 10, at most 100."`
	FilePath      string   `json:"file_path,omitempty" jsonschema:"File being edited; results near it rank higher."`
	Language      string   `json:"language,omitempty" jsonschema:"Only this language, as rag_server_info lists them (go, python, php, javascript for JS/TS/Vue, ...); any other is an input error."`
	PathPrefix    string   `json:"path_prefix,omitempty" jsonschema:"Only files under this directory (absolute or relative to the root)."`
	Kinds         []string `json:"kinds,omitempty" jsonschema:"Only these symbol kinds: function, method, class, interface, type, variable, constant, route, hook, template, scene, chunk."`
	Full          bool     `json:"full,omitempty" jsonschema:"Always include the symbol content."`
}

// SearchHit is one rag_search result.
type SearchHit struct {
	ID            string  `json:"id"`
	QualifiedName string  `json:"qualified_name"`
	Name          string  `json:"name"`
	Kind          string  `json:"kind"`
	Language      string  `json:"language"`
	FilePath      string  `json:"file_path"`
	StartLine     int     `json:"start_line"`
	EndLine       int     `json:"end_line"`
	Signature     string  `json:"signature,omitempty"`
	Score         float32 `json:"score"`
	Match         string  `json:"match"` // exact|name|semantic|fallback
	Content       string  `json:"content,omitempty"`
}

func (t *tools) search(ctx context.Context, req *mcp.CallToolRequest, in SearchInput) (*mcp.CallToolResult, any, error) {
	if strings.TrimSpace(in.Query) == "" {
		return fail("query is empty: pass an identifier, a qualified name or a description")
	}
	in.Limit = min(in.Limit, maxSearchLimit) // a huge limit overflowed Limit*2
	if in.Limit <= 0 {
		in.Limit = 10
	}
	ks, err := kinds(in.Kinds)
	if err != nil {
		return fail(err.Error())
	}
	if l := parser.Languages(); in.Language != "" && !slices.Contains(l, in.Language) {
		return fail(fmt.Sprintf("unknown language %q: use %s (TypeScript and Vue files are javascript)", in.Language, strings.Join(l, ", ")))
	}
	w, res := t.open(ctx, req, in.WorkspaceRoot)
	if res != nil {
		return res, nil, nil
	}
	f := store.Filter{Language: in.Language, Kinds: ks}
	if in.PathPrefix != "" {
		p, err := insideRoot(w.Root, in.PathPrefix)
		if err != nil {
			return fail(err.Error())
		}
		if fi, err := os.Stat(p); err == nil && !fi.IsDir() {
			return fail("path_prefix " + in.PathPrefix + " is a file: pass its directory, or file_path to rank that file first")
		}
		f.PathPrefix = p
	}
	var hits []SearchHit
	seen := map[twinKey]bool{}
	merge := func(hs []store.Hit) []store.Hit {
		return mergeTwins(live(w, hs), func(h *store.Hit) *parser.Symbol { return &h.Symbol })
	}
	msg := ""
	var g *grepRun
	defer func() { g.stop() }()
	if isIdentifier(in.Query) {
		g = t.startGrep(ctx, w.Root, f.PathPrefix, in.Query)
		exact, err := byName(ctx, w, f, in.Query, exactLimit)
		if err != nil {
			return t.searchErr(ctx, w, in, f, err)
		}
		exact = merge(exact)
		if len(exact) == 0 { // said plainly: the semantic hits below are not that name
			msg = fmt.Sprintf("no symbol named %q (struct fields, locals and Go interface methods are not symbols): the results are semantic neighbours only; grep finds field uses", in.Query)
			if in.Language != "" || len(ks) > 0 || f.PathPrefix != "" { // the name may exist outside the filters
				msg = fmt.Sprintf("no symbol named %q matches the kinds, language and path_prefix given (it may exist outside them): the results are semantic neighbours only", in.Query)
			}
			if st := indexInfo(w).State; st == "indexing" || st == "queued" {
				msg = fmt.Sprintf("no symbol named %q in the files indexed so far: it may be in a file not indexed yet (grep finds it now); the results are semantic neighbours only", in.Query)
			}
		}
		match := "exact"
		// A qualified name nothing has ("Base.Data" for BackendController.Data): the
		// caller guessed the owner, so the symbols of that last name are the answer.
		if q := parser.Normalize(in.Query); len(exact) == 0 && strings.Contains(q, ".") {
			last := q[strings.LastIndex(q, ".")+1:]
			if exact, err = byName(ctx, w, f, last, exactLimit); err != nil {
				return t.searchErr(ctx, w, in, f, err)
			}
			if exact = merge(exact); len(exact) > 0 {
				match = "name"
				msg = fmt.Sprintf("no symbol named %q; %d symbols are named %q under another owner (listed first, match \"name\")", in.Query, len(exact), last)
			}
		}
		if len(exact) > 0 { // the index knows the name: no text search needed
			g.stop()
			g = nil
		}
		for _, h := range exact {
			seen[twinOf(&h.Symbol)] = true
			hits = append(hits, toHit(h.Symbol, 1, match))
		}
	}
	sem, err := w.Search(ctx, in.Query, f, in.Limit*2)
	if err != nil {
		return t.searchErr(ctx, w, in, f, err)
	}
	for _, h := range merge(sem) {
		if k := twinOf(&h.Symbol); h.Score >= minScore && !seen[k] {
			seen[k] = true
			hits = append(hits, toHit(h.Symbol, h.Score, "semantic"))
		}
	}
	hits = rank(hits, w.Root, in.FilePath, in.Limit, in.Full)
	// A recreate (or a model change) refills the collection from empty: no
	// hit yet means "not indexed so far", not "does not exist". A run over an
	// index that has symbols (the start-up catch-up) is not that case.
	if s := w.Status(); len(hits) == 0 && (s.Running || s.Queued) {
		some, err := w.Scroll(ctx, store.Filter{}, 1)
		if err != nil {
			return t.searchErr(ctx, w, in, f, err)
		}
		if len(some) == 0 {
			return t.searchErr(ctx, w, in, f, engine.ErrNotIndexed)
		}
	}
	if len(hits) == 0 && (len(ks) > 0 || in.Language != "" || in.PathPrefix != "") {
		msg = emptyFilterMsg
	}
	data := map[string]any{"query": in.Query, "results": hits, "count": len(hits)}
	return reply("ok", g.add(msg, data), data, w)
}

// searchErr answers a failed store call: the bounded AST fallback while the
// index is missing, dependency_down otherwise.
func (t *tools) searchErr(ctx context.Context, w *engine.Workspace, in SearchInput, f store.Filter, err error) (*mcp.CallToolResult, any, error) {
	if !errors.Is(err, engine.ErrNotIndexed) {
		return storeErr(ctx, w, err)
	}
	hits := t.fallback(ctx, w.Root, in, f)
	hits = rank(hits, w.Root, in.FilePath, in.Limit, in.Full)
	status, msg := "not_indexed", "no index yet: results come from a bounded on-the-fly parse of up to 200 files; run rag_index_workspace"
	if s := w.Status(); s.Running || s.Queued {
		status, msg = "indexing", "index in progress: results come from a bounded on-the-fly parse of up to 200 files; retry when the index is done"
	}
	return reply(status, msg, map[string]any{"query": in.Query, "results": hits, "count": len(hits)}, w)
}

// fallback parses up to fallbackFiles files under the root (or path prefix)
// and scores their symbols by name; the file cap bounds it, not a clock.
func (t *tools) fallback(ctx context.Context, root string, in SearchInput, f store.Filter) []SearchHit {
	exts := map[string]bool{}
	if a := parser.ForLanguage(in.Language); a != nil {
		for _, e := range a.Extensions() {
			exts[e] = true
		}
	} else if ext := filepath.Ext(in.Query); ext != "" && parser.ForFile(in.Query) != nil {
		exts[strings.ToLower(ext)] = true
	}
	start := f.PathPrefix
	if start == "" {
		start = root
	}
	files := t.sourceFiles(ctx, root, start, exts, fallbackFiles)
	words := strings.Fields(strings.ToLower(in.Query))
	var hits []SearchHit
	for _, path := range files {
		if ctx.Err() != nil {
			break
		}
		src, err := os.ReadFile(path)
		if err != nil || bytes.IndexByte(src, 0) >= 0 {
			continue
		}
		syms, err := parser.Analyze(parser.File{Path: path, Root: root, Source: src})
		if err != nil {
			continue
		}
		for _, s := range syms {
			if len(f.Kinds) > 0 && !slices.Contains(f.Kinds, s.Kind) {
				continue
			}
			if score := nameScore(s, in.Query, words); score > 0 {
				hits = append(hits, toHit(s, score, "fallback"))
			}
		}
	}
	return hits
}

// sourceFiles lists at most max files under start that an analyzer handles
// (only those with one of exts, when given), by the index rules: no hidden or
// excluded directory, no linked worktree, not RagCode's own home, nothing Skip
// or git leaves out. A start in or under such a directory lists nothing.
func (t *tools) sourceFiles(ctx context.Context, root, start string, exts map[string]bool, max int) []string {
	home := index.OwnHome()
	pruned := func(dir string) bool {
		name := filepath.Base(dir)
		return strings.HasPrefix(name, ".") || slices.Contains(t.cfg.ExcludePatterns, name) || index.IsLinkedWorktree(dir) || index.InOwnHome(dir, home)
	}
	for dir := start; len(dir) > len(root); dir = filepath.Dir(dir) { // start and its parents below root
		if pruned(dir) {
			return nil
		}
	}
	var files []string
	kept := 0 // files[:kept] went through git already
	gitFilter := func() {
		tail := files[kept:]
		if ign := index.GitIgnored(root, tail); len(ign) > 0 {
			tail = slices.DeleteFunc(tail, func(f string) bool { return ign[f] })
		}
		files = files[:kept+len(tail)]
		kept = len(files)
	}
	_ = filepath.WalkDir(start, func(path string, d fs.DirEntry, err error) error {
		if len(files) >= max {
			gitFilter() // the cap counts the files git keeps
		}
		if ctx.Err() != nil || len(files) >= max {
			return fs.SkipAll
		}
		if err != nil {
			return nil // unreadable entry: skip it, keep walking (as index.Walk does)
		}
		if d.IsDir() {
			if path != start && pruned(path) {
				return fs.SkipDir
			}
			return nil
		}
		if parser.ForFile(path) == nil || (len(exts) > 0 && !exts[strings.ToLower(filepath.Ext(path))]) {
			return nil
		}
		// The index rules: no symlink, fifo, secret, lockfile, minified or
		// file over the index size cap; the binary check runs on the bytes read below.
		if info, err := d.Info(); err != nil || index.Skip(path, info, nil) != "" {
			return nil
		}
		files = append(files, path)
		return nil
	})
	gitFilter()
	return files
}

// nameScore ranks a symbol against the query without an index: exact name
// 1.0, qualified suffix 0.9, substring 0.7, otherwise the share of query
// words found in the name.
func nameScore(s parser.Symbol, query string, words []string) float32 {
	lname := strings.ToLower(s.Name)
	switch {
	case s.Name == query:
		return 1
	case parser.MatchQualified(s.QualifiedName, query):
		return 0.9
	case strings.Contains(lname, strings.ToLower(query)):
		return 0.7
	}
	n := 0
	for _, wd := range words {
		if strings.Contains(lname, wd) {
			n++
		}
	}
	if n == 0 || len(words) == 0 {
		return 0
	}
	return 0.6 * float32(n) / float32(len(words))
}

// rank applies the path boost, sorts, cuts and trims the content.
func rank(hits []SearchHit, root, current string, limit int, full bool) []SearchHit {
	if current != "" {
		if p, err := insideRoot(root, current); err == nil {
			current = p
		}
		curDir := filepath.Dir(current)
		curTop := topDir(root, current)
		curTest := isTestFile(current)
		for i := range hits {
			h := &hits[i]
			switch {
			case filepath.Dir(h.FilePath) == curDir:
				h.Score *= 1.15
			case curTop != "" && topDir(root, h.FilePath) == curTop:
				h.Score *= 1.05
			}
			if !curTest && isTestFile(h.FilePath) {
				h.Score *= 0.9
			}
		}
	}
	sort.SliceStable(hits, func(i, j int) bool {
		if hits[i].Score != hits[j].Score {
			return hits[i].Score > hits[j].Score
		}
		return hits[i].QualifiedName < hits[j].QualifiedName
	})
	if len(hits) > limit {
		hits = hits[:limit]
	}
	for i := range hits {
		if len(hits) > compactAbove && !full {
			hits[i].Content = ""
		} else {
			hits[i].Content = capLines(hits[i].Content, contentCap)
		}
	}
	if hits == nil {
		hits = []SearchHit{}
	}
	return hits
}

// topDir is the first path segment of path under root, "" when outside.
func topDir(root, path string) string {
	rel, err := filepath.Rel(root, path)
	if err != nil || strings.HasPrefix(rel, "..") {
		return ""
	}
	top, _, _ := strings.Cut(filepath.ToSlash(rel), "/")
	return top
}

func toHit(s parser.Symbol, score float32, match string) SearchHit {
	return SearchHit{ID: s.ID, QualifiedName: s.QualifiedName, Name: s.Name, Kind: string(s.Kind), Language: s.Language,
		FilePath: s.FilePath, StartLine: s.StartLine, EndLine: s.EndLine, Signature: s.Signature, Score: score, Match: match, Content: s.Content}
}

// isIdentifier reports whether q looks like a name or qualified name: no
// whitespace, only identifier characters and separators, at least one letter.
func isIdentifier(q string) bool {
	letter := false
	for _, r := range q {
		switch {
		case unicode.IsLetter(r):
			letter = true
		case unicode.IsDigit(r), strings.ContainsRune("_.:/\\$-", r):
		default:
			return false
		}
	}
	return letter
}

var allKinds = []parser.Kind{parser.KindFunction, parser.KindMethod, parser.KindClass, parser.KindInterface, parser.KindType, parser.KindVariable,
	parser.KindConstant, parser.KindRoute, parser.KindHook, parser.KindTemplate, parser.KindScene, parser.KindChunk}

// kinds parses a kinds filter; an unknown kind is an input error.
func kinds(ss []string) ([]parser.Kind, error) {
	var out []parser.Kind
	for _, s := range ss {
		if s = strings.TrimSpace(s); s == "" {
			continue
		}
		if !slices.Contains(allKinds, parser.Kind(s)) {
			valid := make([]string, len(allKinds))
			for i, k := range allKinds {
				valid[i] = string(k)
			}
			return nil, fmt.Errorf("unknown kind %q: use %s", s, strings.Join(valid, ", "))
		}
		out = append(out, parser.Kind(s))
	}
	return out, nil
}

// emptyFilterMsg explains a filtered search with no hit.
const emptyFilterMsg = "no symbol matches these filters: widen or drop language, path_prefix or kinds"

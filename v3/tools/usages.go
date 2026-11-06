package tools

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/doITmagic/rag-code-mcp/v3/engine"
	"github.com/doITmagic/rag-code-mcp/v3/parser"
	"github.com/doITmagic/rag-code-mcp/v3/store"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const (
	candidateLimit = 50
	maxNodes       = 100
	maxDepth       = 4
	callLineCap    = 200 // bytes
)

// Candidate is one symbol matching a name, used in ambiguous answers.
type Candidate struct {
	ID            string `json:"id"`
	QualifiedName string `json:"qualified_name"`
	Kind          string `json:"kind"`
	FilePath      string `json:"file_path"`
	StartLine     int    `json:"start_line"`
}

func toCandidate(s parser.Symbol) Candidate {
	return Candidate{ID: s.ID, QualifiedName: s.QualifiedName, Kind: string(s.Kind), FilePath: s.FilePath, StartLine: s.StartLine}
}

// resolveSymbol finds the one symbol named by query. The result is nil with
// a non-nil answer when there is none or several (ambiguous).
func resolveSymbol(ctx context.Context, w *engine.Workspace, query, filePath string) (*parser.Symbol, *mcp.CallToolResult) {
	hits, err := w.Scroll(ctx, store.Filter{QualifiedSuffix: parser.Normalize(query)}, candidateLimit)
	if err != nil {
		res, _, _ := storeErr(ctx, w, err)
		return nil, res
	}
	if hits = live(w, hits); len(hits) == 0 {
		res, _, _ := reply("ok", fmt.Sprintf("no indexed symbol named %q; try rag_search", query), nil, w)
		return nil, res
	}
	if filePath != "" && len(hits) > 1 {
		if p, err := insideRoot(w.Root, filePath); err == nil {
			var same []store.Hit
			for _, h := range hits {
				if h.Symbol.FilePath == p {
					same = append(same, h)
				}
			}
			if len(same) > 0 {
				hits = same
			}
		}
	}
	if len(hits) > 1 {
		cands := make([]Candidate, len(hits))
		for i, h := range hits {
			cands[i] = toCandidate(h.Symbol)
		}
		sort.Slice(cands, func(i, j int) bool { return cands[i].QualifiedName < cands[j].QualifiedName })
		res, _, _ := reply("ambiguous", fmt.Sprintf("%d symbols match %q: pass the full qualified name or file_path", len(hits), query),
			map[string]any{"candidates": cands}, w)
		return nil, res
	}
	s := hits[0].Symbol
	return &s, nil
}

// ---- rag_find_usages ----

// UsagesInput is the input of rag_find_usages.
type UsagesInput struct {
	WorkspaceRoot string `json:"workspace_root,omitempty" jsonschema:"Absolute path of the project directory (required). Pass it on every call."`
	Symbol        string `json:"symbol" jsonschema:"Name or qualified name of the symbol, e.g. User.find or App\\Models\\User::find."`
	FilePath      string `json:"file_path,omitempty" jsonschema:"File that defines the symbol, to pick it when several share the name."`
	Limit         int    `json:"limit,omitempty" jsonschema:"Maximum referencing symbols, default 100."`
}

// Usage is one reference to the symbol.
type Usage struct {
	Caller    string `json:"caller"` // qualified name of the referencing symbol
	Kind      string `json:"kind"`   // relation kind: call, extends, implements, uses_type, imports
	Line      int    `json:"line"`
	Text      string `json:"text,omitempty"`
	CallerID  string `json:"caller_id"`
	CallerDef int    `json:"caller_start_line"`
}

// FileUsages groups the usages of one file.
type FileUsages struct {
	FilePath string  `json:"file_path"`
	Usages   []Usage `json:"usages"`
}

func (t *tools) findUsages(ctx context.Context, req *mcp.CallToolRequest, in UsagesInput) (*mcp.CallToolResult, any, error) {
	if strings.TrimSpace(in.Symbol) == "" {
		return fail("symbol is empty: pass the name or qualified name of the symbol")
	}
	if in.Limit <= 0 {
		in.Limit = 100
	}
	w, res := t.open(ctx, req, in.WorkspaceRoot)
	if res != nil {
		return res, nil, nil
	}
	sym, res := resolveSymbol(ctx, w, in.Symbol, in.FilePath)
	if res != nil {
		return res, nil, nil
	}
	callers, err := referrers(ctx, w, sym, in.Limit)
	if err != nil {
		return storeErr(ctx, w, err)
	}
	byFile := map[string]*FileUsages{}
	lines := map[string][]string{}
	total := 0
	for _, c := range callers {
		for _, r := range c.Relations {
			if r.TargetID != sym.ID && (r.TargetQualifiedName == "" || r.TargetQualifiedName != sym.QualifiedName) {
				continue
			}
			g := byFile[c.FilePath]
			if g == nil {
				g = &FileUsages{FilePath: c.FilePath}
				byFile[c.FilePath] = g
			}
			if _, ok := lines[c.FilePath]; !ok {
				lines[c.FilePath] = readLines(w.Root, c.FilePath)
			}
			g.Usages = append(g.Usages, Usage{Caller: c.QualifiedName, Kind: string(r.Kind), Line: r.Line,
				Text: lineText(lines[c.FilePath], r.Line), CallerID: c.ID, CallerDef: c.StartLine})
			total++
		}
	}
	files := make([]FileUsages, 0, len(byFile))
	for _, g := range byFile {
		sort.Slice(g.Usages, func(i, j int) bool { return g.Usages[i].Line < g.Usages[j].Line })
		files = append(files, *g)
	}
	sort.Slice(files, func(i, j int) bool { return files[i].FilePath < files[j].FilePath })
	ambiguous := ambiguousByName(ctx, w, sym)
	data := map[string]any{"symbol": toCandidate(*sym), "files": files, "total": total, "ambiguous_by_name": ambiguous}
	msg := fmt.Sprintf("%d usages in %d files", total, len(files))
	if ambiguous > 0 {
		msg += fmt.Sprintf("; %d more relations name %q but could not be resolved to one symbol", ambiguous, sym.Name)
	}
	return reply("ok", msg, data, w)
}

// referrers lists the symbols with a relation to sym, by target id and by
// target qualified name, deduplicated.
func referrers(ctx context.Context, w *engine.Workspace, sym *parser.Symbol, limit int) ([]parser.Symbol, error) {
	byID, err := w.Scroll(ctx, store.Filter{TargetIDs: []string{sym.ID}}, limit)
	if err != nil {
		return nil, err
	}
	byQN, err := w.Scroll(ctx, store.Filter{TargetQualifiedNames: []string{sym.QualifiedName}}, limit)
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	var out []parser.Symbol
	for _, h := range live(w, append(byID, byQN...)) {
		if !seen[h.Symbol.ID] {
			seen[h.Symbol.ID] = true
			out = append(out, h.Symbol)
		}
	}
	return out, nil
}

// ambiguousByName counts the relations that name the symbol but were left
// unresolved because several symbols share the name; 0 on a store error.
// Those naming its qualified name are already listed as usages.
func ambiguousByName(ctx context.Context, w *engine.Workspace, sym *parser.Symbol) int {
	hits, err := w.Scroll(ctx, store.Filter{TargetNames: []string{sym.Name}}, store.MaxScroll)
	if err != nil {
		return 0
	}
	n := 0
	for _, h := range hits {
		for _, r := range h.Symbol.Relations {
			if r.TargetName == sym.Name && r.Resolution == parser.ResolutionAmbiguous && r.TargetQualifiedName != sym.QualifiedName {
				n++
			}
		}
	}
	return n
}

func lineText(lines []string, n int) string {
	if n < 1 || n > len(lines) {
		return ""
	}
	s := strings.TrimSpace(lines[n-1])
	if len(s) > callLineCap {
		i := callLineCap
		for i > 0 && !utf8.RuneStart(s[i]) {
			i--
		}
		s = s[:i] + "…"
	}
	return s
}

// ---- rag_call_hierarchy ----

// HierarchyInput is the input of rag_call_hierarchy.
type HierarchyInput struct {
	WorkspaceRoot string `json:"workspace_root,omitempty" jsonschema:"Absolute path of the project directory (required). Pass it on every call."`
	Symbol        string `json:"symbol" jsonschema:"Name or qualified name of the function or method."`
	Direction     string `json:"direction,omitempty" jsonschema:"incoming (callers), outgoing (callees) or both; default incoming."`
	Depth         int    `json:"depth,omitempty" jsonschema:"Levels to expand, default 2, max 4."`
	FilePath      string `json:"file_path,omitempty" jsonschema:"File that defines the symbol, to pick it when several share the name."`
}

// Node is one symbol in the hierarchy tree.
type Node struct {
	Candidate
	Children []*Node `json:"children,omitempty"`
}

type walker struct {
	w       *engine.Workspace
	visited map[string]bool
	nodes   int
	err     error
}

func (t *tools) callHierarchy(ctx context.Context, req *mcp.CallToolRequest, in HierarchyInput) (*mcp.CallToolResult, any, error) {
	if strings.TrimSpace(in.Symbol) == "" {
		return fail("symbol is empty: pass the name or qualified name of the function")
	}
	switch in.Direction {
	case "":
		in.Direction = "incoming"
	case "incoming", "outgoing", "both":
	default:
		return fail("direction must be incoming, outgoing or both")
	}
	if in.Depth <= 0 {
		in.Depth = 2
	}
	in.Depth = min(in.Depth, maxDepth)
	w, res := t.open(ctx, req, in.WorkspaceRoot)
	if res != nil {
		return res, nil, nil
	}
	sym, res := resolveSymbol(ctx, w, in.Symbol, in.FilePath)
	if res != nil {
		return res, nil, nil
	}
	data := map[string]any{"symbol": toCandidate(*sym), "direction": in.Direction, "depth": in.Depth}
	var text strings.Builder
	for _, dir := range []string{"incoming", "outgoing"} {
		if in.Direction != "both" && in.Direction != dir {
			continue
		}
		wk := &walker{w: w, visited: map[string]bool{sym.ID: true}, nodes: 1}
		root := &Node{Candidate: toCandidate(*sym)}
		wk.expand(ctx, root, *sym, dir == "incoming", in.Depth)
		if wk.err != nil {
			return storeErr(ctx, w, wk.err)
		}
		data[dir] = root
		fmt.Fprintf(&text, "%s:\n", dir)
		writeTree(&text, root, 1)
		if wk.nodes >= maxNodes {
			fmt.Fprintf(&text, "  … capped at %d nodes\n", maxNodes)
			data[dir+"_truncated"] = true
		}
	}
	data["text"] = strings.TrimRight(text.String(), "\n")
	return reply("ok", "", data, w)
}

// expand fills n's children: the symbols referring to it (incoming) or the
// resolved targets of its own relations (outgoing).
func (wk *walker) expand(ctx context.Context, n *Node, sym parser.Symbol, incoming bool, depth int) {
	if depth == 0 || wk.err != nil || wk.nodes >= maxNodes {
		return
	}
	var next []parser.Symbol
	if incoming {
		callers, err := referrers(ctx, wk.w, &sym, candidateLimit)
		if err != nil {
			wk.err = err
			return
		}
		next = callers
	} else {
		seen := map[string]bool{}
		for _, r := range sym.Relations {
			// An ambiguous call naming a qualified name (build-tagged
			// variants of one function) lists every variant, as incoming does.
			f, key, n := store.Filter{IDs: []string{r.TargetID}}, r.TargetID, 1
			if r.TargetID == "" && r.Resolution == parser.ResolutionAmbiguous && r.TargetQualifiedName != "" {
				f, key, n = store.Filter{QualifiedName: r.TargetQualifiedName}, "qn:"+r.TargetQualifiedName, candidateLimit
			}
			if r.Kind != parser.RelCall || key == "" || seen[key] {
				continue
			}
			seen[key] = true
			hits, err := wk.w.Scroll(ctx, f, n)
			if err != nil {
				wk.err = err
				return
			}
			for _, h := range live(wk.w, hits) {
				next = append(next, h.Symbol)
			}
		}
	}
	sort.Slice(next, func(i, j int) bool { return next[i].QualifiedName < next[j].QualifiedName })
	for _, s := range next {
		if wk.nodes >= maxNodes {
			return
		}
		child := &Node{Candidate: toCandidate(s)}
		n.Children = append(n.Children, child)
		wk.nodes++
		if wk.visited[s.ID] {
			continue // already expanded elsewhere: listed, not re-walked
		}
		wk.visited[s.ID] = true
		wk.expand(ctx, child, s, incoming, depth-1)
	}
}

func writeTree(b *strings.Builder, n *Node, indent int) {
	fmt.Fprintf(b, "%s%s (%s:%d)\n", strings.Repeat("  ", indent), n.QualifiedName, n.FilePath, n.StartLine)
	for _, c := range n.Children {
		writeTree(b, c, indent+1)
	}
}

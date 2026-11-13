package tools

import (
	"context"
	"fmt"
	"slices"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/doITmagic/rag-code-mcp/v2/engine"
	"github.com/doITmagic/rag-code-mcp/v2/parser"
	"github.com/doITmagic/rag-code-mcp/v2/store"
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
func resolveSymbol(ctx context.Context, w *engine.Workspace, query, filePath string, g *grepRun) (*parser.Symbol, *mcp.CallToolResult) {
	var in store.Filter // the file narrows the lookup itself: a common name has more symbols than candidateLimit
	if filePath != "" {
		p, err := insideRoot(w.Root, filePath)
		if err != nil {
			res, _, _ := fail(err.Error())
			return nil, res
		}
		in.FilePath = p
	}
	hits, err := byName(ctx, w, in, query, candidateLimit)
	if err == nil && len(live(w, hits)) == 0 && isID(query) { // a candidate's id from an ambiguous answer
		hits, err = w.Scroll(ctx, store.Filter{IDs: []string{query}}, 1)
	}
	if err == nil && len(live(w, hits)) == 0 && filePath != "" {
		hits, err = byName(ctx, w, store.Filter{}, query, candidateLimit) // for the "elsewhere" answer below
	}
	if err != nil {
		res, _, _ := storeErr(ctx, w, err)
		return nil, res
	}
	if hits = live(w, hits); len(hits) == 0 {
		data := map[string]any{}
		msg := g.add(fmt.Sprintf("no indexed symbol named %q; try rag_search", query), data)
		if len(data) == 0 {
			res, _, _ := reply("ok", msg, nil, w)
			return nil, res
		}
		res, _, _ := reply("ok", msg, data, w)
		return nil, res
	}
	if p := in.FilePath; p != "" {
		var same []store.Hit
		for _, h := range hits {
			if h.Symbol.FilePath == p {
				same = append(same, h)
			}
		}
		if len(same) == 0 {
			res, _, _ := reply("ok", fmt.Sprintf("no indexed symbol named %q in %s (%d elsewhere); fix file_path or drop it", query, p, len(hits)), nil, w)
			return nil, res
		}
		hits = same
	}
	if len(hits) > 1 { // a Godot scene node shares the qualified name of its script's class: the class wins
		code := map[string]bool{}
		for _, h := range hits {
			if h.Symbol.Kind != parser.KindScene {
				code[h.Symbol.QualifiedName] = true
			}
		}
		kept := hits[:0:0]
		for _, h := range hits {
			if h.Symbol.Kind != parser.KindScene || !code[h.Symbol.QualifiedName] {
				kept = append(kept, h)
			}
		}
		hits = kept
	}
	if len(hits) > 1 { // a doc heading named like a code symbol (README "### CleanupTask"): the code symbol wins
		var code []store.Hit
		for _, h := range hits {
			if h.Symbol.Kind != parser.KindChunk {
				code = append(code, h)
			}
		}
		if len(code) > 0 {
			hits = code
		}
	}
	hits = mergeTwins(hits, func(h *store.Hit) *parser.Symbol { return &h.Symbol })
	if len(hits) > 1 { // a property and a method of one name: the method, reads of a property are not tracked
		fn := map[twinKey]bool{}
		for _, h := range hits {
			if k := twinOf(&h.Symbol); k.fn {
				fn[twinKey{k.qn, k.file, false}] = true
			}
		}
		hits = slices.DeleteFunc(hits, func(h store.Hit) bool { return fn[twinOf(&h.Symbol)] })
	}
	if len(hits) > 1 { // the one whose qualified name is the query beats those that only end with it
		q := parser.Normalize(query)
		var equal []store.Hit
		for _, h := range hits {
			if parser.Normalize(h.Symbol.QualifiedName) == q {
				equal = append(equal, h)
			}
		}
		if len(equal) == 1 {
			hits = equal
		}
	}
	if len(hits) > 1 {
		cands := make([]Candidate, len(hits))
		for i, h := range hits {
			cands[i] = toCandidate(h.Symbol)
		}
		sort.Slice(cands, func(i, j int) bool { return cands[i].QualifiedName < cands[j].QualifiedName })
		msg := fmt.Sprintf("%d symbols match %q: pass the full qualified name or file_path, or a candidate's id as symbol", len(hits), query)
		if c := cands[0]; c.QualifiedName == cands[len(cands)-1].QualifiedName && !slices.ContainsFunc(cands, func(o Candidate) bool { return o.FilePath != c.FilePath }) {
			// neither a longer name nor file_path tells them apart (routes of one path, doc sections with one heading)
			msg = fmt.Sprintf("%d symbols in %s share the qualified name %q: data.candidates lists each with its line; pass a candidate's id as symbol", len(hits), c.FilePath, c.QualifiedName)
		}
		res, _, _ := reply("ambiguous", msg, map[string]any{"candidates": cands}, w)
		return nil, res
	}
	s := hits[0].Symbol
	return &s, nil
}

// twinKey groups twins: overload signatures, a get/set pair or a merged
// declaration share a qualified name and a file. Doc chunks (two sections
// with one heading) and routes (GET and DELETE of one path) are never twins,
// nor a function or method and another kind of one name (a PHP property and
// its accessor method), as the index resolves.
type twinKey struct {
	qn, file string
	fn       bool
}

func twinOf(s *parser.Symbol) twinKey {
	if s.Kind == parser.KindChunk || s.Kind == parser.KindRoute {
		return twinKey{qn: s.ID}
	}
	return twinKey{s.QualifiedName, s.FilePath, s.Kind == parser.KindFunction || s.Kind == parser.KindMethod}
}

// mergeTwins keeps one item per twin group, in first-seen order: the widest
// (the implementation, with the body), then the first line, then the lowest
// id, as the index picks a call's target. It carries the relations of every
// twin (outgoing calls of a getter and a setter).
func mergeTwins[T any](items []T, sym func(*T) *parser.Symbol) []T {
	at := map[twinKey]int{} // index in kept
	kept := items[:0:0]
	for _, it := range items {
		s := sym(&it)
		i, ok := at[twinOf(s)]
		if !ok {
			at[twinOf(s)] = len(kept)
			kept = append(kept, it)
			continue
		}
		r := sym(&kept[i])
		rels := append(r.Relations[:len(r.Relations):len(r.Relations)], s.Relations...)
		if w, rw := s.EndLine-s.StartLine, r.EndLine-r.StartLine; w > rw || w == rw && (s.StartLine < r.StartLine || s.StartLine == r.StartLine && s.ID < r.ID) {
			kept[i] = it
			r = sym(&kept[i])
		}
		r.Relations = rels
	}
	return kept
}

// byName lists the symbols whose qualified name ends with query and, for a
// plain name, those named query: a JS default export is named after its
// declaration but qualified as "<module>.default".
func byName(ctx context.Context, w *engine.Workspace, f store.Filter, query string, limit int) ([]store.Hit, error) {
	q := parser.Normalize(query)
	f.QualifiedSuffix = q
	hits, err := w.Scroll(ctx, f, limit)
	if err != nil || strings.Contains(q, ".") {
		return hits, err
	}
	f.QualifiedSuffix, f.Name = "", q
	named, err := w.Scroll(ctx, f, limit)
	if err != nil {
		return nil, err
	}
	seen := make(map[string]bool, len(hits))
	for _, h := range hits {
		seen[h.Symbol.ID] = true
	}
	for _, h := range named {
		if !seen[h.Symbol.ID] {
			hits = append(hits, h)
		}
	}
	return hits, nil
}

// ---- rag_find_usages ----

// UsagesInput is the input of rag_find_usages.
type UsagesInput struct {
	WorkspaceRoot string `json:"workspace_root,omitempty" jsonschema:"Absolute path of the top directory of the project the user is working in (required), the same on every call. Find it as the folder opened in the editor, or the nearest parent directory that holds .git (in a monorepo that is the repository root, not a package inside it); without git, the nearest parent that holds a project file (go.mod, package.json, composer.json, pyproject.toml, project.godot); with neither, the smallest directory that holds the files being worked on. Never go up to a directory that holds other projects, the home or a system directory, and never pass a subdirectory of the project."`
	Symbol        string `json:"symbol" jsonschema:"Name or qualified name of the symbol, e.g. User.find or App\\Models\\User::find, or a candidate id from an ambiguous answer."`
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
	ByName    bool   `json:"by_name,omitempty"` // matched by name only: the receiver's type is unknown, check it
	Via       string `json:"via,omitempty"`     // a call of this interface's method (qualified name), which the symbol implements
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
	g := t.startGrep(ctx, w.Root, "", in.Symbol)
	defer g.stop()
	sym, res := resolveSymbol(ctx, w, in.Symbol, in.FilePath, g)
	if res != nil {
		return res, nil, nil
	}
	// A Vue component is used through its default symbol (import Card from
	// './Card.vue', <Card />); the template symbol only carries its name.
	if sym.Kind == parser.KindTemplate && strings.HasSuffix(sym.FilePath, ".vue") {
		if d, err := byName(ctx, w, store.Filter{FilePath: sym.FilePath}, sym.Module+".default", 1); err == nil && len(live(w, d)) == 1 {
			sym = &d[0].Symbol
		}
	}
	callers, cut, err := referrers(ctx, w, sym, in.Limit)
	if err != nil {
		return storeErr(ctx, w, err)
	}
	byFile := map[string]*FileUsages{}
	lines := map[string][]string{}
	total, guessed := 0, 0
	add := func(callers []parser.Symbol, sym *parser.Symbol, via string) {
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
				guess := !exactRel(r, sym.QualifiedName)
				g.Usages = append(g.Usages, Usage{Caller: c.QualifiedName, Kind: string(r.Kind), Line: r.Line,
					Text: lineText(lines[c.FilePath], r.Line), CallerID: c.ID, CallerDef: c.StartLine, ByName: guess, Via: via})
				total++
				if guess {
					guessed++
				}
			}
		}
	}
	add(callers, sym, "")
	viaCount, n := total, len(callers) // n: referencing symbols, direct and via, within one limit
	for _, m := range viaInterfaces(ctx, w, sym) {
		vc, vcut, err := referrers(ctx, w, &m, in.Limit-n)
		if err != nil {
			return storeErr(ctx, w, err)
		}
		cut, n = cut || vcut, n+len(vc)
		add(vc, &m, ownerQN(&m))
	}
	viaCount = total - viaCount
	files := make([]FileUsages, 0, len(byFile))
	for _, g := range byFile {
		sort.Slice(g.Usages, func(i, j int) bool { return g.Usages[i].Line < g.Usages[j].Line })
		files = append(files, *g)
	}
	sort.Slice(files, func(i, j int) bool { return files[i].FilePath < files[j].FilePath })
	ambiguous := ambiguousByName(ctx, w, sym)
	data := map[string]any{"symbol": toCandidate(*sym), "files": files, "total": total, "ambiguous_by_name": ambiguous, "by_name": guessed, "truncated": cut}
	msg := fmt.Sprintf("%d usages in %d files", total, len(files))
	switch {
	case cut && n == in.Limit && in.Limit < store.MaxScroll-1:
		msg += fmt.Sprintf("; only the first %d referencing symbols: raise limit for the rest", in.Limit)
	case cut && n == store.MaxScroll-1:
		msg += fmt.Sprintf("; only the first %d referencing symbols, the most one answer holds: grep the name for the rest", n)
	case cut: // fewer than limit: rows of files deleted from disk filled the store's scroll
		msg += fmt.Sprintf("; the store scroll (%d rows) filled up with callers in files deleted from disk, more may follow: grep the name for the rest", store.MaxScroll)
	}
	if viaCount > 0 {
		msg += fmt.Sprintf("; %d are calls of an interface's method it implements, marked via (the object may be another implementation)", viaCount)
	}
	if guessed > 0 {
		msg += fmt.Sprintf("; %d matched by name only (no import, qualified name or known type links them; it may be another symbol of that name), marked by_name", guessed)
	}
	if ambiguous > 0 {
		msg += fmt.Sprintf("; %d more relations name %q but could not be resolved to one symbol", ambiguous, sym.Name)
	}
	if total == 0 { // nothing in the index: show where the text has the name
		msg = g.add(msg, data)
	}
	if used := usedMembers(ctx, w, sym, total); len(used) > 0 {
		msg += fmt.Sprintf("; %s itself is not called, but its members are: %s (rag_find_usages on a member lists the calls)", sym.Name, strings.Join(used, ", "))
	} else if total == 0 && (sym.Kind == parser.KindConstant || sym.Kind == parser.KindVariable) {
		msg += "; reads of constants and variables are not tracked, only calls, type uses and inheritance (an import is not a usage): try rag_search"
	}
	return reply("ok", msg, data, w)
}

// isID is true for a symbol id: 32 lowercase hex digits.
func isID(s string) bool {
	return len(s) == 32 && strings.Trim(s, "0123456789abcdef") == ""
}

// ownerQN is the qualified name of the class that declares member s.
func ownerQN(s *parser.Symbol) string { return strings.TrimSuffix(s.QualifiedName, "."+s.Name) }

// viaInterfaces lists the methods named like sym of the workspace interfaces
// its PHP class implements: directly, through a parent class or through an
// interface extending them; for a trait method, the classes using the trait. A call through an interface-typed variable
// targets the interface's method only, so its callers are sym's too.
// shortcut: PHP only, other languages' implements relations are not walked; extend when one asks for it.
func viaInterfaces(ctx context.Context, w *engine.Workspace, sym *parser.Symbol) (out []parser.Symbol) {
	if sym.Language != "php" || sym.Kind != parser.KindMethod || sym.Owner == "" {
		return nil
	}
	seen := map[string]bool{}
	owner := ownerQN(sym)
	queue := []store.Filter{{Language: "php", Kinds: []parser.Kind{parser.KindClass}, QualifiedName: owner}}
	// and the classes using it as a trait: a class's only uses_type relations
	// are its "use T;" lines (types in bodies belong to its members)
	if users, err := w.Scroll(ctx, store.Filter{Language: "php", Kinds: []parser.Kind{parser.KindClass}, TargetQualifiedNames: []string{owner}}, candidateLimit); err == nil {
		for _, h := range live(w, users) {
			if slices.ContainsFunc(h.Symbol.Relations, func(r parser.Relation) bool {
				return r.Kind == parser.RelUsesType && r.TargetQualifiedName == owner
			}) {
				queue = append(queue, store.Filter{IDs: []string{h.Symbol.ID}})
			}
		}
	}
	for len(queue) > 0 && len(seen) < candidateLimit {
		hits, err := w.Scroll(ctx, queue[0], candidateLimit)
		queue = queue[1:]
		if err != nil {
			return out
		}
		for _, h := range live(w, hits) {
			t := h.Symbol
			if seen[t.ID] || (t.Kind != parser.KindClass && t.Kind != parser.KindInterface) {
				continue
			}
			seen[t.ID] = true
			if t.Kind == parser.KindInterface {
				if m, err := w.Scroll(ctx, store.Filter{Language: "php", Kinds: []parser.Kind{parser.KindMethod}, QualifiedName: t.QualifiedName + "." + sym.Name}, candidateLimit); err == nil {
					for _, h := range live(w, m) {
						out = append(out, h.Symbol)
					}
				}
			}
			for _, r := range t.Relations {
				switch {
				case r.Kind != parser.RelExtends && r.Kind != parser.RelImplements:
				case r.TargetID != "":
					queue = append(queue, store.Filter{IDs: []string{r.TargetID}})
				case r.TargetQualifiedName != "":
					queue = append(queue, store.Filter{Language: "php", QualifiedName: r.TargetQualifiedName})
				}
			}
		}
	}
	return out
}

// usedMembers names the members of an unused constant or variable (an object
// of functions, as "export const api = { list: ... }") that something calls,
// at most 10: zero usages of the object must not read as dead code.
func usedMembers(ctx context.Context, w *engine.Workspace, sym *parser.Symbol, total int) (used []string) {
	if total > 0 || (sym.Kind != parser.KindConstant && sym.Kind != parser.KindVariable) {
		return nil
	}
	hits, err := w.Scroll(ctx, store.Filter{FilePath: sym.FilePath}, store.MaxScroll)
	if err != nil {
		return nil
	}
	for _, h := range hits {
		rest, ok := strings.CutPrefix(h.Symbol.QualifiedName, sym.QualifiedName+".")
		if !ok || len(used) == 10 {
			continue
		}
		refs, _, _ := referrers(ctx, w, &h.Symbol, 20)
		if slices.ContainsFunc(refs, func(c parser.Symbol) bool { // a call bound by name only may be another symbol's
			return slices.ContainsFunc(c.Relations, func(r parser.Relation) bool {
				return (r.TargetID == h.Symbol.ID || r.TargetQualifiedName == h.Symbol.QualifiedName) && exactRel(r, h.Symbol.QualifiedName)
			})
		}) {
			used = append(used, sym.Name+"."+rest)
		}
	}
	sort.Strings(used)
	return used
}

// referrers lists at most limit symbols with a relation to sym, by target
// id and by target qualified name, deduplicated; cut says there were more.
// The limit counts live symbols: when files gone from disk took rows of a
// full scroll, it scrolls again up to MaxScroll.
func referrers(ctx context.Context, w *engine.Workspace, sym *parser.Symbol, limit int) (out []parser.Symbol, cut bool, err error) {
	limit = min(limit, store.MaxScroll-1) // one more than limit must fit a Scroll, or cut is never seen
	for n := limit + 1; ; n = store.MaxScroll {
		byID, err := w.Scroll(ctx, store.Filter{TargetIDs: []string{sym.ID}}, n)
		if err != nil {
			return nil, false, err
		}
		full, rows := len(byID) == n, len(byID)
		// A qualified name shared with another language's symbol is not this
		// one's: as the index resolves, code never calls across languages
		// (docs and html link into code on purpose). Each language is its own
		// scroll, so another language's rows never fill the cap.
		for _, l := range slices.Compact([]string{sym.Language, "docs", "html"}) {
			hs, err := w.Scroll(ctx, store.Filter{Language: l, TargetQualifiedNames: []string{sym.QualifiedName}}, n)
			if err != nil {
				return nil, false, err
			}
			full, rows = full || len(hs) == n, rows+len(hs)
			byID = append(byID, hs...)
		}
		alive := live(w, byID)
		// rows past a full scroll were not seen: dropped ones leave room for them
		unseen := full && len(alive) < rows
		if unseen && n < store.MaxScroll {
			continue
		}
		seen := map[string]bool{}
		for _, h := range alive {
			if !seen[h.Symbol.ID] {
				seen[h.Symbol.ID] = true
				out = append(out, h.Symbol)
			}
		}
		if len(out) > limit {
			return out[:limit], true, nil
		}
		return out, unseen, nil
	}
}

// ambiguousByName counts the relations of the symbol's language that name it
// but were left unresolved because several symbols share the name; 0 on a
// store error. For a JavaScript browser global it also counts the html
// inline handlers that call that name.
// Those naming its qualified name (its twins too) are already listed as
// usages; those naming another indexed symbol's qualified name are that
// symbol's.
func ambiguousByName(ctx context.Context, w *engine.Workspace, sym *parser.Symbol) int {
	hits, err := w.Scroll(ctx, store.Filter{TargetNames: []string{sym.Name}}, store.MaxScroll)
	if err != nil {
		return 0
	}
	known := map[string]bool{} // qualified name -> some indexed symbol has it
	n := 0
	for _, h := range hits {
		// An html inline handler (onclick="f()") looks f up among the JavaScript browser globals.
		handler := h.Symbol.Language == "html" && sym.Language == "javascript" && sym.Metadata["global"] == "true"
		if h.Symbol.Language != sym.Language && !handler { // otherwise the index looks a name up in its own language only
			continue
		}
		for _, r := range h.Symbol.Relations {
			qn := r.TargetQualifiedName
			if r.TargetName != sym.Name || r.Resolution != parser.ResolutionAmbiguous || qn == sym.QualifiedName {
				continue
			}
			if handler {
				if qn == "window."+sym.Name {
					n++
				}
				continue
			}
			if _, ok := known[qn]; !ok && qn != "" {
				some, err := w.Scroll(ctx, store.Filter{QualifiedName: qn}, 1)
				if err != nil {
					return 0
				}
				known[qn] = len(live(w, some)) > 0
			}
			if !known[qn] {
				n++
			}
		}
	}
	return n
}

// exactRel says whether r links to the symbol qualified qn by its qualified
// name: it names qn itself (build-tagged variants), or the index matched its
// qualified name. A relation bound by the name alone is a guess (by_name).
func exactRel(r parser.Relation, qn string) bool {
	return r.Resolution == parser.ResolutionExact || (r.TargetQualifiedName != "" && r.TargetQualifiedName == qn)
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
	WorkspaceRoot string `json:"workspace_root,omitempty" jsonschema:"Absolute path of the top directory of the project the user is working in (required), the same on every call. Find it as the folder opened in the editor, or the nearest parent directory that holds .git (in a monorepo that is the repository root, not a package inside it); without git, the nearest parent that holds a project file (go.mod, package.json, composer.json, pyproject.toml, project.godot); with neither, the smallest directory that holds the files being worked on. Never go up to a directory that holds other projects, the home or a system directory, and never pass a subdirectory of the project."`
	Symbol        string `json:"symbol" jsonschema:"Name or qualified name of the function or method, or a candidate id from an ambiguous answer."`
	Direction     string `json:"direction,omitempty" jsonschema:"incoming (callers), outgoing (callees) or both; default incoming."`
	Depth         int    `json:"depth,omitempty" jsonschema:"Levels to expand, default 2, max 4."`
	FilePath      string `json:"file_path,omitempty" jsonschema:"File that defines the symbol, to pick it when several share the name."`
}

// Node is one symbol in the hierarchy tree.
type Node struct {
	Candidate
	ByName   bool    `json:"by_name,omitempty"` // linked by name only: the receiver's type is unknown, check it
	Via      string  `json:"via,omitempty"`     // calls this interface's method (qualified name), which the parent implements
	Children []*Node `json:"children,omitempty"`
}

type walker struct {
	w       *engine.Workspace
	visited map[string]bool
	nodes   int
	cut     bool // a node's callers were cut at candidateLimit
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
	g := t.startGrep(ctx, w.Root, "", in.Symbol)
	defer g.stop()
	sym, res := resolveSymbol(ctx, w, in.Symbol, in.FilePath, g)
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
		wk.walk(ctx, root, *sym, dir == "incoming", in.Depth)
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
		if wk.cut {
			fmt.Fprintf(&text, "  … some nodes list only their first %d callers\n", candidateLimit)
			data[dir+"_truncated"] = true
		}
	}
	data["text"] = strings.TrimRight(text.String(), "\n")
	return reply("ok", "", data, w)
}

// walk fills the tree level by level, so the node cap drops the deepest
// levels first and never a direct caller.
func (wk *walker) walk(ctx context.Context, root *Node, sym parser.Symbol, incoming bool, depth int) {
	type item struct {
		n     *Node
		sym   parser.Symbol
		depth int
	}
	queue := []item{{root, sym, depth}}
	for len(queue) > 0 && wk.err == nil && wk.nodes < maxNodes {
		it := queue[0]
		queue = queue[1:]
		if it.depth == 0 {
			continue
		}
		next, exact, via := wk.next(ctx, it.sym, incoming)
		for _, s := range next {
			if wk.nodes >= maxNodes {
				break
			}
			child := &Node{Candidate: toCandidate(s), ByName: !exact[twinOf(&s)], Via: via[twinOf(&s)]}
			it.n.Children = append(it.n.Children, child)
			wk.nodes++
			if wk.visited[s.ID] {
				continue // already expanded elsewhere: listed, not re-walked
			}
			wk.visited[s.ID] = true
			queue = append(queue, item{child, s, it.depth - 1})
		}
	}
}

// next lists sym's neighbours, sorted, twins merged: the symbols referring
// to it (incoming) or the resolved targets of its own relations (outgoing),
// which of them are linked by a qualified name, and which only call an
// interface's method sym implements (via, as in rag_find_usages).
func (wk *walker) next(ctx context.Context, sym parser.Symbol, incoming bool) ([]parser.Symbol, map[twinKey]bool, map[twinKey]string) {
	var next []parser.Symbol
	exact := map[twinKey]bool{} // linked by at least one relation naming a qualified name
	via := map[twinKey]string{}
	if incoming {
		direct := map[twinKey]bool{}
		for i, m := range append([]parser.Symbol{sym}, viaInterfaces(ctx, wk.w, &sym)...) {
			callers, cut, err := referrers(ctx, wk.w, &m, candidateLimit-len(next)) // one limit for direct and via callers
			wk.cut = wk.cut || cut
			if err != nil {
				wk.err = err
				return nil, nil, nil
			}
			next = append(next, callers...)
			for _, c := range callers {
				if k := twinOf(&c); i == 0 {
					direct[k] = true
				} else if !direct[k] {
					via[k] = ownerQN(&m)
				}
				for _, r := range c.Relations {
					if (r.TargetID == m.ID || r.TargetQualifiedName == m.QualifiedName) && exactRel(r, m.QualifiedName) {
						exact[twinOf(&c)] = true
					}
				}
			}
		}
	} else {
		// An ambiguous call naming a qualified name (build-tagged variants
		// of one function) lists every variant, as incoming does.
		target := func(r parser.Relation) (store.Filter, string, int) {
			if r.TargetID == "" && r.Resolution == parser.ResolutionAmbiguous && r.TargetQualifiedName != "" {
				return store.Filter{QualifiedName: r.TargetQualifiedName}, "qn:" + r.TargetQualifiedName, candidateLimit
			}
			return store.Filter{IDs: []string{r.TargetID}}, r.TargetID, 1
		}
		exactKey := map[string]bool{}
		for _, r := range sym.Relations {
			if _, key, _ := target(r); r.Kind == parser.RelCall && (r.Resolution == parser.ResolutionExact || strings.HasPrefix(key, "qn:")) {
				exactKey[key] = true
			}
		}
		seen := map[string]bool{}
		for _, r := range sym.Relations {
			f, key, n := target(r)
			if r.Kind != parser.RelCall || key == "" || seen[key] {
				continue
			}
			seen[key] = true
			hits, err := wk.w.Scroll(ctx, f, n)
			if t := hits; err == nil && r.TargetID != "" && len(t) == 1 && t[0].Symbol.Kind != parser.KindChunk && t[0].Symbol.QualifiedName != "" {
				// The target's twins (a getter and its setter) carry calls of their own.
				k := twinOf(&t[0].Symbol)
				hits, err = wk.w.Scroll(ctx, store.Filter{QualifiedName: t[0].Symbol.QualifiedName, FilePath: t[0].Symbol.FilePath}, candidateLimit)
				hits = slices.DeleteFunc(hits, func(h store.Hit) bool { return twinOf(&h.Symbol) != k })
			}
			if err != nil {
				wk.err = err
				return nil, nil, nil
			}
			for _, h := range live(wk.w, hits) {
				// As the index resolves: code never calls across languages.
				if r.TargetID != "" || h.Symbol.Language == sym.Language || sym.Language == "docs" || sym.Language == "html" {
					next = append(next, h.Symbol)
					if exactKey[key] {
						exact[twinOf(&h.Symbol)] = true
					}
				}
			}
		}
	}
	next = mergeTwins(next, func(s *parser.Symbol) *parser.Symbol { return s })
	sort.Slice(next, func(i, j int) bool { return next[i].QualifiedName < next[j].QualifiedName })
	return next, exact, via
}

func writeTree(b *strings.Builder, n *Node, indent int) {
	mark := ""
	if n.ByName {
		mark = " [by name only]"
	}
	if n.Via != "" {
		mark += " [via " + n.Via + "]"
	}
	fmt.Fprintf(b, "%s%s (%s:%d)%s\n", strings.Repeat("  ", indent), n.QualifiedName, n.FilePath, n.StartLine, mark)
	for _, c := range n.Children {
		writeTree(b, c, indent+1)
	}
}

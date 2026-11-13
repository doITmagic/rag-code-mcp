package tools

import (
	"context"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"

	"github.com/doITmagic/rag-code-mcp/v2/engine"
	"github.com/doITmagic/rag-code-mcp/v2/index"
	"github.com/doITmagic/rag-code-mcp/v2/parser"
	"github.com/doITmagic/rag-code-mcp/v2/store"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const (
	exportsCap         = 300
	shortModuleScrolls = 50 // candidate lookups for a short package name
)

// ExportsInput is the input of rag_list_package_exports.
type ExportsInput struct {
	WorkspaceRoot string   `json:"workspace_root,omitempty" jsonschema:"Absolute path of the top directory of the project the user is working in (required), the same on every call. Find it as the folder opened in the editor, or the nearest parent directory that holds .git (in a monorepo that is the repository root, not a package inside it); without git, the nearest parent that holds a project file (go.mod, package.json, composer.json, pyproject.toml, project.godot); with neither, the smallest directory that holds the files being worked on. Never go up to a directory that holds other projects, the home or a system directory, and never pass a subdirectory of the project."`
	Package       string   `json:"package" jsonschema:"Full module path (github.com/x/y/pkg, App\\Models, app.models) or a short package name."`
	Kinds         []string `json:"kinds,omitempty" jsonschema:"Only these symbol kinds."`
}

// Export is one exported symbol.
type Export struct {
	Name          string `json:"name"`
	QualifiedName string `json:"qualified_name"`
	Signature     string `json:"signature,omitempty"`
	FilePath      string `json:"file_path"`
	StartLine     int    `json:"start_line"`
}

func (t *tools) listExports(ctx context.Context, req *mcp.CallToolRequest, in ExportsInput) (*mcp.CallToolResult, any, error) {
	pkg := strings.TrimSpace(in.Package)
	if pkg == "" {
		return fail("package is empty: pass a module path or a package name")
	}
	ks, err := kinds(in.Kinds)
	if err != nil {
		return fail(err.Error())
	}
	w, res := t.open(ctx, req, in.WorkspaceRoot)
	if res != nil {
		return res, nil, nil
	}
	hits, capped, file, err := moduleSymbols(ctx, w, pkg, ks)
	if err != nil {
		return storeErr(ctx, w, err)
	}
	hits = mergeTwins(live(w, hits), func(h *store.Hit) *parser.Symbol { return &h.Symbol })
	byKind := map[string][]Export{}
	modules := map[string]bool{}
	n := 0
	for _, h := range hits {
		s := h.Symbol
		if !s.Exported || s.Metadata["interface"] == "true" { // a Go interface's method: the interface row lists it
			continue
		}
		if n++; n > exportsCap {
			break
		}
		modules[s.Module] = true
		byKind[string(s.Kind)] = append(byKind[string(s.Kind)], Export{Name: s.Name, QualifiedName: s.QualifiedName,
			Signature: s.Signature, FilePath: s.FilePath, StartLine: s.StartLine})
	}
	for _, list := range byKind {
		sort.Slice(list, func(i, j int) bool { return list[i].QualifiedName < list[j].QualifiedName })
	}
	mods := make([]string, 0, len(modules))
	for m := range modules {
		mods = append(mods, m)
	}
	sort.Strings(mods)
	msg := fmt.Sprintf("%d exported symbols in %d modules", min(n, exportsCap), len(mods))
	if len(mods) == 0 && len(ks) > 0 {
		all, _, _, err := moduleSymbols(ctx, w, pkg, nil)
		if err != nil {
			return storeErr(ctx, w, err)
		}
		if slices.ContainsFunc(live(w, all), func(h store.Hit) bool { return h.Symbol.Exported }) {
			return reply("ok", fmt.Sprintf("%q has no exported symbols of kinds %v; drop kinds to list them all", pkg, ks), map[string]any{"package": pkg, "modules": mods, "exports": byKind, "truncated": capped}, w)
		}
	}
	// An empty state (no run yet) says nothing about what is indexed.
	st, stErr := index.LoadState(w.Root)
	indexed := func(match func(p string) bool) bool {
		return stErr != nil || len(st.Files) == 0 || slices.ContainsFunc(slices.Collect(maps.Keys(st.Files)), match)
	}
	const notIndexed = "ignored by .gitignore, excluded by exclude_patterns or skipped by the index rules"
	if len(mods) == 0 && file != "" {
		msg = fmt.Sprintf("%s has no exported symbols of its own (it may only re-export others)", file)
		if abs := filepath.Join(w.Root, filepath.FromSlash(file)); !indexed(func(p string) bool { return p == abs }) {
			msg = fmt.Sprintf("%s is not indexed (%s)", file, notIndexed)
		}
	} else if len(mods) == 0 && len(hits) > 0 { // indexed, only unexported symbols
		msg = fmt.Sprintf("%q has no exported symbols (only unexported ones)", pkg)
	} else if len(mods) == 0 {
		msg = fmt.Sprintf("no indexed module or directory matches %q; pass the full module path or a directory of the workspace", pkg)
		if abs, err := insideRoot(w.Root, strings.TrimRight(pkg, "/\\")); err == nil && abs != w.Root {
			if fi, err := os.Stat(abs); err == nil && fi.IsDir() {
				msg = fmt.Sprintf("directory %s has no exported symbols in the files directly in it (subdirectories are listed on their own)", pkg)
				ents, err := os.ReadDir(abs)
				hasSource := slices.ContainsFunc(ents, func(e os.DirEntry) bool { // only then can the rules have left files out
					return e.Type().IsRegular() && parser.ForFile(filepath.Join(abs, e.Name())) != nil
				})
				if err != nil {
					msg = fmt.Sprintf("directory %s is unreadable (%v), so its files are not indexed", pkg, err)
				} else if hasSource && !indexed(func(p string) bool { return filepath.Dir(p) == abs }) {
					msg = fmt.Sprintf("directory %s has no indexed files directly in it (%s; subdirectories are listed on their own)", pkg, notIndexed)
				}
			}
		}
	}
	return reply("ok", msg, map[string]any{"package": pkg, "modules": mods, "exports": byKind, "truncated": capped || n > exportsCap}, w)
}

// moduleSymbols finds the symbols of pkg: a module path, a short package
// name, a directory or a source file of the workspace (file is then set).
func moduleSymbols(ctx context.Context, w *engine.Workspace, pkg string, ks []parser.Kind) (hits []store.Hit, capped bool, file string, err error) {
	if hits, err = w.Scroll(ctx, store.Filter{Module: pkg, Kinds: ks}, store.MaxScroll); err != nil {
		return nil, false, "", err
	}
	// The scroll caps before the Exported filter, so a full page means some
	// exported symbols may be missing.
	capped = len(hits) >= store.MaxScroll
	if len(hits) == 0 && !strings.ContainsAny(pkg, "/\\.") {
		if hits, capped, err = shortModule(ctx, w, pkg, ks); err != nil {
			return nil, false, "", err
		}
	}
	if len(hits) == 0 { // a directory of the workspace: the files directly in it (a JS or GDScript module is one file)
		return dirModules(ctx, w, pkg, ks)
	}
	return hits, capped, "", nil
}

// dirModules returns the symbols of the files directly in dir, a directory
// inside the workspace, or of dir itself when it is a source file; none when
// it is neither. file is the file's path relative to the root.
func dirModules(ctx context.Context, w *engine.Workspace, dir string, ks []parser.Kind) (hits []store.Hit, capped bool, file string, err error) {
	abs, err := insideRoot(w.Root, strings.TrimRight(dir, "/\\"))
	if err != nil {
		return nil, false, "", nil
	}
	fi, err := os.Stat(abs)
	if err != nil { // the module spelling of a file (src/index for src/index.ts): the indexed file with that stem
		if st, lerr := index.LoadState(w.Root); lerr == nil {
			for p := range st.Files {
				if strings.TrimSuffix(p, filepath.Ext(p)) == abs {
					abs = p
					fi, err = os.Stat(abs)
					break
				}
			}
		}
	}
	if err == nil && fi.Mode().IsRegular() && parser.ForFile(abs) != nil {
		file, _ = filepath.Rel(w.Root, abs)
		hits, err = w.Scroll(ctx, store.Filter{FilePath: abs, Kinds: ks}, store.MaxScroll)
		return hits, len(hits) >= store.MaxScroll, filepath.ToSlash(file), err
	}
	if err != nil || !fi.IsDir() || abs == w.Root {
		return nil, false, "", nil
	}
	part, err := w.Scroll(ctx, store.Filter{PathPrefix: abs, Kinds: ks}, store.MaxScroll)
	if err != nil {
		return nil, false, "", err
	}
	for _, h := range part {
		if filepath.Dir(h.Symbol.FilePath) == abs {
			hits = append(hits, h)
		}
	}
	return hits, len(part) >= store.MaxScroll, "", nil
}

// shortModule finds the symbols of the modules ending in "/"+name, "."+name
// or "\"+name without scanning the collection: symbols named name (the
// QualifiedSuffix lookup) plus the indexed directories and files called name,
// each read with one filtered scroll. capped reports a scroll that hit
// store.MaxScroll.
func shortModule(ctx context.Context, w *engine.Workspace, name string, ks []parser.Kind) (hits []store.Hit, capped bool, err error) {
	filters := []store.Filter{{QualifiedSuffix: name, Kinds: ks}} // a PathPrefix filter is a directory called name
	dirs := map[string]bool{}
	var files []string
	if st, err := index.LoadState(w.Root); err == nil {
		for p := range st.Files {
			switch d := filepath.Dir(p); {
			case d != w.Root && strings.EqualFold(filepath.Base(d), name): // the root itself is no package
				dirs[d] = true
			case strings.EqualFold(strings.TrimSuffix(filepath.Base(p), filepath.Ext(p)), name):
				files = append(files, p)
			}
		}
	}
	for d := range dirs {
		filters = append(filters, store.Filter{PathPrefix: d, Kinds: ks})
	}
	for _, p := range files {
		if !dirs[filepath.Dir(p)] {
			filters = append(filters, store.Filter{FilePath: p, Kinds: ks})
		}
	}
	// shortcut: one scroll per candidate, capped; a module_suffix payload
	// index would make this one call.
	if len(filters) > shortModuleScrolls {
		filters = filters[:shortModuleScrolls]
	}
	seen, scrolled := map[string]bool{}, map[string]bool{}
	for i := 0; i < len(filters); i++ {
		f := filters[i]
		part, err := w.Scroll(ctx, f, store.MaxScroll)
		if err != nil {
			return nil, false, err
		}
		capped = capped || len(part) >= store.MaxScroll
		for _, h := range part {
			m := h.Symbol.Module
			inDir := f.PathPrefix != "" && filepath.Dir(h.Symbol.FilePath) == f.PathPrefix // a JS module is a file of the directory
			if seen[h.Symbol.ID] || !(inDir || m == name || strings.HasSuffix(m, "/"+name) || strings.HasSuffix(m, "."+name) || strings.HasSuffix(m, "\\"+name)) {
				continue
			}
			seen[h.Symbol.ID] = true
			hits = append(hits, h)
			// A module spread over files the lookups did not name (Go's root
			// package seen through cobra.go): read the whole module once.
			if m != "" && !inDir && !scrolled[m] && len(filters) < 2*shortModuleScrolls {
				scrolled[m] = true
				filters = append(filters, store.Filter{Module: m, Kinds: ks})
			}
		}
	}
	return hits, capped, nil
}

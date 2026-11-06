package tools

import (
	"context"
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/doITmagic/rag-code-mcp/v3/engine"
	"github.com/doITmagic/rag-code-mcp/v3/index"
	"github.com/doITmagic/rag-code-mcp/v3/parser"
	"github.com/doITmagic/rag-code-mcp/v3/store"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const (
	exportsCap         = 300
	shortModuleScrolls = 50 // candidate lookups for a short package name
)

// ExportsInput is the input of rag_list_package_exports.
type ExportsInput struct {
	WorkspaceRoot string   `json:"workspace_root,omitempty" jsonschema:"Absolute path of the project directory (required). Pass it on every call."`
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
	w, res := t.open(ctx, req, in.WorkspaceRoot)
	if res != nil {
		return res, nil, nil
	}
	f := store.Filter{Module: pkg, Kinds: kinds(in.Kinds)}
	hits, err := w.Scroll(ctx, f, store.MaxScroll)
	if err != nil {
		return storeErr(ctx, w, err)
	}
	// The scroll caps before the Exported filter, so a full page means some
	// exported symbols may be missing.
	capped := len(hits) >= store.MaxScroll
	if len(hits) == 0 && !strings.ContainsAny(pkg, "/\\.") {
		if hits, capped, err = shortModule(ctx, w, pkg, f.Kinds); err != nil {
			return storeErr(ctx, w, err)
		}
	}
	hits = live(w, hits)
	byKind := map[string][]Export{}
	modules := map[string]bool{}
	n := 0
	for _, h := range hits {
		s := h.Symbol
		if !s.Exported {
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
	if len(mods) == 0 {
		msg = fmt.Sprintf("no indexed module matches %q; pass the full module path", pkg)
	}
	return reply("ok", msg, map[string]any{"package": pkg, "modules": mods, "exports": byKind, "truncated": capped || n > exportsCap}, w)
}

// shortModule finds the symbols of the modules ending in "/"+name, "."+name
// or "\"+name without scanning the collection: symbols named name (the
// QualifiedSuffix lookup) plus the indexed directories and files called name,
// each read with one filtered scroll. capped reports a scroll that hit
// store.MaxScroll.
func shortModule(ctx context.Context, w *engine.Workspace, name string, ks []parser.Kind) (hits []store.Hit, capped bool, err error) {
	filters := []store.Filter{{QualifiedSuffix: name, Kinds: ks}}
	dirs := map[string]bool{}
	var files []string
	if st, err := index.LoadState(w.Root); err == nil {
		for p := range st.Files {
			switch d := filepath.Dir(p); {
			case strings.EqualFold(filepath.Base(d), name):
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
	seen := map[string]bool{}
	for _, f := range filters {
		part, err := w.Scroll(ctx, f, store.MaxScroll)
		if err != nil {
			return nil, false, err
		}
		capped = capped || len(part) >= store.MaxScroll
		for _, h := range part {
			m := h.Symbol.Module
			if seen[h.Symbol.ID] || !(m == name || strings.HasSuffix(m, "/"+name) || strings.HasSuffix(m, "."+name) || strings.HasSuffix(m, "\\"+name)) {
				continue
			}
			seen[h.Symbol.ID] = true
			hits = append(hits, h)
		}
	}
	return hits, capped, nil
}

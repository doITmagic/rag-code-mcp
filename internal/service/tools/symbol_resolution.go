package tools

import (
	"context"
	"path/filepath"
	"strings"

	"github.com/doITmagic/rag-code-mcp/internal/service/engine"
	"github.com/doITmagic/rag-code-mcp/pkg/storage"
)

// A short name is not an identity. Prefer an explicit identity, then require
// a unique candidate in the supplied file or workspace.
func resolveSymbol(ctx context.Context, eng *engine.Engine, wsID, name, file string) (*storage.SearchResult, string) {
	results, err := searchSymbolCandidates(ctx, eng, wsID, name, 100)
	if err != nil {
		return nil, "unresolved"
	}
	return uniqueSymbol(results, file)
}

func searchSymbolCandidates(ctx context.Context, eng *engine.Engine, wsID, name string, limit int) ([]storage.SearchResult, error) {
	results, err := eng.SearchByName(ctx, wsID, name, limit)
	if err != nil || len(results) > 0 {
		return results, err
	}
	results, err = eng.ExactSearchPolyglot(ctx, wsID, map[string]interface{}{"qualified_name": name}, limit)
	if err != nil || len(results) > 0 {
		return results, err
	}
	filters := goMethodFilters(name)
	if filters == nil {
		return nil, nil
	}
	results, err = eng.ExactSearchPolyglot(ctx, wsID, filters, limit)
	if err != nil || len(results) > 0 || filters["package"] != nil {
		return results, err
	}
	// A two-part name may also be a Go package.Function or package.Type.
	results, err = eng.ExactSearchPolyglot(ctx, wsID, map[string]interface{}{
		"language": "go", "package": filters["metadata.receiver"], "name": filters["name"],
	}, limit)
	var symbols []storage.SearchResult
	for _, r := range results {
		if r.Point.Payload["type"] != "method" {
			symbols = append(symbols, r)
		}
	}
	return symbols, err
}

// goMethodFilters maps [pkg.]Receiver.Method, including (*T).Method, to the
// metadata Go stores method ownership in, since it has no qualified_name.
func goMethodFilters(name string) map[string]interface{} {
	parts := strings.Split(name, ".")
	if len(parts) != 2 && len(parts) != 3 {
		return nil
	}
	parts[len(parts)-2] = strings.Trim(parts[len(parts)-2], "(*)")
	for _, part := range parts {
		if part == "" {
			return nil
		}
	}
	filters := map[string]interface{}{
		"language": "go", "type": "method", "name": parts[len(parts)-1],
		"metadata.receiver": parts[len(parts)-2],
	}
	if len(parts) == 3 {
		filters["package"] = parts[0]
	}
	return filters
}

// qualifierRulesOut reports whether an unresolved name's qualifier was
// checked: Go methods with its short name exist, none on that receiver.
// Other qualifiers (packages, other languages) are not modelled here.
func qualifierRulesOut(ctx context.Context, eng *engine.Engine, wsID, name string) bool {
	filters := goMethodFilters(name)
	if filters == nil {
		return false
	}
	delete(filters, "metadata.receiver")
	delete(filters, "package")
	results, err := eng.ExactSearchPolyglot(ctx, wsID, filters, 1)
	return err == nil && len(results) > 0
}

func uniqueSymbol(results []storage.SearchResult, file string) (*storage.SearchResult, string) {
	if len(results) == 0 {
		return nil, "unresolved"
	}
	if file != "" {
		var local []storage.SearchResult
		for _, r := range results {
			path, _ := r.Point.Payload["file_path"].(string)
			if filepath.Clean(path) == filepath.Clean(file) {
				local = append(local, r)
			}
		}
		if len(local) > 0 {
			results = local
		}
	}
	if len(results) != 1 {
		return nil, "ambiguous"
	}
	return &results[0], "resolved"
}

func resolveCall(ctx context.Context, eng *engine.Engine, wsID string, source storage.SearchResult, rel map[string]interface{}) (*storage.SearchResult, string) {
	if id, _ := rel["target_id"].(string); id != "" {
		results, err := eng.ExactSearchPolyglot(ctx, wsID, map[string]interface{}{"symbol_id": id}, 2)
		if err != nil {
			return nil, "unresolved"
		}
		return uniqueSymbol(results, "")
	}
	file, _ := source.Point.Payload["file_path"].(string)
	if qualified, _ := rel["target_qualified_name"].(string); qualified != "" {
		results, err := eng.ExactSearchPolyglot(ctx, wsID, map[string]interface{}{"qualified_name": qualified}, 100)
		if err != nil {
			return nil, "unresolved"
		}
		return uniqueSymbol(results, file)
	}
	if receiver, _ := rel["receiver"].(string); receiver != "" {
		return nil, "unresolved"
	}
	name, _ := rel["target_name"].(string)
	language, _ := source.Point.Payload["language"].(string)
	result, status := resolveSymbol(ctx, eng, wsID, name, file)
	// A Go call keeps only the method name, so the caller's file says nothing
	// about the receiver's type: only a workspace-unique method is an identity.
	if language == "go" && result != nil && result.Point.Payload["type"] == "method" {
		result, status = resolveSymbol(ctx, eng, wsID, name, "")
	}
	// New indexes distinguish free calls from member calls. Legacy indexes
	// without resolution metadata can still expose unique name-only edges.
	if _, indexed := rel["resolution"]; indexed && result != nil && result.Point.Payload["type"] == "method" && (language == "php" || language == "javascript" || language == "typescript") {
		return nil, "unresolved"
	}
	return result, status
}

// callMayTarget reports whether a call from source can reach target. A call
// that resolves elsewhere cannot; an ambiguous call by the target's name stays
// a candidate, except from the target itself: a name collision, not recursion.
func callMayTarget(source storage.SearchResult, rel map[string]interface{}, resolved *storage.SearchResult, status string, target storage.SearchResult) bool {
	if resolved != nil {
		return symbolKey(*resolved) == symbolKey(target)
	}
	return status == "ambiguous" && rel["target_name"] == target.Point.Payload["name"] && symbolKey(source) != symbolKey(target)
}

func symbolKey(r storage.SearchResult) string {
	if id, _ := r.Point.Payload["symbol_id"].(string); id != "" {
		return id
	}
	return r.Point.ID
}

func shortSymbolName(name string) string {
	// Scene symbols and dependency targets include their extension; the
	// dot separates the filename, rather than a class and member name.
	if strings.EqualFold(filepath.Ext(name), ".tscn") {
		return name
	}
	if i := strings.LastIndex(name, "::"); i >= 0 {
		return name[i+2:]
	}
	// PHP namespaces: App\Models\User is referenced as User.
	if i := strings.LastIndex(name, "\\"); i >= 0 {
		return name[i+1:]
	}
	if i := strings.LastIndex(name, "."); i >= 0 {
		return name[i+1:]
	}
	return name
}

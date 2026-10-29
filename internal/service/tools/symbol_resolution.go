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
func resolveSymbol(ctx context.Context, eng *engine.Engine, wsID, root, name, file string) (*storage.SearchResult, string) {
	results, err := searchSymbolCandidates(ctx, eng, wsID, root, name, 100)
	if err != nil {
		return nil, "unresolved"
	}
	return uniqueSymbol(results, file)
}

func searchSymbolCandidates(ctx context.Context, eng *engine.Engine, wsID, root, name string, limit int) ([]storage.SearchResult, error) {
	results, err := eng.SearchByName(ctx, wsID, name, limit)
	if err != nil || len(results) > 0 {
		return results, err
	}
	results, err = eng.ExactSearchPolyglot(ctx, wsID, map[string]interface{}{"qualified_name": name}, limit)
	if err != nil || len(results) > 0 {
		return results, err
	}
	var symbols []storage.SearchResult
	for _, reading := range splitGoImportPath(name) {
		found, err := searchGoQualified(ctx, eng, wsID, reading.name, limit)
		if err != nil {
			return nil, err
		}
		if reading.importPath != "" {
			found = filterByImportPath(found, reading.importPath, root)
		}
		symbols = append(symbols, found...)
	}
	return symbols, nil
}

func searchGoQualified(ctx context.Context, eng *engine.Engine, wsID, name string, limit int) ([]storage.SearchResult, error) {
	filters := goMethodFilters(name)
	if filters == nil {
		return nil, nil
	}
	symbols, err := eng.ExactSearchPolyglot(ctx, wsID, filters, limit)
	if err != nil || filters["package"] != nil {
		return symbols, err
	}
	// A two-part name may also be a Go package.Function or package.Type,
	// also when a type shares the package's name: keep both readings.
	results, err := eng.ExactSearchPolyglot(ctx, wsID, map[string]interface{}{
		"language": "go", "package": filters["metadata.receiver"], "name": filters["name"],
	}, limit)
	for _, r := range results {
		if r.Point.Payload["type"] != "method" {
			symbols = append(symbols, r)
		}
	}
	return symbols, err
}

type goImportReading struct{ importPath, name string }

// splitGoImportPath splits github.com/spf13/pflag.FlagSet.Parse into the
// package's import path and the name within it, pflag.FlagSet.Parse: the
// index stores only the package's name. A dot may also belong to the path,
// as in gopkg.in/yaml.v3.Marshal, so the name within the package is read as
// both Type.Method and Function, its package named by the segment's first
// dotted part.
func splitGoImportPath(name string) []goImportReading {
	i := strings.LastIndex(name, "/")
	if i < 0 {
		return []goImportReading{{name: name}}
	}
	parts := strings.Split(name[i+1:], ".")
	var readings []goImportReading
	for k := 1; k <= 2 && k < len(parts); k++ {
		readings = append(readings, goImportReading{
			importPath: name[:i+1] + strings.Join(parts[:len(parts)-k], "."),
			name:       parts[0] + "." + strings.Join(parts[len(parts)-k:], "."),
		})
	}
	return readings
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
// checked: it names an indexed Go type, which has no such method. Interfaces
// (methods not indexed by receiver), types with embedded fields (promoted
// methods), external packages and other languages are not modelled here.
func qualifierRulesOut(ctx context.Context, eng *engine.Engine, wsID, root, name string) bool {
	for _, reading := range splitGoImportPath(name) {
		filters := goMethodFilters(reading.name)
		if filters == nil {
			continue
		}
		typeFilters := map[string]interface{}{"language": "go", "type": "type", "name": filters["metadata.receiver"]}
		if pkg, ok := filters["package"]; ok {
			typeFilters["package"] = pkg
		}
		results, err := eng.ExactSearchPolyglot(ctx, wsID, typeFilters, 100)
		if err != nil {
			return false
		}
		if reading.importPath != "" {
			results = filterByImportPath(results, reading.importPath, root)
		}
		for _, r := range results {
			if metadata, _ := r.Point.Payload["metadata"].(map[string]interface{}); metadata["kind"] != "interface" && !hasEmbeddedField(metadata) {
				return true
			}
		}
	}
	return false
}

// hasEmbeddedField reports whether a Go struct has an embedded field, which
// the analyzer stores as a field without a name.
func hasEmbeddedField(metadata map[string]interface{}) bool {
	fields, _ := metadata["fields"].([]interface{})
	for _, raw := range fields {
		if field, _ := raw.(map[string]interface{}); field["name"] == "" {
			return true
		}
	}
	return false
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
	name, _ := rel["target_name"].(string)
	language, _ := source.Point.Payload["language"].(string)
	receiver, _ := rel["receiver"].(string)
	if qualified, _ := rel["target_qualified_name"].(string); qualified != "" {
		results, err := eng.ExactSearchPolyglot(ctx, wsID, map[string]interface{}{"qualified_name": qualified}, 100)
		// Python self.helper() may call a method the class inherits.
		if err == nil && len(results) == 0 && language == "python" && (receiver == "self" || receiver == "cls") {
			results, err = eng.ExactSearchPolyglot(ctx, wsID, map[string]interface{}{"language": "python", "type": "method", "name": name}, 100)
		}
		if err != nil {
			return nil, "unresolved"
		}
		return uniqueSymbol(results, file)
	}
	if receiver != "" {
		// Python utils.helper() calls a function of a project module named
		// utils, so json.dumps() does not reach a project's own dumps.
		if language == "python" {
			results, err := eng.ExactSearchPolyglot(ctx, wsID, map[string]interface{}{"language": "python", "type": "function", "name": name}, 100)
			if err != nil {
				return nil, "unresolved"
			}
			var module []storage.SearchResult
			for _, r := range results {
				if pkg, _ := r.Point.Payload["package"].(string); pkg == receiver || strings.HasSuffix(pkg, "."+receiver) {
					module = append(module, r)
				}
			}
			return uniqueSymbol(module, "")
		}
		if language != "go" {
			return nil, "unresolved"
		}
		// Go records the receiver's type for calls on the method's own
		// receiver; that type lives in the caller's package. A promoted
		// method is not declared on it and falls back to the name.
		results, err := eng.ExactSearchPolyglot(ctx, wsID, map[string]interface{}{
			"language": "go", "type": "method", "name": name,
			"metadata.receiver": receiver, "package": source.Point.Payload["package"],
		}, 2)
		if err != nil {
			return nil, "unresolved"
		}
		if len(results) > 0 {
			return uniqueSymbol(results, "")
		}
	}
	result, status := resolveSymbol(ctx, eng, wsID, "", name, file)
	// A Go call keeps only the method name, so the caller's file says nothing
	// about the receiver's type: only a workspace-unique method is an identity.
	if language == "go" && result != nil && result.Point.Payload["type"] == "method" {
		result, status = resolveSymbol(ctx, eng, wsID, "", name, "")
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

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
	results, err := eng.SearchByName(ctx, wsID, name, 100)
	if err != nil {
		return nil, "unresolved"
	}
	if len(results) == 0 {
		results, err = eng.ExactSearchPolyglot(ctx, wsID, map[string]interface{}{"qualified_name": name}, 100)
		if err != nil {
			return nil, "unresolved"
		}
	}
	return uniqueSymbol(results, file)
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
	result, status := resolveSymbol(ctx, eng, wsID, name, file)
	// New indexes distinguish free calls from member calls. Legacy indexes
	// without resolution metadata can still expose unique name-only edges.
	language, _ := source.Point.Payload["language"].(string)
	if _, indexed := rel["resolution"]; indexed && result != nil && result.Point.Payload["type"] == "method" && (language == "php" || language == "javascript" || language == "typescript") {
		return nil, "unresolved"
	}
	return result, status
}

func symbolKey(r storage.SearchResult) string {
	if id, _ := r.Point.Payload["symbol_id"].(string); id != "" {
		return id
	}
	return r.Point.ID
}

func shortSymbolName(name string) string {
	if i := strings.LastIndex(name, "::"); i >= 0 {
		return name[i+2:]
	}
	if i := strings.LastIndex(name, "."); i >= 0 {
		return name[i+1:]
	}
	return name
}

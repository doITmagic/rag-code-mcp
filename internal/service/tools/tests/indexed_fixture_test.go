package tests

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/doITmagic/rag-code-mcp/pkg/indexer"
	"github.com/doITmagic/rag-code-mcp/pkg/parser"
	"github.com/doITmagic/rag-code-mcp/pkg/storage"
)

// indexFixture parses and indexes files through the production analyzer and
// indexer; only embedding and storage are fakes. It returns the fixture dir.
func indexFixture(t *testing.T, ext string, analyzer parser.Analyzer, files map[string]string) (string, *mockVectorStore) {
	t.Helper()
	dir := t.TempDir()
	for name, code := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(code), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	parsed, err := analyzer.Analyze(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	symbols := parsed.Symbols
	var points []storage.Point
	store := &mockVectorStore{UpsertFunc: func(_ context.Context, _ string, batch []storage.Point) (*storage.UpdateResult, error) {
		points = append(points, batch...)
		return nil, nil
	}}
	if err := indexer.NewService(&mockLLMProvider{}, store).IndexItems(context.Background(), "fixture-"+ext, symbols); err != nil {
		t.Fatal(err)
	}
	store.ExactSearchFunc = func(_ context.Context, collection string, filters map[string]interface{}, limit int) ([]storage.SearchResult, error) {
		var found []storage.SearchResult
		for _, point := range points {
			language, _ := point.Payload["language"].(string)
			if strings.HasSuffix(collection, "-"+language) && payloadMatches(point.Payload, filters) {
				found = append(found, storage.SearchResult{Point: point, Score: 1})
			}
		}
		if limit > 0 && len(found) > limit {
			found = found[:limit]
		}
		return found, nil
	}
	return dir, store
}

// payloadMatches applies exact filters the way Qdrant does: dotted keys walk
// nested objects and "relations[]." keys match any relation.
func payloadMatches(payload map[string]interface{}, filters map[string]interface{}) bool {
	for key, expected := range filters {
		if field, ok := strings.CutPrefix(key, "relations[]."); ok {
			matched := false
			for _, raw := range relationList(payload) {
				if rel, _ := raw.(map[string]interface{}); rel[field] == expected {
					matched = true
				}
			}
			if !matched {
				return false
			}
			continue
		}
		var actual interface{} = payload
		for _, part := range strings.Split(key, ".") {
			object, _ := actual.(map[string]interface{})
			actual = object[part]
		}
		if actual != expected {
			return false
		}
	}
	return true
}

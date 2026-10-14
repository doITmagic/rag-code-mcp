package tests

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/doITmagic/rag-code-mcp/internal/service/tools"
	"github.com/doITmagic/rag-code-mcp/pkg/storage"
)

func TestCallHierarchyUsesIdentityForSameNamedMethods(t *testing.T) {
	points := []storage.SearchResult{
		{Point: storage.Point{ID: "a", Payload: map[string]interface{}{"symbol_id": "a", "name": "validate", "qualified_name": "App\\A::validate", "type": "method", "file_path": "owners.php"}}},
		{Point: storage.Point{ID: "b", Payload: map[string]interface{}{"symbol_id": "b", "name": "validate", "qualified_name": "App\\B::validate", "type": "method", "file_path": "owners.php"}}},
		{Point: storage.Point{ID: "run", Payload: map[string]interface{}{"symbol_id": "run", "name": "run", "qualified_name": "App\\A::run", "type": "method", "file_path": "owners.php", "relations": []interface{}{map[string]interface{}{"target_name": "validate", "type": "calls", "target_id": "a", "resolution": "resolved"}}}}},
	}
	store := &mockVectorStore{ExactSearchFunc: func(_ context.Context, col string, filters map[string]interface{}, _ int) ([]storage.SearchResult, error) {
		if !strings.HasSuffix(col, "-go") {
			return nil, nil
		}
		if _, ok := filters["relations[].target_name"]; ok {
			return points[2:], nil
		}
		return points, nil
	}}
	eng := setupTestEngine(store)
	tool := tools.NewCallHierarchyTool(eng)
	for _, tc := range []struct {
		name, direction, status string
		children                int
	}{
		{"validate", "incoming", "ambiguous", 0},
		{"App\\A::validate", "incoming", "success", 1},
		{"App\\B::validate", "incoming", "success", 0},
		{"App\\A::run", "outgoing", "success", 1},
	} {
		text, err := tool.Execute(context.Background(), map[string]interface{}{"symbol_name": tc.name, "direction": tc.direction, "file_path": "owners.php", "depth": 1})
		if err != nil {
			t.Fatal(err)
		}
		var response struct {
			Status string
			Data   struct {
				Children []struct {
					SymbolID string `json:"symbol_id"`
				}
			}
		}
		if err := json.Unmarshal([]byte(text), &response); err != nil {
			t.Fatal(err)
		}
		if response.Status != tc.status || len(response.Data.Children) != tc.children {
			t.Fatalf("%s %s: %s", tc.name, tc.direction, text)
		}
		if tc.direction == "outgoing" && response.Data.Children[0].SymbolID != "a" {
			t.Fatal(text)
		}
	}
	usage := tools.NewFindUsagesTool(eng)
	text, err := usage.Execute(context.Background(), map[string]interface{}{"symbol_name": "App\\B::validate", "file_path": "owners.php"})
	if err != nil || strings.Contains(text, "Found symbol usages") {
		t.Fatalf("false usages: %s %v", text, err)
	}
}

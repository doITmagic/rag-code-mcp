package tests

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/doITmagic/rag-code-mcp/internal/service/tools"
	golang "github.com/doITmagic/rag-code-mcp/pkg/parser/go"
	"github.com/doITmagic/rag-code-mcp/pkg/storage"
)

// The fixture store caps a scan at its limit like Qdrant does, so a caller
// past a fixed candidate limit would be dropped silently.
func TestUsagesAndCallersAreNotCappedByCandidateLimit(t *testing.T) {
	const callers = 120
	var code strings.Builder
	code.WriteString("package app\n\nfunc Target() {}\n")
	for i := 0; i < callers; i++ {
		fmt.Fprintf(&code, "\nfunc Caller%d() { Target() }\n", i)
	}
	dir, store := indexFixture(t, "go", golang.NewCodeAnalyzer(), map[string]string{"app.go": code.String()})
	eng := setupTestEngine(store)
	t.Cleanup(eng.Close)
	path := filepath.Join(dir, "app.go")

	text, err := tools.NewFindUsagesTool(eng).Execute(context.Background(), map[string]interface{}{"symbol_name": "Target", "file_path": path})
	if err != nil {
		t.Fatal(err)
	}
	var usages struct{ Data []tools.UsageResult }
	if err := json.Unmarshal([]byte(text), &usages); err != nil {
		t.Fatal(err)
	}
	if len(usages.Data) != callers {
		t.Errorf("find_usages want %d usages, got %d", callers, len(usages.Data))
	}

	text, err = tools.NewCallHierarchyTool(eng).Execute(context.Background(), map[string]interface{}{
		"symbol_name": "Target", "direction": "incoming", "file_path": path, "depth": 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	var hierarchy struct{ Data tools.CallNode }
	if err := json.Unmarshal([]byte(text), &hierarchy); err != nil {
		t.Fatal(err)
	}
	if len(hierarchy.Data.Children) != callers {
		t.Errorf("call_hierarchy want %d callers, got %d", callers, len(hierarchy.Data.Children))
	}
}

// Unexported points count against a scan limit too, so a package with more
// points than the limit would lose exports that are in the index.
func TestPackageExportsAreNotCappedByScanLimit(t *testing.T) {
	const exported, unexported = 200, 900
	var code strings.Builder
	code.WriteString("package big\n")
	for i := 0; i < exported; i++ {
		fmt.Fprintf(&code, "\nfunc Public%d() {}\n", i)
	}
	for i := 0; i < unexported; i++ {
		fmt.Fprintf(&code, "\nfunc private%d() {}\n", i)
	}
	dir, store := indexFixture(t, "go", golang.NewCodeAnalyzer(), map[string]string{"big.go": code.String()})
	// Qdrant scrolls in point-ID order; put the unexported points first.
	scan := store.ExactSearchFunc
	store.ExactSearchFunc = func(ctx context.Context, collection string, filters map[string]interface{}, limit int) ([]storage.SearchResult, error) {
		found, err := scan(ctx, collection, filters, 0)
		sort.SliceStable(found, func(i, j int) bool {
			return found[i].Point.Payload["is_public"] == false && found[j].Point.Payload["is_public"] == true
		})
		if limit > 0 && len(found) > limit {
			found = found[:limit]
		}
		return found, err
	}
	eng := setupTestEngine(store)
	t.Cleanup(eng.Close)
	text, err := tools.NewListPackageExportsTool(eng).Execute(context.Background(), map[string]interface{}{"package": "big", "file_path": filepath.Join(dir, "big.go")})
	if err != nil {
		t.Fatal(err)
	}
	var response struct {
		Data map[string][]tools.ExportedSymbol
	}
	if err := json.Unmarshal([]byte(text), &response); err != nil {
		t.Fatal(err)
	}
	if got := len(response.Data["function"]); got != exported {
		t.Errorf("want %d exported functions, got %d", exported, got)
	}
}

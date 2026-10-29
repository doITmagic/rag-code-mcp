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
// past a fixed candidate limit would be dropped silently. Every candidate is
// scanned; the listing is capped, with the real total reported.
func TestUsagesAndCallersAreNotCappedByCandidateLimit(t *testing.T) {
	const callers = 300
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
	var usages struct {
		Message, Warning string
		Data             []tools.UsageResult
	}
	if err := json.Unmarshal([]byte(text), &usages); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(usages.Message, fmt.Sprintf("**Total usages found:** %d", callers)) || len(usages.Data) != 100 || usages.Warning == "" {
		t.Errorf("find_usages want %d usages reported, 100 listed and a warning; got %d listed, warning %q", callers, len(usages.Data), usages.Warning)
	}

	text, err = tools.NewCallHierarchyTool(eng).Execute(context.Background(), map[string]interface{}{
		"symbol_name": "Target", "direction": "incoming", "file_path": path, "depth": 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	var hierarchy struct {
		Warning string
		Data    tools.CallNode
	}
	if err := json.Unmarshal([]byte(text), &hierarchy); err != nil {
		t.Fatal(err)
	}
	if got := len(hierarchy.Data.Children); got != 200 || got+hierarchy.Data.OmittedCallers != callers || hierarchy.Warning == "" {
		t.Errorf("call_hierarchy want 200 of %d callers listed and a warning; got %d listed, %d omitted, warning %q", callers, got, hierarchy.Data.OmittedCallers, hierarchy.Warning)
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

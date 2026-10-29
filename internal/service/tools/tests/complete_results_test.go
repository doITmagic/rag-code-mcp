package tests

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/doITmagic/rag-code-mcp/internal/service/tools"
	golang "github.com/doITmagic/rag-code-mcp/pkg/parser/go"
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

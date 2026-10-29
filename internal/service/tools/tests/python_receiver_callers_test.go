package tests

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/doITmagic/rag-code-mcp/internal/service/tools"
	"github.com/doITmagic/rag-code-mcp/pkg/parser/python"
)

func pythonCallers(t *testing.T, tool *tools.CallHierarchyTool, path, name string) []string {
	t.Helper()
	text, err := tool.Execute(context.Background(), map[string]interface{}{
		"symbol_name": name, "direction": "incoming", "file_path": path, "depth": 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	var hierarchy struct{ Data tools.CallNode }
	if err := json.Unmarshal([]byte(text), &hierarchy); err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, child := range hierarchy.Data.Children {
		names = append(names, child.Name)
	}
	return names
}

// A method inherited from a base class in another file is still the target
// of self.helper().
func TestPythonSelfCallReachesInheritedMethod(t *testing.T) {
	dir, store := indexFixture(t, "py", python.NewAnalyzer(), map[string]string{
		"base.py":  "class Base:\n    def helper(self):\n        return 1\n",
		"child.py": "from base import Base\n\n\nclass Child(Base):\n    def run(self):\n        return self.helper()\n",
	})
	eng := setupTestEngine(store)
	t.Cleanup(eng.Close)
	got := pythonCallers(t, tools.NewCallHierarchyTool(eng), filepath.Join(dir, "base.py"), "Base.helper")
	if len(got) != 1 || got[0] != "run" {
		t.Fatalf("callers of Base.helper = %v, want [run]", got)
	}
}

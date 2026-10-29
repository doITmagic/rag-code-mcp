package tests

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/doITmagic/rag-code-mcp/internal/service/tools"
	"github.com/doITmagic/rag-code-mcp/pkg/parser/python"
)

// @overload stubs and a property setter reuse the method's name; the
// qualified name must still address the one implementation or getter.
func TestPythonOverloadsKeepQualifiedNameUnique(t *testing.T) {
	dir, store := indexFixture(t, "py", python.NewAnalyzer(), map[string]string{
		"timed.py": `import typing as t


class TimestampSigner:
    @t.overload
    def unsign(self, value: str, return_timestamp: t.Literal[False] = False) -> bytes: ...

    @t.overload
    def unsign(self, value: str, return_timestamp: t.Literal[True] = True) -> tuple: ...

    def unsign(self, value, return_timestamp=False):
        return value

    def validate(self, value):
        return self.unsign(value)

    @property
    def secret_key(self):
        return self.keys[-1]

    @secret_key.setter
    def secret_key(self, value):
        self.keys = [value]
`,
	})
	eng := setupTestEngine(store)
	t.Cleanup(eng.Close)
	path := filepath.Join(dir, "timed.py")
	for _, name := range []string{"TimestampSigner.unsign", "TimestampSigner.secret_key"} {
		text, err := tools.NewCallHierarchyTool(eng).Execute(context.Background(), map[string]interface{}{
			"symbol_name": name, "direction": "incoming", "file_path": path, "depth": 1,
		})
		if err != nil {
			t.Fatal(err)
		}
		var hierarchy struct {
			Status string
			Data   tools.CallNode
		}
		if err := json.Unmarshal([]byte(text), &hierarchy); err != nil {
			t.Fatal(err)
		}
		if hierarchy.Status != "success" || hierarchy.Data.Resolution != "resolved" {
			t.Errorf("%s want one resolved symbol: %s", name, text)
		}
		if name == "TimestampSigner.unsign" && (len(hierarchy.Data.Children) != 1 || hierarchy.Data.Children[0].Name != "validate") {
			t.Errorf("%s callers want only validate: %s", name, text)
		}
	}
}

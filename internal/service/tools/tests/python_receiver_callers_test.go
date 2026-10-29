package tests

import (
	"context"
	"encoding/json"
	"path/filepath"
	"slices"
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

// utils.helper2() calls the function of the project's utils module, while
// json.dumps() is not the project's own dumps.
func TestPythonModuleCallReachesModuleFunction(t *testing.T) {
	dir, store := indexFixture(t, "py", python.NewAnalyzer(), map[string]string{
		"utils.py": "def helper2(x):\n    return x\n\n\ndef dumps(obj):\n    return obj\n",
		"main.py":  "import json\nimport utils\n\n\ndef main():\n    return utils.helper2(1)\n\n\ndef save(obj):\n    return json.dumps(obj)\n",
	})
	eng := setupTestEngine(store)
	t.Cleanup(eng.Close)
	tool := tools.NewCallHierarchyTool(eng)
	path := filepath.Join(dir, "utils.py")
	if got := pythonCallers(t, tool, path, "helper2"); len(got) != 1 || got[0] != "main" {
		t.Errorf("callers of helper2 = %v, want [main]", got)
	}
	if got := pythonCallers(t, tool, path, "dumps"); len(got) != 0 {
		t.Errorf("callers of dumps = %v, want none", got)
	}
}

// import pkg.utils as u binds u to the module, so u.helper() calls its helper.
func TestPythonAliasedModuleCallReachesModuleFunction(t *testing.T) {
	dir, store := indexFixture(t, "py", python.NewAnalyzer(), map[string]string{
		"pkg/__init__.py": "",
		"pkg/utils.py":    "def helper():\n    return 1\n",
		"pkg/main.py":     "import pkg.utils as u\nfrom pkg import utils as uu\n\n\ndef call_alias():\n    return u.helper()\n\n\ndef call_from_alias():\n    return uu.helper()\n",
	})
	eng := setupTestEngine(store)
	t.Cleanup(eng.Close)
	if got := pythonCallers(t, tools.NewCallHierarchyTool(eng), filepath.Join(dir, "pkg", "utils.py"), "helper"); !slices.Equal(got, []string{"call_alias", "call_from_alias"}) {
		t.Fatalf("callers of helper = %v, want [call_alias call_from_alias]", got)
	}
}

// super().run() calls the run the class inherits, not its own.
func TestPythonSuperCallReachesParentMethod(t *testing.T) {
	dir, store := indexFixture(t, "py", python.NewAnalyzer(), map[string]string{
		"base.py":  "class Base:\n    def run(self):\n        return 1\n",
		"child.py": "from base import Base\n\n\nclass Child(Base):\n    def run(self):\n        return super().run()\n",
	})
	eng := setupTestEngine(store)
	t.Cleanup(eng.Close)
	if got := pythonCallers(t, tools.NewCallHierarchyTool(eng), filepath.Join(dir, "base.py"), "Base.run"); len(got) != 1 || got[0] != "run" {
		t.Fatalf("callers of Base.run = %v, want [run]", got)
	}
}

// signer.sign() on a value of unknown type may call any method sign, but no
// free function sign; json.dumps() calls an imported module, not a method.
func TestPythonInstanceCallReachesMethod(t *testing.T) {
	dir, store := indexFixture(t, "py", python.NewAnalyzer(), map[string]string{
		"signer.py":     "class Signer:\n    def sign(self, v):\n        return v\n",
		"other.py":      "class Other:\n    def sign(self, v):\n        return v\n",
		"helpers.py":    "def sign(v):\n    return v\n",
		"serializer.py": "class Serializer:\n    def dumps(self, o):\n        return o\n",
		"main.py":       "import json as _json\nfrom signer import Signer\n\n\ndef use():\n    signer = Signer()\n    return signer.sign(1)\n\n\ndef dump(o):\n    return _json.dumps(o)\n",
	})
	eng := setupTestEngine(store)
	t.Cleanup(eng.Close)
	tool := tools.NewCallHierarchyTool(eng)
	for file, want := range map[string][]string{"signer.py": {"use"}, "other.py": {"use"}, "helpers.py": nil} {
		if got := pythonCallers(t, tool, filepath.Join(dir, file), "sign"); !slices.Equal(got, want) {
			t.Errorf("callers of sign in %s = %v, want %v", file, got, want)
		}
	}
	if got := pythonCallers(t, tool, filepath.Join(dir, "serializer.py"), "Serializer.dumps"); len(got) != 0 {
		t.Errorf("callers of Serializer.dumps = %v, want none", got)
	}
	text, err := tools.NewFindUsagesTool(eng).Execute(context.Background(), map[string]interface{}{"symbol_name": "Signer.sign", "file_path": filepath.Join(dir, "signer.py")})
	if err != nil {
		t.Fatal(err)
	}
	var response struct{ Data []tools.UsageResult }
	if err := json.Unmarshal([]byte(text), &response); err != nil {
		t.Fatal(err)
	}
	if len(response.Data) != 1 || response.Data[0].Name != "use" {
		t.Errorf("usages of Signer.sign want [use]: %s", text)
	}
}

// d.get() on a value of unknown type may call the project's only get, so
// the call is an ambiguous candidate, not a resolved edge into Cache.get.
func TestPythonUnknownReceiverCallIsAmbiguous(t *testing.T) {
	dir, store := indexFixture(t, "py", python.NewAnalyzer(), map[string]string{
		"cache.py": "class Cache:\n    def get(self, k):\n        return k\n",
		"main.py":  "def lookup(d, k):\n    return d.get(k)\n",
	})
	eng := setupTestEngine(store)
	t.Cleanup(eng.Close)
	tool := tools.NewCallHierarchyTool(eng)
	for _, c := range []struct{ file, name, direction string }{
		{"cache.py", "Cache.get", "incoming"},
		{"main.py", "lookup", "outgoing"},
	} {
		text, err := tool.Execute(context.Background(), map[string]interface{}{
			"symbol_name": c.name, "direction": c.direction, "file_path": filepath.Join(dir, c.file), "depth": 1,
		})
		if err != nil {
			t.Fatal(err)
		}
		var hierarchy struct{ Data tools.CallNode }
		if err := json.Unmarshal([]byte(text), &hierarchy); err != nil {
			t.Fatal(err)
		}
		if len(hierarchy.Data.Children) != 1 || hierarchy.Data.Children[0].Resolution != "ambiguous" {
			t.Errorf("%s %s: want one ambiguous child: %s", c.direction, c.name, text)
		}
	}
}

// import pkg.utils as u names pkg.utils, not every module named utils, and
// from db import session binds a value, whose session.commit() is a method call.
func TestPythonImportBindingNamesFullModule(t *testing.T) {
	dir, store := indexFixture(t, "py", python.NewAnalyzer(), map[string]string{
		"pkg/__init__.py":   "",
		"pkg/utils.py":      "def helper():\n    return 1\n",
		"other/__init__.py": "",
		"other/utils.py":    "def helper():\n    return 2\n",
		"pkg/main.py":       "import pkg.utils as u\n\n\ndef call_alias():\n    return u.helper()\n",
		"db.py":             "class Session:\n    def commit(self):\n        return 1\n\n\nsession = Session()\n",
		"app.py":            "from db import session\n\n\ndef save():\n    return session.commit()\n",
	})
	eng := setupTestEngine(store)
	t.Cleanup(eng.Close)
	tool := tools.NewCallHierarchyTool(eng)
	if got := pythonCallers(t, tool, filepath.Join(dir, "pkg", "utils.py"), "helper"); !slices.Equal(got, []string{"call_alias"}) {
		t.Errorf("callers of pkg.utils helper = %v, want [call_alias]", got)
	}
	if got := pythonCallers(t, tool, filepath.Join(dir, "other", "utils.py"), "helper"); len(got) != 0 {
		t.Errorf("callers of other.utils helper = %v, want none", got)
	}
	if got := pythonCallers(t, tool, filepath.Join(dir, "db.py"), "Session.commit"); !slices.Equal(got, []string{"save"}) {
		t.Errorf("callers of Session.commit = %v, want [save]", got)
	}
}

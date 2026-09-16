package golang

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// _test.go files are indexed as their own package group, so a test that
// calls the API shows up in usages and call hierarchies.
func TestAnalyzeIncludesTestFiles(t *testing.T) {
	dir := t.TempDir()
	files := map[string]string{
		"calc.go":          "package calc\n\n// Add adds.\nfunc Add(a, b int) int { return a + b }\n",
		"calc_test.go":     "package calc\n\nimport \"testing\"\n\nfunc TestAdd(t *testing.T) { if Add(1, 2) != 3 { t.Fatal() } }\n",
		"calc_ext_test.go": "package calc_test\n\nimport \"testing\"\n\nfunc TestAddExternal(t *testing.T) { _ = 1 }\n",
	}
	for name, src := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	res, err := NewCodeAnalyzer().Analyze(context.Background(), filepath.Join(dir, "calc.go"))
	if err != nil {
		t.Fatal(err)
	}
	byName := map[string]string{}
	calls := map[string]bool{}
	for _, s := range res.Symbols {
		byName[s.Name] = s.Package
		for _, r := range s.Relations {
			if s.Name == "TestAdd" && r.Type == "calls" {
				calls[r.TargetName] = true
			}
		}
	}
	if byName["Add"] != "calc" || byName["TestAdd"] != "calc" || byName["TestAddExternal"] != "calc_test" {
		t.Fatalf("symbols/packages = %v", byName)
	}
	if !calls["Add"] {
		t.Fatalf("TestAdd should record a call to Add, got %v", calls)
	}
}

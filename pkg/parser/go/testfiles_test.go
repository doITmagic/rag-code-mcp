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

// Test helpers returning, or methods on, a type declared in the package's
// non-test files must be indexed once, and the package's own symbols must
// not be duplicated by the test group.
func TestAnalyzeTestHelpersOnPackageTypes(t *testing.T) {
	dir := t.TempDir()
	files := map[string]string{
		"box.go":      "package box\n\ntype Box struct{}\n\nfunc NewBox() *Box { return &Box{} }\n",
		"box_test.go": "package box\n\nimport \"testing\"\n\nfunc setUpBox() *Box { return NewBox() }\n\nfunc (b *Box) testOnly() {}\n\nfunc TestBox(t *testing.T) { setUpBox().testOnly() }\n",
	}
	for name, src := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	res, err := NewCodeAnalyzer().Analyze(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	count := map[string]int{}
	for _, s := range res.Symbols {
		got[s.Name] = filepath.Base(s.FilePath)
		count[s.Name]++
	}
	want := map[string]string{"Box": "box.go", "NewBox": "box.go", "setUpBox": "box_test.go", "testOnly": "box_test.go", "TestBox": "box_test.go"}
	for name, file := range want {
		if got[name] != file || count[name] != 1 {
			t.Errorf("%s: file %q count %d, want %q once (all: %v)", name, got[name], count[name], file, count)
		}
	}
}

// go/doc keeps one func per name, so init in several files and build-tag
// variants of a func or method were indexed once, from a random file.
func TestAnalyzeKeepsSameNamedFuncsInSeveralFiles(t *testing.T) {
	dir := t.TempDir()
	files := map[string]string{
		"a.go":            "package dup\n\nfunc init() {}\n",
		"b.go":            "package dup\n\nfunc init() {}\n",
		"s.go":            "package dup\n\ntype S struct{}\n",
		"plat_linux.go":   "//go:build linux\n\npackage dup\n\nfunc Platform() string { return \"linux\" }\n\nfunc (s S) Close() error { return nil }\n",
		"plat_windows.go": "//go:build windows\n\npackage dup\n\nfunc Platform() string { return \"windows\" }\n\nfunc (s S) Close() error { return nil }\n",
		"a_test.go":       "package dup\n\nfunc init() {}\n",
		"b_test.go":       "package dup\n\nfunc init() {}\n",
	}
	for name, src := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	res, err := NewCodeAnalyzer().Analyze(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]int{}
	for _, s := range res.Symbols {
		got[filepath.Base(s.FilePath)+":"+s.Name]++
	}
	for _, key := range []string{"a.go:init", "b.go:init", "a_test.go:init", "b_test.go:init",
		"plat_linux.go:Platform", "plat_windows.go:Platform", "plat_linux.go:Close", "plat_windows.go:Close"} {
		if got[key] != 1 {
			t.Errorf("%s indexed %d times, want once (all: %v)", key, got[key], got)
		}
	}
}

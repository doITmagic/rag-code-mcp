package tests

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/doITmagic/rag-code-mcp/internal/service/tools"
	"github.com/doITmagic/rag-code-mcp/pkg/parser/php"
	"slices"
)

func TestNamespacedPHPClassKeepsUsages(t *testing.T) {
	dir, store := indexFixture(t, "php", php.NewAnalyzer(), map[string]string{
		"Base.php": "<?php namespace App; class Base {} class Child extends Base {}",
	})
	eng := setupTestEngine(store)
	t.Cleanup(eng.Close)
	for _, name := range []string{"Base", "App\\Base"} {
		text, err := tools.NewFindUsagesTool(eng).Execute(context.Background(), map[string]interface{}{"symbol_name": name, "file_path": filepath.Join(dir, "Base.php")})
		if err != nil {
			t.Fatal(err)
		}
		var response struct{ Data []tools.UsageResult }
		if err := json.Unmarshal([]byte(text), &response); err != nil {
			t.Fatal(err)
		}
		if len(response.Data) != 1 || response.Data[0].Name != "Child" {
			t.Errorf("%s usages want Child: %s", name, text)
		}
	}
}

// Dotenv::load names the method by its class's short name; other classes'
// load() and the method itself are no usages of it.
func TestPHPShortClassMethodNameResolves(t *testing.T) {
	dir, store := indexFixture(t, "php", php.NewAnalyzer(), map[string]string{
		"Dotenv.php":   "<?php\nnamespace Dotenv;\nclass Dotenv {\n  public function load() { return $this->loader->load(); }\n}\n",
		"Loader.php":   "<?php\nnamespace Dotenv\\Loader;\nclass Loader {\n  public function load() { return 1; }\n}\n",
		"RepoTest.php": "<?php\nnamespace Tests;\nclass RepoTest {\n  private function load() { return 2; }\n  public function testIt() { return $this->load(); }\n}\n",
		"Boot.php":     "<?php\nnamespace App;\nclass Boot {\n  public function run($dotenv) { return $dotenv->load(); }\n}\n",
	})
	eng := setupTestEngine(store)
	t.Cleanup(eng.Close)
	text, err := tools.NewFindUsagesTool(eng).Execute(context.Background(), map[string]interface{}{"symbol_name": "Dotenv::load", "file_path": filepath.Join(dir, "Dotenv.php")})
	if err != nil {
		t.Fatal(err)
	}
	var response struct{ Data []tools.UsageResult }
	if err := json.Unmarshal([]byte(text), &response); err != nil {
		t.Fatal(err)
	}
	if len(response.Data) != 1 || response.Data[0].Name != "run" {
		t.Errorf("Dotenv::load usages want [run]: %s", text)
	}
}

// `new Entry(...)` is a usage of the class it names, resolved through the
// namespace and imports, so another namespace's Entry does not match.
func TestPHPNewIsAUsageOfTheClass(t *testing.T) {
	dir, store := indexFixture(t, "php", php.NewAnalyzer(), map[string]string{
		"Entry.php":       "<?php\nnamespace Dotenv\\Parser;\nclass Entry {}\n",
		"EntryParser.php": "<?php\nnamespace Dotenv\\Parser;\nclass EntryParser {\n  public function parse($n) { return new Entry($n); }\n}\n",
		"make.php":        "<?php\nnamespace App;\nfunction make() { return new \\Dotenv\\Parser\\Entry(); }\n",
		"Other.php":       "<?php\nnamespace App;\nclass Entry {}\nfunction other() { return new Entry(); }\n",
	})
	eng := setupTestEngine(store)
	t.Cleanup(eng.Close)
	text, err := tools.NewFindUsagesTool(eng).Execute(context.Background(), map[string]interface{}{"symbol_name": "Dotenv\\Parser\\Entry", "file_path": filepath.Join(dir, "Entry.php")})
	if err != nil {
		t.Fatal(err)
	}
	var response struct{ Data []tools.UsageResult }
	if err := json.Unmarshal([]byte(text), &response); err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, u := range response.Data {
		got = append(got, u.Name)
	}
	slices.Sort(got)
	if !slices.Equal(got, []string{"make", "parse"}) {
		t.Errorf("usages of Entry = %v, want [make parse]: %s", got, text)
	}
}

// A class in the global namespace (no namespace line) is found by `new Foo()`
// in the same file and by `new \Foo()` from a namespaced file.
func TestPHPNewOfGlobalClassIsAUsage(t *testing.T) {
	dir, store := indexFixture(t, "php", php.NewAnalyzer(), map[string]string{
		"Glob.php": "<?php\nclass Foo {}\nfunction g() { return new Foo(); }\n",
		"make.php": "<?php\nnamespace App;\nfunction make() { return new \\Foo(); }\n",
	})
	eng := setupTestEngine(store)
	t.Cleanup(eng.Close)
	text, err := tools.NewFindUsagesTool(eng).Execute(context.Background(), map[string]interface{}{"symbol_name": "Foo", "file_path": filepath.Join(dir, "Glob.php")})
	if err != nil {
		t.Fatal(err)
	}
	var response struct{ Data []tools.UsageResult }
	if err := json.Unmarshal([]byte(text), &response); err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, u := range response.Data {
		got = append(got, u.Name)
	}
	slices.Sort(got)
	if !slices.Equal(got, []string{"g", "make"}) {
		t.Errorf("usages of Foo = %v, want [g make]: %s", got, text)
	}
}

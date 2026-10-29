package tests

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/doITmagic/rag-code-mcp/internal/service/tools"
	"github.com/doITmagic/rag-code-mcp/pkg/parser/php"
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

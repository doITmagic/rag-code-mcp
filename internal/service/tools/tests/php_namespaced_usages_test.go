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

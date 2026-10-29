package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/doITmagic/rag-code-mcp/internal/config"
)

const userConfig = `# my notes
llm:
  provider: ollama
  ollama_embed: nomic-embed-text # indexes were built with this
storage:
  vector_db:
    url: http://localhost:6333
`

func writeUserConfig(t *testing.T) string {
	t.Helper()
	t.Setenv("QDRANT_API_KEY", "env-secret-123")
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(userConfig), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestCheckConfigUpgradeUnattendedKeepsConfig(t *testing.T) {
	path := writeUserConfig(t)

	checkConfigUpgrade(path) // -y defaults to true, as on reinstall and auto-update

	if got, _ := os.ReadFile(path); string(got) != userConfig {
		t.Fatalf("unattended run rewrote config.yaml:\n%s", got)
	}
}

func TestCheckConfigUpgradeConfirmedOnlyChangesModel(t *testing.T) {
	path := writeUserConfig(t)
	orig := *assumeYes
	*assumeYes = false
	t.Cleanup(func() { *assumeYes = orig })
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	_, _ = w.WriteString("y\n")
	w.Close()
	stdin := os.Stdin
	os.Stdin = r
	t.Cleanup(func() { os.Stdin = stdin; r.Close() })

	checkConfigUpgrade(path)

	want := strings.Replace(userConfig, "nomic-embed-text", config.StableEmbeddingModel, 1)
	if got, _ := os.ReadFile(path); string(got) != want {
		t.Fatalf("config.yaml = \n%s\nwant\n%s", got, want)
	}
}

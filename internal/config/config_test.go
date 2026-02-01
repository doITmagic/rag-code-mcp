package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDefaultConfigValues(t *testing.T) {
	cfg := DefaultConfig()
	if cfg == nil {
		t.Fatalf("DefaultConfig() returned nil")
	}

	if cfg.LLM.Provider != "ollama" {
		t.Errorf("LLM.Provider = %q, want %q", cfg.LLM.Provider, "ollama")
	}
	if cfg.LLM.OllamaBaseURL != "http://localhost:11434" {
		t.Errorf("LLM.OllamaBaseURL = %q, want %q", cfg.LLM.OllamaBaseURL, "http://localhost:11434")
	}
	if cfg.Storage.VectorDB.Provider != "qdrant" {
		t.Errorf("VectorDB.Provider = %q, want %q", cfg.Storage.VectorDB.Provider, "qdrant")
	}
	if cfg.Storage.VectorDB.URL != "http://localhost:6333" {
		t.Errorf("VectorDB.URL = %q, want %q", cfg.Storage.VectorDB.URL, "http://localhost:6333")
	}
	if !cfg.Workspace.Enabled {
		t.Errorf("Workspace.Enabled = false, want true")
	}
	if cfg.Workspace.CollectionPrefix != "coderag" {
		t.Errorf("Workspace.CollectionPrefix = %q, want %q", cfg.Workspace.CollectionPrefix, "coderag")
	}
	if cfg.CodeRAG.Collection != "do-ai-code" {
		t.Errorf("CodeRAG.Collection = %q, want %q", cfg.CodeRAG.Collection, "do-ai-code")
	}
	if cfg.Docs.Collection != "do-ai-docs" {
		t.Errorf("Docs.Collection = %q, want %q", cfg.Docs.Collection, "do-ai-docs")
	}
	if cfg.APIDocs.Collection != "do-ai-api-docs" {
		t.Errorf("APIDocs.Collection = %q, want %q", cfg.APIDocs.Collection, "do-ai-api-docs")
	}
}

func TestLoadMissingFileReturnsDefaultConfig(t *testing.T) {
	tempDir := t.TempDir()
	missing := filepath.Join(tempDir, "no-such-config.yaml")

	cfg, err := Load(missing)
	if err != nil {
		t.Fatalf("Load(%q) returned error: %v", missing, err)
	}
	if cfg == nil {
		t.Fatalf("Load(%q) returned nil config", missing)
	}

	if cfg.LLM.Provider != "ollama" {
		t.Errorf("LLM.Provider = %q, want %q", cfg.LLM.Provider, "ollama")
	}
}

func TestLoadParsesYAMLAndValidates(t *testing.T) {
	tempDir := t.TempDir()
	path := filepath.Join(tempDir, "config.yaml")

	yamlContent := []byte(`
llm:
  provider: ollama
  ollama_base_url: http://localhost:9000
  ollama_model: custom-model
storage:
  vector_db:
    provider: qdrant
    url: http://qdrant:6333
    collection: custom-code
server:
  port: 9090
`)
	if err := os.WriteFile(path, yamlContent, 0o644); err != nil {
		t.Fatalf("failed to write temp config: %v", err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load(%q) returned error: %v", path, err)
	}

	if cfg.LLM.OllamaBaseURL != "http://localhost:9000" {
		t.Errorf("LLM.OllamaBaseURL = %q, want %q", cfg.LLM.OllamaBaseURL, "http://localhost:9000")
	}
	if cfg.LLM.OllamaModel != "custom-model" {
		t.Errorf("LLM.OllamaModel = %q, want %q", cfg.LLM.OllamaModel, "custom-model")
	}
	if cfg.Storage.VectorDB.Collection != "custom-code" {
		t.Errorf("VectorDB.Collection = %q, want %q", cfg.Storage.VectorDB.Collection, "custom-code")
	}
	if cfg.Server.Port != 9090 {
		t.Errorf("Server.Port = %d, want %d", cfg.Server.Port, 9090)
	}
}

func TestApplyEnvOverrides(t *testing.T) {
	cfg := DefaultConfig()

	t.Setenv("QDRANT_URL", "http://qdrant:7777")
	t.Setenv("QDRANT_COLLECTION", "my-code")
	t.Setenv("DOCS_COLLECTION", "my-docs")
	t.Setenv("DOCS_README_PATH", "./OTHER.md")
	t.Setenv("DOCS_PATHS", "./docs, ./more-docs  ")
	t.Setenv("API_DOCS_COLLECTION", "api-docs")
	t.Setenv("WORKSPACE_ENABLED", "false")
	t.Setenv("WORKSPACE_AUTO_INDEX", "false")
	t.Setenv("WORKSPACE_MAX_WORKSPACES", "42")
	t.Setenv("WORKSPACE_COLLECTION_PREFIX", "mycoderag")

	applyEnvOverrides(cfg)

	if cfg.Storage.VectorDB.URL != "http://qdrant:7777" {
		t.Errorf("VectorDB.URL = %q, want %q", cfg.Storage.VectorDB.URL, "http://qdrant:7777")
	}
	if cfg.Storage.VectorDB.Collection != "my-code" {
		t.Errorf("VectorDB.Collection = %q, want %q", cfg.Storage.VectorDB.Collection, "my-code")
	}
	if cfg.Docs.Collection != "my-docs" {
		t.Errorf("Docs.Collection = %q, want %q", cfg.Docs.Collection, "my-docs")
	}
	if cfg.Docs.ReadmePath != "./OTHER.md" {
		t.Errorf("Docs.ReadmePath = %q, want %q", cfg.Docs.ReadmePath, "./OTHER.md")
	}
	if len(cfg.Docs.DocsPaths) != 2 || cfg.Docs.DocsPaths[0] != "./docs" || cfg.Docs.DocsPaths[1] != "./more-docs" {
		t.Errorf("Docs.DocsPaths = %#v, want [./docs ./more-docs]", cfg.Docs.DocsPaths)
	}
	if cfg.APIDocs.Collection != "api-docs" {
		t.Errorf("APIDocs.Collection = %q, want %q", cfg.APIDocs.Collection, "api-docs")
	}
	if cfg.Workspace.Enabled {
		t.Errorf("Workspace.Enabled = true, want false")
	}
	if cfg.Workspace.AutoIndex {
		t.Errorf("Workspace.AutoIndex = true, want false")
	}
	if cfg.Workspace.MaxWorkspaces != 42 {
		t.Errorf("Workspace.MaxWorkspaces = %d, want %d", cfg.Workspace.MaxWorkspaces, 42)
	}
	if cfg.Workspace.CollectionPrefix != "mycoderag" {
		t.Errorf("Workspace.CollectionPrefix = %q, want %q", cfg.Workspace.CollectionPrefix, "mycoderag")
	}
}

func TestValidateDefaultsProviderAndRequiresModel(t *testing.T) {
	cfg := DefaultConfig()
	cfg.LLM.Provider = "" // should default to ollama

	if err := validate(cfg); err != nil {
		t.Fatalf("validate(default cfg) returned error: %v", err)
	}
	if cfg.LLM.Provider != "ollama" {
		t.Errorf("after validate, LLM.Provider = %q, want %q", cfg.LLM.Provider, "ollama")
	}

	cfgNoModel := DefaultConfig()
	cfgNoModel.LLM.OllamaModel = ""
	cfgNoModel.LLM.Model = ""
	if err := validate(cfgNoModel); err == nil {
		t.Fatalf("validate(cfg without model) = nil error, want non-nil")
	}

	cfgBadProvider := DefaultConfig()
	cfgBadProvider.LLM.Provider = "huggingface"
	if err := validate(cfgBadProvider); err == nil {
		t.Fatalf("validate(cfg with bad provider) = nil error, want non-nil")
	}
}

func TestValidateServerPort(t *testing.T) {
	cfg := DefaultConfig()
	// Server.Port is currently unused by the MCP runtime; validate should not
	// reject configurations based solely on the port value.
	cfg.Server.Port = 70000
	if err := validate(cfg); err != nil {
		t.Fatalf("validate(cfg with high port) returned unexpected error: %v", err)
	}
}

package main

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// An unattended install or upgrade (how auto-update runs it) must use the
// Ollama and Qdrant that config.yaml names, not start local ones: it started
// ollama serve plus GPU Ollama and Qdrant containers with --restart always on
// a machine whose services live on another host.
func TestSetupEnvironmentUsesConfiguredRemoteServices(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shims are shell scripts")
	}
	home := isolate(t)
	for _, env := range []string{"OLLAMA_BASE_URL", "QDRANT_URL", "OLLAMA_MODEL", "OLLAMA_EMBED"} {
		t.Setenv(env, "")
	}
	binDir := filepath.Join(home, installDirName, binDirName)
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := "llm:\n  ollama_base_url: http://ollama.invalid:11434\n  ollama_embed: nomic-embed-text\nstorage:\n  vector_db:\n    url: http://qdrant.invalid:6333\n"
	if err := os.WriteFile(filepath.Join(binDir, "config.yaml"), []byte(cfg), 0o644); err != nil {
		t.Fatal(err)
	}

	shims := t.TempDir()
	calls := filepath.Join(t.TempDir(), "calls")
	for _, name := range []string{"ollama", "docker"} {
		script := "#!/bin/sh\necho \"" + name + " $*\" >> '" + calls + "'\n"
		if err := os.WriteFile(filepath.Join(shims, name), []byte(script), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", shims)
	origOllama, origQdrant := *ollamaMode, *qdrantMode
	t.Cleanup(func() { *ollamaMode, *qdrantMode = origOllama, origQdrant })

	setupEnvironment()

	if data, err := os.ReadFile(calls); err == nil {
		t.Fatalf("started local services although config.yaml names remote ones:\n%s", data)
	}
}

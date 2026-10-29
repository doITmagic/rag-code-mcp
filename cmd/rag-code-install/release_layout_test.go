package main

import (
	"os"
	"path/filepath"
	"testing"
)

// TestInstallsFromExtractedReleaseArchive lays out a directory the way
// .goreleaser.yaml packs a release (no config.yaml at the root): the installer
// must use the extracted binaries instead of downloading the release again.
func TestInstallsFromExtractedReleaseArchive(t *testing.T) {
	src := t.TempDir()
	for _, f := range []string{"rag-code-mcp", "rag-code-mcp.exe", "rag-code-install", "README.md", "LICENSE", "llms.txt", "internal/config/default.yaml"} {
		path := filepath.Join(src, f)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("x"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if needsDownload(src) {
		t.Fatal("extracted release archive rejected; installer would download it again")
	}

	bin := t.TempDir()
	installConfig(src, bin)
	if _, err := os.Stat(filepath.Join(bin, "config.yaml")); err != nil {
		t.Fatalf("no config.yaml installed: %v", err)
	}
}

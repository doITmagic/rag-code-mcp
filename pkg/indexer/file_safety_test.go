package indexer

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	_ "github.com/doITmagic/rag-code-mcp/pkg/parser/generic" // register text analyzer
)

func writeTestFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

// A file symlink is never followed: its target may live outside the
// workspace (/etc/passwd, ~/.aws/credentials) and rag_search would return it.
func TestIndexWorkspaceSkipsFileSymlinks(t *testing.T) {
	root, outside := t.TempDir(), t.TempDir()
	writeTestFile(t, filepath.Join(root, "main.go"), "package main\nfunc Kept() {}\n")
	writeTestFile(t, filepath.Join(outside, "credentials"), "aws_secret_access_key = outsideSecretMarker\n")
	writeTestFile(t, filepath.Join(outside, "x.go"), "package main\nfunc OutsideLeakMarker() {}\n")
	for link, target := range map[string]string{"notes.txt": "credentials", "leak.go": "x.go"} {
		if err := os.Symlink(filepath.Join(outside, target), filepath.Join(root, link)); err != nil {
			t.Skipf("symlinks unavailable: %v", err)
		}
	}

	store := &mockStore{}
	svc := NewService(&mockEmbedder{}, store)
	for _, lang := range []string{"go", "generic"} {
		if err := svc.IndexWorkspace(context.Background(), root, lang, Options{Language: lang}); err != nil {
			t.Fatal(err)
		}
	}
	kept := false
	for _, point := range store.upsertPoints {
		content, _ := point.Payload["content"].(string)
		path, _ := point.Payload["file_path"].(string)
		if strings.Contains(content, "outsideSecretMarker") || strings.Contains(content, "OutsideLeakMarker") {
			t.Fatalf("outside content indexed under %s: %q", path, content)
		}
		kept = kept || strings.Contains(content, "func Kept")
	}
	if !kept {
		t.Fatal("regular workspace file was not indexed")
	}
}

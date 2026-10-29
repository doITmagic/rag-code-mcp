//go:build unix

package indexer

import (
	"context"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	_ "github.com/doITmagic/rag-code-mcp/pkg/parser/php/laravel"   // register Laravel detection
	_ "github.com/doITmagic/rag-code-mcp/pkg/parser/php/wordpress" // register PHP analyzer and WordPress detection
	_ "github.com/doITmagic/rag-code-mcp/pkg/parser/python"        // register Python analyzer
)

// Opening a FIFO with no writer blocks forever, which stalled the run and,
// through the single indexing slot, every other workspace.
func TestIndexWorkspaceSkipsFIFOs(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, filepath.Join(root, "main.go"), "package main\nfunc Kept() {}\n")
	// WordPress detection reads the other *.php files next to an indexed one,
	// Laravel detection reads composer.json there and in the parents.
	writeTestFile(t, filepath.Join(root, "a.php"), "<?php\nfunction kept() {}\n")
	for _, name := range []string{"fifo.go", "fifo.py", "fifo.php", "composer.json"} {
		if err := syscall.Mkfifo(filepath.Join(root, name), 0o600); err != nil {
			t.Skipf("mkfifo unavailable: %v", err)
		}
	}

	store := &mockStore{}
	svc := NewService(&mockEmbedder{}, store)
	done := make(chan error, 1)
	go func() {
		for _, lang := range []string{"go", "python", "php"} {
			if err := svc.IndexWorkspace(context.Background(), root, lang, Options{Language: lang}); err != nil {
				done <- err
				return
			}
		}
		done <- nil
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("indexing blocked on a FIFO")
	}
	if len(store.upsertPoints) == 0 {
		t.Fatal("regular workspace file was not indexed")
	}
}

// An unreadable file (root-owned 0600, chmod 000) is a local condition no
// retry fixes; it must not mark the whole run failed, as for Go files.
func TestIndexWorkspaceSkipsUnreadableFiles(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root reads files regardless of permissions")
	}
	root := t.TempDir()
	writeTestFile(t, filepath.Join(root, "ok.py"), "def kept():\n    pass\n")
	writeTestFile(t, filepath.Join(root, "ok.php"), "<?php\nfunction kept() {}\n")
	for _, name := range []string{"locked.py", "locked.php"} {
		path := filepath.Join(root, name)
		writeTestFile(t, path, "def locked():\n    pass\n")
		if err := os.Chmod(path, 0); err != nil {
			t.Fatal(err)
		}
	}

	store := &mockStore{}
	svc := NewService(&mockEmbedder{}, store)
	for _, lang := range []string{"python", "php"} {
		if err := svc.IndexWorkspace(context.Background(), root, lang, Options{Language: lang}); err != nil {
			t.Fatalf("%s: %v", lang, err)
		}
	}
	if len(store.upsertPoints) < 2 {
		t.Fatalf("readable files were not indexed: %d points", len(store.upsertPoints))
	}
}

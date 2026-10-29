//go:build unix

package indexer

import (
	"context"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	_ "github.com/doITmagic/rag-code-mcp/pkg/parser/php/wordpress" // register PHP analyzer and WordPress detection
	_ "github.com/doITmagic/rag-code-mcp/pkg/parser/python"        // register Python analyzer
)

// Opening a FIFO with no writer blocks forever, which stalled the run and,
// through the single indexing slot, every other workspace.
func TestIndexWorkspaceSkipsFIFOs(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, filepath.Join(root, "main.go"), "package main\nfunc Kept() {}\n")
	// WordPress detection reads the other *.php files next to an indexed one.
	writeTestFile(t, filepath.Join(root, "a.php"), "<?php\nfunction kept() {}\n")
	for _, name := range []string{"fifo.go", "fifo.py", "fifo.php"} {
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

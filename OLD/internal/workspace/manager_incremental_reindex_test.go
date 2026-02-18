package workspace

import (
	"context"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/doITmagic/rag-code-mcp/internal/config"
	"github.com/doITmagic/rag-code-mcp/internal/storage"
)

func TestCheckAndReindexIfNeeded_TriggersOnModifiedFile(t *testing.T) {
	root := t.TempDir()
	goFile := filepath.Join(root, "main.go")
	if err := os.WriteFile(goFile, []byte("package main\nfunc main() {}\n"), 0644); err != nil {
		t.Fatalf("failed to create go file: %v", err)
	}

	initialInfo, err := os.Stat(goFile)
	if err != nil {
		t.Fatalf("failed to stat go file: %v", err)
	}

	state := NewWorkspaceState()
	state.UpdateFile(goFile, initialInfo)
	stateFile := filepath.Join(root, ".ragcode", "state.json")
	if err := state.Save(stateFile); err != nil {
		t.Fatalf("failed to save state: %v", err)
	}

	if err := os.WriteFile(goFile, []byte("package main\nfunc main() { println(\"changed\") }\n"), 0644); err != nil {
		t.Fatalf("failed to modify go file: %v", err)
	}

	modifiedInfo, err := os.Stat(goFile)
	if err != nil {
		t.Fatalf("failed to stat modified go file: %v", err)
	}
	if !modifiedInfo.ModTime().After(initialInfo.ModTime()) && modifiedInfo.Size() == initialInfo.Size() {
		// Filesystem timestamp granularity fallback.
		time.Sleep(20 * time.Millisecond)
		if err := os.WriteFile(goFile, []byte("package main\nfunc main() { println(\"changed-again\") }\n"), 0644); err != nil {
			t.Fatalf("failed to modify go file again: %v", err)
		}
	}

	mockQ := &mockQdrant{
		exists: true,
		collectionInfo: &storage.CollectionInfo{
			VectorSize: 768,
			PointsCount: 1,
		},
		pointCount: 1,
	}

	mgr := &Manager{
		qdrant: mockQ,
		llm:    &fakeLLM{dimension: 768},
		config: &config.Config{},
	}

	var indexCalls int32
	mgr.indexLanguageFn = func(ctx context.Context, info *Info, language string, collectionName string, force bool) error {
		atomic.AddInt32(&indexCalls, 1)
		return nil
	}

	info := &Info{ID: "ws-modified", Root: root, CollectionPrefix: "ragcode"}
	mgr.checkAndReindexIfNeeded(context.Background(), info, "go", info.CollectionNameForLanguage("go"))

	if got := atomic.LoadInt32(&indexCalls); got != 1 {
		t.Fatalf("expected incremental reindex to be triggered once, got %d", got)
	}
}

func TestCheckAndReindexIfNeeded_NoTriggerWhenUnchanged(t *testing.T) {
	root := t.TempDir()
	goFile := filepath.Join(root, "main.go")
	if err := os.WriteFile(goFile, []byte("package main\nfunc main() {}\n"), 0644); err != nil {
		t.Fatalf("failed to create go file: %v", err)
	}

	fileInfo, err := os.Stat(goFile)
	if err != nil {
		t.Fatalf("failed to stat go file: %v", err)
	}

	state := NewWorkspaceState()
	state.UpdateFile(goFile, fileInfo)
	stateFile := filepath.Join(root, ".ragcode", "state.json")
	if err := state.Save(stateFile); err != nil {
		t.Fatalf("failed to save state: %v", err)
	}

	mockQ := &mockQdrant{
		exists: true,
		collectionInfo: &storage.CollectionInfo{
			VectorSize: 768,
			PointsCount: 1,
		},
		pointCount: 1,
	}

	mgr := &Manager{
		qdrant: mockQ,
		llm:    &fakeLLM{dimension: 768},
		config: &config.Config{},
	}

	var indexCalls int32
	mgr.indexLanguageFn = func(ctx context.Context, info *Info, language string, collectionName string, force bool) error {
		atomic.AddInt32(&indexCalls, 1)
		return nil
	}

	info := &Info{ID: "ws-unchanged", Root: root, CollectionPrefix: "ragcode"}
	mgr.checkAndReindexIfNeeded(context.Background(), info, "go", info.CollectionNameForLanguage("go"))

	if got := atomic.LoadInt32(&indexCalls); got != 0 {
		t.Fatalf("expected no incremental reindex trigger for unchanged files, got %d", got)
	}
}

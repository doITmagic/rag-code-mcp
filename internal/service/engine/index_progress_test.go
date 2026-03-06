package engine

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestProgressStoreDeepCopy verifies that get() returns an independent copy
// of the Languages map, so callers cannot corrupt the cached snapshot.
func TestProgressStoreDeepCopy(t *testing.T) {
	store := newProgressStore()
	now := time.Now()

	store.start("ws1", "/tmp/ws1", "job1", now)
	store.update("ws1", "go", 5, 10, now)

	p := store.get("ws1", "")
	if p == nil {
		t.Fatal("expected progress, got nil")
	}

	// Mutate the returned copy
	p.Languages["go"] = IndexLanguageProgress{TotalFiles: 999}

	// Re-fetch and ensure the store was not corrupted
	p2 := store.get("ws1", "")
	if p2.Languages["go"].TotalFiles == 999 {
		t.Error("deep copy failed: mutation of returned Languages map corrupted the store")
	}
}

// TestProgressStoreDiskRoundTrip verifies save+load round-trip and deep copy
// for the disk-loaded branch of get().
func TestProgressStoreDiskRoundTrip(t *testing.T) {
	dir := t.TempDir()
	wsRoot := filepath.Join(dir, "workspace")
	if err := os.MkdirAll(wsRoot, 0o755); err != nil {
		t.Fatal(err)
	}

	store := newProgressStore()
	now := time.Now().UTC().Truncate(time.Second)

	store.start("ws-disk", wsRoot, "job-disk", now)
	store.update("ws-disk", "go", 3, 10, now)
	store.complete("ws-disk", wsRoot, now)

	// Create a fresh store (simulating restart) and load from disk
	freshStore := newProgressStore()
	p := freshStore.get("ws-disk", wsRoot)
	if p == nil {
		t.Fatal("expected progress loaded from disk, got nil")
	}
	if p.State != "completed" {
		t.Errorf("expected state 'completed', got %q", p.State)
	}
	if _, ok := p.Languages["go"]; !ok {
		t.Error("expected 'go' language entry in loaded progress")
	}

	// Verify deep copy from disk branch—mutating should not affect cache
	p.Languages["go"] = IndexLanguageProgress{TotalFiles: 777}
	p2 := freshStore.get("ws-disk", wsRoot)
	if p2.Languages["go"].TotalFiles == 777 {
		t.Error("deep copy failed for disk-loaded branch: mutation corrupted cached entry")
	}
}

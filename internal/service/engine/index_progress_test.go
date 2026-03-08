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
	wsRoot := t.TempDir()

	store.start("ws1", wsRoot, "job1", now)
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

// TestProgressStoreGlobalPercent verifies that GlobalPercent is calculated correctly
// as sum(done_all_langs) / sum(total_all_langs) * 100.
func TestProgressStoreGlobalPercent(t *testing.T) {
	store := newProgressStore()
	now := time.Now()
	wsRoot := t.TempDir()

	store.start("ws1", wsRoot, "job1", now)
	store.update("ws1", "md", 120, 120, now) // md: 100% (120/120)
	store.update("ws1", "go", 234, 500, now) // go: 46%  (234/500)

	p := store.get("ws1", "")
	if p == nil {
		t.Fatal("expected progress, got nil")
	}

	// global = (120 + 234) / (120 + 500) * 100 = 354/620*100 = 57%
	want := 57
	if p.GlobalPercent != want {
		t.Errorf("expected GlobalPercent=%d, got %d", want, p.GlobalPercent)
	}
}

// TestProgressStoreCurrentLanguage verifies that CurrentLanguage is updated on each update().
func TestProgressStoreCurrentLanguage(t *testing.T) {
	store := newProgressStore()
	now := time.Now()
	wsRoot := t.TempDir()

	store.start("ws2", wsRoot, "job2", now)
	store.update("ws2", "md", 10, 100, now)

	p := store.get("ws2", "")
	if p.CurrentLanguage != "md" {
		t.Errorf("expected CurrentLanguage='md', got %q", p.CurrentLanguage)
	}

	store.update("ws2", "go", 5, 200, now)
	p = store.get("ws2", "")
	if p.CurrentLanguage != "go" {
		t.Errorf("expected CurrentLanguage='go', got %q", p.CurrentLanguage)
	}
}

// TestProgressStoreTwoWorkspacesIsolated verifies that two workspaces updating
// the store concurrently do not corrupt each other's GlobalPercent or Languages.
// This covers the real production scenario: wsA and wsB indexing simultaneously
// (allowed since indexingJobs dedup is per workspace ID, not global).
func TestProgressStoreTwoWorkspacesIsolated(t *testing.T) {
	store := newProgressStore()
	now := time.Now()
	rootA := t.TempDir()
	rootB := t.TempDir()

	// WsA: go 50/100, python 0/200 → global = (50+0)/(100+200)*100 = 16%
	// python is in the denominator because it was added via update() with 0 processed/200 total
	store.start("wsA", rootA, "jobA", now)
	store.update("wsA", "go", 50, 100, now)
	store.update("wsA", "python", 0, 200, now)

	// WsB: go 200/200 → global = 200/200 = 100%
	store.start("wsB", rootB, "jobB", now)
	store.update("wsB", "go", 200, 200, now)

	pA := store.get("wsA", "")
	pB := store.get("wsB", "")

	if pA == nil || pB == nil {
		t.Fatal("expected both workspaces to have progress, got nil")
	}

	// WsA: global = (50+0)/(100+200)*100 = 16
	wantA := 16
	if pA.GlobalPercent != wantA {
		t.Errorf("wsA: expected GlobalPercent=%d, got %d", wantA, pA.GlobalPercent)
	}

	// WsB: global = 200/200*100 = 100
	wantB := 100
	if pB.GlobalPercent != wantB {
		t.Errorf("wsB: expected GlobalPercent=%d, got %d", wantB, pB.GlobalPercent)
	}

	// WsA should have python (was added with 0/200), wsB should not
	if _, exists := pA.Languages["python"]; !exists {
		t.Error("wsA should have python language entry (added with 0/200)")
	}
	if _, exists := pB.Languages["python"]; exists {
		t.Error("wsB should not have python language (was only set on wsA)")
	}

	// Verify wsA progress is not polluted by wsB's go collection
	if pA.Languages["go"].TotalFiles == 200 {
		t.Error("wsA.go should have TotalFiles=100, not 200 (wsB pollution)")
	}
}

// TestProgressStoreTwoWorkspacesConcurrent verifies no data races when two
// workspaces update the store from separate goroutines simultaneously.
// Run with -race to detect races.
func TestProgressStoreTwoWorkspacesConcurrent(t *testing.T) {
	store := newProgressStore()
	now := time.Now()

	store.start("cA", t.TempDir(), "jA", now)
	store.start("cB", t.TempDir(), "jB", now)

	const iters = 50
	done := make(chan struct{}, 2)

	go func() {
		for i := 0; i < iters; i++ {
			store.update("cA", "go", i, iters, time.Now())
		}
		done <- struct{}{}
	}()

	go func() {
		for i := 0; i < iters; i++ {
			store.update("cB", "python", i, iters, time.Now())
		}
		done <- struct{}{}
	}()

	<-done
	<-done

	pA := store.get("cA", "")
	pB := store.get("cB", "")

	if pA == nil || pB == nil {
		t.Fatal("expected progress for both workspaces after concurrent updates")
	}
	// After 50 updates each, final state should be 49/50 for each
	if pA.Languages["go"].DoneFiles != iters-1 {
		t.Errorf("wsA go: expected done=%d, got %d", iters-1, pA.Languages["go"].DoneFiles)
	}
	if pB.Languages["python"].DoneFiles != iters-1 {
		t.Errorf("wsB python: expected done=%d, got %d", iters-1, pB.Languages["python"].DoneFiles)
	}
}

// TestProgressStoreIncrementalPreservesProgress verifies that when a completed
// workspace is re-indexed incrementally, the progress carries over from the
// previous run instead of resetting to 0%.
//
// Scenario: Go had 228/228 (100%). New run starts, preRegister sets total=233.
// Expected: progress shows 228/233 ≈ 97%, NOT 0/233 = 0%.
func TestProgressStoreIncrementalPreservesProgress(t *testing.T) {
	store := newProgressStore()
	now := time.Now()
	wsRoot := t.TempDir()

	// Phase 1: complete a full indexing run
	store.start("ws1", wsRoot, "job1", now)
	store.update("ws1", "go", 228, 228, now)
	store.update("ws1", "docs", 809, 809, now)
	store.complete("ws1", wsRoot, now)

	p := store.get("ws1", "")
	if p == nil || p.State != "completed" || p.GlobalPercent != 100 {
		t.Fatalf("expected completed at 100%%, got state=%q pct=%d", p.State, p.GlobalPercent)
	}

	// Phase 2: start a new incremental indexing run (e.g., auto-trigger on connect)
	store.start("ws1", wsRoot, "job2", now)

	// preRegister with updated totals (5 new Go files appeared after git pull)
	store.preRegister("ws1", "go", 233, now)
	store.preRegister("ws1", "docs", 809, now)

	p2 := store.get("ws1", "")
	if p2 == nil {
		t.Fatal("expected progress after restart, got nil")
	}

	// Go: 228 done out of 233 total = 97%
	goLang := p2.Languages["go"]
	if goLang.DoneFiles != 228 {
		t.Errorf("go: expected DoneFiles=228 (carried over), got %d", goLang.DoneFiles)
	}
	if goLang.TotalFiles != 233 {
		t.Errorf("go: expected TotalFiles=233 (from preRegister), got %d", goLang.TotalFiles)
	}

	// Docs: 809/809 unchanged
	docsLang := p2.Languages["docs"]
	if docsLang.DoneFiles != 809 {
		t.Errorf("docs: expected DoneFiles=809 (carried over), got %d", docsLang.DoneFiles)
	}

	// GlobalPercent = (228+809) / (233+809) * 100 = 1037/1042*100 = 99%
	wantGlobal := 99
	if p2.GlobalPercent != wantGlobal {
		t.Errorf("expected GlobalPercent=%d, got %d", wantGlobal, p2.GlobalPercent)
	}

	// Verify state is "starting", not "completed"
	if p2.State != "starting" {
		t.Errorf("expected state='starting', got %q", p2.State)
	}
}

# Multi-Collection Indexing Overhaul — Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Fix progress tracking bugs in multi-language indexing and simplify the goroutine model for debuggability.

**Architecture:** Replace the dual `state.json`/`progressStore` truth sources with a single `index_status.json` persisted by a debounced save goroutine. Remove `globalIndexSemaphore` and file worker pools in favor of a simple sequential for-loop per language.

**Tech Stack:** Go, `log/slog` via `logger.Instance`, `sync.Mutex`, `time.Ticker` for debounced writes.

**Design doc:** `docs/plans/2026-03-07-indexing-overhaul-design.md`

**Branch:** `feat/indexing-overhaul`

---

## Overview of changes

```
pkg/indexer/state.go           — remove LastPercent field
pkg/indexer/service.go         — remove globalIndexSemaphore + worker pool → simple for loop
internal/service/engine/index_progress.go  — add global_percent + debounced disk flush
internal/service/engine/engine.go          — pre-scan, search reads index_status.json, ErrIndexingBusy
```

Tests to verify after each task: `go test ./pkg/indexer/... ./internal/service/engine/...`

---

## Task 1: Remove `LastPercent` from `State` struct

**Files:**
- Modify: `pkg/indexer/state.go`
- Modify: `pkg/indexer/service.go` (remove `state.SetLastPercent(pct)` calls)
- Test: `pkg/indexer/service_test.go`

### Step 1: Write failing test that verifies State has no LastPercent

```go
// In pkg/indexer/state_test.go (create if missing)
func TestStateHasNoLastPercent(t *testing.T) {
    s := NewState()
    data, err := json.Marshal(s)
    require.NoError(t, err)
    // LastPercent must NOT be serialized
    assert.NotContains(t, string(data), "last_percent")
}
```

### Step 2: Run test — expect FAIL (field exists)

```bash
go test ./pkg/indexer/... -run TestStateHasNoLastPercent -v
```
Expected: FAIL — `last_percent` present in JSON.

### Step 3: Remove `LastPercent` from State

In `pkg/indexer/state.go`:
- Remove field `LastPercent int \`json:"last_percent"\`` from `State` struct
- Remove method `SetLastPercent(percent int)`

### Step 4: Remove all calls to `SetLastPercent`

In `pkg/indexer/service.go`, remove:
```go
state.SetLastPercent(pct) // line ~316
```
Also remove the `pct` variable calculation above it (lines ~311-314).

In `internal/service/engine/engine.go`, remove the two blocks:
```go
// Remove this:
if state, err := indexer.LoadState(statePath); err == nil {
    state.SetLastPercent(1)
    _ = state.Save(statePath)
}
// And this (end of IndexWorkspace):
if state, err := indexer.LoadState(statePath); err == nil {
    state.SetLastPercent(100)
    _ = state.Save(statePath)
}
```

### Step 5: Run tests

```bash
go test ./pkg/indexer/... ./internal/service/engine/... -v
```
Expected: all pass (or only pre-existing failures).

### Step 6: Commit

```bash
git add pkg/indexer/state.go pkg/indexer/service.go internal/service/engine/engine.go
git commit -m "refactor(indexer): remove LastPercent from State — progress moves to index_status.json"
```

---

## Task 2: Remove `globalIndexSemaphore` + file worker pool

**Files:**
- Modify: `pkg/indexer/service.go`

The goal: replace the worker-pool goroutines with a simple `for _, path := range changedFiles { IndexFile(...) }` loop.

### Step 1: Write test verifying sequential file processing

```go
// In pkg/indexer/service_test.go
func TestIndexWorkspaceIsSequential(t *testing.T) {
    // This test verifies that IndexWorkspace calls Progress callback
    // in strictly ascending order (sequential = no out-of-order increments).
    var calls []int
    var mu sync.Mutex
    opts := Options{
        Language: "go",
        Progress: func(done, total int) {
            mu.Lock()
            calls = append(calls, done)
            mu.Unlock()
        },
    }
    // Use a mock store + embedder that return immediately
    // ... (use existing test helpers from service_test.go)
    // Assert calls are strictly [1, 2, 3, ...]
    for i := 1; i < len(calls); i++ {
        assert.Equal(t, calls[i-1]+1, calls[i], "progress calls must be sequential")
    }
}
```

### Step 2: Run test — verify it passes already or documents current behavior

```bash
go test ./pkg/indexer/... -run TestIndexWorkspaceIsSequential -v
```

### Step 3: Replace worker pool with simple loop

In `pkg/indexer/service.go`, replace the section starting at `// 4. Process changed files using the global semaphore`:

**Remove:**
```go
var globalIndexSemaphore = func() chan struct{} { ... }()  // entire package-level var

// In IndexWorkspace, remove:
numFileWorkers := cap(globalIndexSemaphore)
filePaths := make(chan string, totalFiles)
for _, p := range changedFiles { filePaths <- p }
close(filePaths)

var (
    fileWg    sync.WaitGroup
    errMu     sync.Mutex
    fileErrs  []string
    doneFiles atomic.Int64
)

// ... the entire save+watchdog goroutine block ...
// ... the worker goroutine for loop ...
fileWg.Wait()
close(saveStop)
```

**Add (simple loop):**
```go
var fileErrs []string
doneFiles := 0

// Dedicated periodic-save + stall watchdog goroutine
saveStop := make(chan struct{})
go func() {
    saveTicker := time.NewTicker(10 * time.Second)
    stallTicker := time.NewTicker(60 * time.Second)
    defer saveTicker.Stop()
    defer stallTicker.Stop()
    stallCount := 0
    lastDone := 0
    for {
        select {
        case <-saveTicker.C:
            if err := state.Save(statePath); err != nil {
                logger.Instance.Warn("[IDX] ws=%s lang=%s periodic state save failed: %v", wsName, opts.Language, err)
            }
        case <-stallTicker.C:
            if doneFiles < totalFiles && doneFiles == lastDone {
                stallCount++
                logger.Instance.Warn("[IDX] ws=%s lang=%s ⚠️ STALL: no progress for 60s [%d/%d] (stall #%d)",
                    wsName, opts.Language, doneFiles, totalFiles, stallCount)
                if stallCount >= 2 {
                    if err := healthcheck.PingOllama(""); err != nil {
                        logger.Instance.Error("[IDX] ws=%s lang=%s ❌ Ollama unresponsive: %v — forcing restart", wsName, opts.Language, err)
                    }
                    attemptOllamaRestart()
                }
            } else {
                stallCount = 0
            }
            lastDone = doneFiles
        case <-saveStop:
            return
        }
    }
}()

for _, path := range changedFiles {
    doneFiles++
    langPct := doneFiles * 100 / totalFiles

    logger.Instance.Debug("[IDX] ws=%s lang=%s [%d/%d] %s (%d%%)",
        wsName, opts.Language, doneFiles, totalFiles, filepath.Base(path), langPct)

    if opts.Progress != nil {
        opts.Progress(doneFiles, totalFiles)
    }

    _, indexErr := s.IndexFile(ctx, collection, path, state)
    if indexErr != nil {
        logger.Instance.Warn("[IDX] ws=%s lang=%s ⚠️ %s: %v",
            wsName, opts.Language, filepath.Base(path), indexErr)
        fileErrs = append(fileErrs, fmt.Sprintf("%s: %v", path, indexErr))
    }
}

close(saveStop)
```

> Note: `wsName` needs to be passed in via `Options` struct (see Task 3).

### Step 4: Add `WorkspaceName` to `Options`

In `pkg/indexer/service.go` — `Options` struct, add:
```go
WorkspaceName string // for logging (basename of workspace root)
```

### Step 5: Remove unused imports

Remove `sync/atomic`, `sync.WaitGroup` from `service.go` if no longer used.

### Step 6: Run tests

```bash
go test ./pkg/indexer/... -v
```

### Step 7: Commit

```bash
git add pkg/indexer/service.go
git commit -m "refactor(indexer): replace globalIndexSemaphore+worker pool with simple sequential loop"
```

---

## Task 3: Add `global_percent` + debounced flush to `index_progress.go`

**Files:**
- Modify: `internal/service/engine/index_progress.go`
- Modify: `internal/service/engine/index_progress_test.go`

### Step 1: Write failing test for `globalPercent()`

```go
// In index_progress_test.go
func TestProgressStoreGlobalPercent(t *testing.T) {
    s := newProgressStore()
    s.start("ws1", "/root", "job1", time.Now())
    s.update("ws1", "md", 120, 120, time.Now())   // md: 100%
    s.update("ws1", "go", 234, 500, time.Now())   // go: 46%

    p := s.get("ws1", "")
    require.NotNil(t, p)
    // global = (120 + 234) / (120 + 500) * 100 = 354/620*100 = 57%
    assert.Equal(t, 57, p.GlobalPercent)
}
```

### Step 2: Run test — expect FAIL (field missing)

```bash
go test ./internal/service/engine/... -run TestProgressStoreGlobalPercent -v
```

### Step 3: Add `GlobalPercent` to `IndexProgress` and `CurrentLanguage`

In `index_progress.go`, update `IndexProgress` struct:
```go
type IndexProgress struct {
    JobID           string                           `json:"job_id"`
    WorkspaceID     string                           `json:"workspace_id"`
    WorkspaceRoot   string                           `json:"workspace_root"`
    State           string                           `json:"state"`
    GlobalPercent   int                              `json:"global_percent"`   // NEW
    CurrentLanguage string                           `json:"current_language"` // NEW
    LanguagesQueued []string                         `json:"languages_queued"` // NEW
    StartedAt       time.Time                        `json:"started_at"`
    CompletedAt     *time.Time                       `json:"completed_at,omitempty"`
    Languages       map[string]IndexLanguageProgress `json:"languages,omitempty"`
    UpdatedAt       time.Time                        `json:"updated_at"`
    Error           string                           `json:"error,omitempty"`
}
```

### Step 4: Add `globalPercent()` helper to `progressStore`

```go
// calcGlobalPercent computes global_percent as sum(done) / sum(total) * 100.
// Call with s.mu held.
func calcGlobalPercent(langs map[string]IndexLanguageProgress) int {
    var totalDone, totalFiles int
    for _, lp := range langs {
        totalDone += lp.DoneFiles
        totalFiles += lp.TotalFiles
    }
    if totalFiles == 0 {
        return 0
    }
    pct := totalDone * 100 / totalFiles
    if pct > 100 {
        return 100
    }
    return pct
}
```

Update `progressStore.update()` to set `GlobalPercent`:
```go
func (s *progressStore) update(workspaceID, lang string, done, total int, now time.Time) {
    // ... existing code ...
    p.Languages[lang] = IndexLanguageProgress{ ... }
    p.GlobalPercent = calcGlobalPercent(p.Languages) // NEW
    p.CurrentLanguage = lang                          // NEW
    p.UpdatedAt = now
}
```

### Step 5: Add debounced flush to `index_status.json`

Add a `flushCh chan struct{}` to `progressStore` and a background flush goroutine:

```go
type progressStore struct {
    mu      sync.Mutex
    jobs    map[string]*IndexProgress
    flushCh chan struct{} // signals debounced flush
}

func newProgressStore() *progressStore {
    ps := &progressStore{
        jobs:    map[string]*IndexProgress{},
        flushCh: make(chan struct{}, 1),
    }
    go ps.runFlusher()
    return ps
}

// runFlusher drains flushCh and persists all jobs debounced at 500ms.
func (s *progressStore) runFlusher() {
    for range s.flushCh {
        time.Sleep(500 * time.Millisecond)
        // Drain any pending signals during the sleep
        for len(s.flushCh) > 0 {
            <-s.flushCh
        }
        s.mu.Lock()
        for _, p := range s.jobs {
            if p.WorkspaceRoot != "" {
                saveIndexStatus(p.WorkspaceRoot, p)
            }
        }
        s.mu.Unlock()
    }
}

// triggerFlush signals the flusher non-blockingly.
func (s *progressStore) triggerFlush() {
    select {
    case s.flushCh <- struct{}{}:
    default: // already pending, no need to queue another
    }
}
```

Call `s.triggerFlush()` at end of `update()`, `complete()`, `fail()`.

### Step 6: Run tests

```bash
go test ./internal/service/engine/... -v
```

### Step 7: Commit

```bash
git add internal/service/engine/index_progress.go internal/service/engine/index_progress_test.go
git commit -m "feat(engine): add global_percent + debounced index_status.json flush to progressStore"
```

---

## Task 4: Pre-scan total files + search reads `index_status.json`

**Files:**
- Modify: `internal/service/engine/engine.go`

### Step 1: Add `ErrIndexingBusy` error type

```go
// ErrIndexingBusy is returned when any workspace is currently being indexed
// and a second workspace requests indexing (serialized by design).
type ErrIndexingBusy struct {
    ActiveWorkspaceRoot string
}

func (e *ErrIndexingBusy) Error() string {
    return fmt.Sprintf("indexing busy: workspace %q is currently being indexed; try again after it completes", e.ActiveWorkspaceRoot)
}
```

### Step 2: Add `WorkspaceName` to `indexer.Options` call in `engine.IndexWorkspace`

Update the `e.indexer.IndexWorkspace(...)` call to pass workspace name:
```go
err := e.indexer.IndexWorkspace(ctx, wctx.Root, collection, indexer.Options{
    Language:        lang,
    WorkspaceName:   filepath.Base(wctx.Root), // NEW
    ExcludePatterns: e.config.Workspace.ExcludePatterns,
    Recreate:        recreate,
    Progress:        progressCb,
})
```

### Step 3: Add pre-scan for total files and set `LanguagesQueued`

At the start of `engine.IndexWorkspace`, before the language loop, set queued languages:
```go
// Inform progressStore of the full language queue upfront
if e.progress != nil {
    e.progress.setLanguagesQueued(wctx.ID, languages)
}

// Log start
wsName := filepath.Base(wctx.Root)
logger.Instance.Info("[IDX] ws=%s ▶ starting %d languages", wsName, len(languages))
```

Add `setLanguagesQueued` to `progressStore`:
```go
func (s *progressStore) setLanguagesQueued(workspaceID string, langs []string) {
    s.mu.Lock()
    defer s.mu.Unlock()
    if p, ok := s.jobs[workspaceID]; ok {
        p.LanguagesQueued = langs
    }
}
```

### Step 4: Add per-language Info logs to `engine.IndexWorkspace`

Wrap each language with start/done logs:
```go
for _, lang := range languages {
    wsName := filepath.Base(wctx.Root)
    logger.Instance.Info("[IDX] ws=%s lang=%s ▶ starting", wsName, lang)
    langStart := time.Now()

    // ... existing progressCb and indexer call ...

    elapsed := time.Since(langStart)
    if err != nil {
        logger.Instance.Error("[IDX] ws=%s lang=%s ❌ FAILED in %s: %v", wsName, lang, elapsed.Round(time.Second), err)
        indexErrors = append(indexErrors, fmt.Sprintf("%s: %v", lang, err))
    } else {
        // Get done files count from progressStore for the log
        if pgs := e.progress.get(wctx.ID, ""); pgs != nil {
            if lp, ok := pgs.Languages[lang]; ok {
                logger.Instance.Info("[IDX] ws=%s lang=%s ✅ DONE %d files in %s (global=%d%%)",
                    wsName, lang, lp.DoneFiles, elapsed.Round(time.Second), pgs.GlobalPercent)
            }
        }
    }
}

// Log global complete
logger.Instance.Info("[IDX] ws=%s ✅ COMPLETE all %d languages", wsName, len(languages))
```

### Step 5: Update search logic to read `index_status.json` for progress display

In `engine.SearchCode` and `engine.HybridSearchCode`, replace the `indexer.LoadState` check:

**Remove:**
```go
indexerStatePath := filepath.Join(wctx.Root, ".ragcode", "state.json")
if idxState, sErr := indexer.LoadState(indexerStatePath); sErr == nil {
    if idxState.LastPercent > 0 && idxState.LastPercent < 100 {
        // ... auto-resume logic ...
    }
}
```

**Add:**
```go
// Check index_status.json for interrupted/in-progress indexing
if p := e.GetIndexProgress(wctx.ID, wctx.Root); p != nil {
    switch p.State {
    case "running", "starting":
        if _, ok := e.indexingJobs.Load(wctx.ID); !ok {
            // Was running but process died — auto-resume
            const resumeCooldown = 5 * time.Minute
            now := time.Now()
            if last, loaded := e.resumeAttempts.Load(wctx.ID); !loaded || now.Sub(last.(time.Time)) > resumeCooldown {
                e.resumeAttempts.Store(wctx.ID, now)
                logger.Instance.Info("[IDX] ws=%s interrupted at %d%% — auto-resuming", filepath.Base(wctx.Root), p.GlobalPercent)
                e.StartIndexingAsync(wctx.Root, wctx.ID, nil, false)
            }
        }
        logger.Instance.Info("[IDX] ws=%s in progress (%d%%) — searching available results", filepath.Base(wctx.Root), p.GlobalPercent)
    }
}
```

### Step 6: Run tests

```bash
go test ./internal/service/engine/... -v
go test ./... 2>&1 | tail -20
```

### Step 7: Commit

```bash
git add internal/service/engine/engine.go
git commit -m "feat(engine): pre-scan langs queue, search reads index_status.json for progress"
```

---

## Task 5: Standardize all `log.Printf` → `logger.Instance` in indexer

**Files:**
- Modify: `pkg/indexer/service.go` (grep for remaining `log.Printf`)

### Step 1: Find all non-standard log calls

```bash
grep -n "log\.Printf\|log\.Println\|fmt\.Printf" pkg/indexer/service.go
```

### Step 2: Replace each with `logger.Instance.*` using `[IDX]` prefix

Pattern for replacements:
- `log.Printf("[INFO] ...")` → `logger.Instance.Info("[IDX] ...")`
- `log.Printf("[ERROR] ...")` → `logger.Instance.Error("[IDX] ...")`
- `log.Printf("[DEBUG] ...")` → `logger.Instance.Debug("[IDX] ...")`

Remove `"log"` from imports if no longer used.

### Step 3: Remove unused imports from service.go

```bash
go build ./pkg/indexer/...
```
Fix any import errors.

### Step 4: Run tests + build

```bash
go test ./pkg/indexer/... -v
go build ./...
```

### Step 5: Commit

```bash
git add pkg/indexer/service.go
git commit -m "chore(indexer): standardize all log.Printf → logger.Instance with [IDX] prefix"
```

---

## Task 6: End-to-end verification

### Step 1: Run full test suite

```bash
go test ./... -v 2>&1 | grep -E "^(=== RUN|--- (PASS|FAIL)|FAIL|ok)" | head -60
```
Expected: all pass.

### Step 2: Build binary

```bash
go build -o /tmp/rag-code-mcp-test ./cmd/rag-code-mcp/
```

### Step 3: Verify `state.json` no longer contains `last_percent`

```bash
cat /home/razvan/go/src/github.com/doITmagic/rag-code-mcp/.ragcode/state.json | python3 -c "import json,sys; d=json.load(sys.stdin); assert 'last_percent' not in d, 'last_percent still present!'; print('OK: last_percent removed')"
```

### Step 4: Manual log check

Trigger indexing on a small workspace and verify terminal output shows:
```
[IDX] ws=<name> ▶ starting N languages
[IDX] ws=<name> lang=<lang> ▶ starting
[IDX] ws=<name> lang=<lang> ✅ DONE X files in Ys (global=Z%)
[IDX] ws=<name> ✅ COMPLETE all N languages
```

With `MCP_LOG_LEVEL=debug`, verify per-file lines appear.

### Step 5: Verify `index_status.json` has correct global_percent

```bash
cat /home/razvan/go/src/github.com/doITmagic/rag-code-mcp/.ragcode/index_status.json | python3 -m json.tool | grep global_percent
```

### Step 6: Final commit

```bash
git add -A
git commit -m "chore: final cleanup after indexing overhaul"
```

---

## Summary of files changed

| File | Type |
|---|---|
| `pkg/indexer/state.go` | Remove `LastPercent`, `SetLastPercent` |
| `pkg/indexer/service.go` | Remove semaphore+workers → sequential loop; `WorkspaceName` in Options |
| `internal/service/engine/index_progress.go` | Add `GlobalPercent`, `CurrentLanguage`, `LanguagesQueued`; debounced flush |
| `internal/service/engine/index_progress_test.go` | Tests for global_percent calculation |
| `internal/service/engine/engine.go` | `ErrIndexingBusy`; pre-scan; search reads index_status.json; per-lang logs |

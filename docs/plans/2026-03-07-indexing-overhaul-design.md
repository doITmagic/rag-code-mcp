# Multi-Collection Indexing Overhaul — Design Document

**Date:** 2026-03-07  
**Status:** Approved  
**Scope:** `pkg/indexer/`, `internal/service/engine/`

---

## Problem

### Bug 1 — Inaccurate Progress (`last_percent`)

`state.json` contains the `last_percent` field which is **per-language**, not global.
For each new language, the progress resets from 0 to 100, independently.

**Buggy flow:**
```
engine.IndexWorkspace:
  state.json ← last_percent=1         (global start)

  lang="md":
    indexer ← LoadState → last_percent=1
    processes files → last_percent=46, 87, 100 (saved periodically)
    final → state.json: last_percent=100

  lang="go":
    indexer ← LoadState → last_percent=100  (picks up value from md!)
    processes files → last_percent=3, 5, 8... (RESET!)
    search sees 5% → "indexing interrupted" false positive
```

**Observed effects:**
- Search refused indexing at >80% md with "indexing in progress" message without real percentage
- `last_percent` reset between languages confusing the auto-resume logic
- `progressStore` in-memory was correct but did not persist to disk frequently enough

### Bug 2 — Undebuggable Goroutine Complexity

`globalIndexSemaphore` (package-level var) creates concurrency that is hard to trace:
- N file workers per `IndexWorkspace` call
- 1 periodic save goroutine per call (5 languages → 5 sequential goroutines)
- 1 stall watchdog goroutine per call
- Workers share the global semaphore → non-deterministic behavior if 2 workspaces index simultaneously

Since `IndexItems` has `numWorkers=1` (embedding is serial at Ollama), file worker parallelism provides no real performance gain, but adds maximum complexity.

---

## Solution

### Design Principles

1. **Single source of truth for progress** — `index_status.json` (not `state.json`)
2. **3 fixed goroutines** — regardless of how many languages exist
3. **1 workspace at a time** — simple, deterministic, easy to debug
4. **Verbosity via log levels** — per-file at Debug, per-language at Info, errors at Warn/Error

### New Architecture

```
StartIndexingAsync(ws):   ← 1 goroutine
  1. Pre-scan ALL languages → total_files_global (sum)
  2. for lang in languages:          ← sequential
       for file in lang_files:       ← sequential (simple for loop, no pool)
         IndexFile(file)             ← AST → embed → Qdrant
         progress++
         lang_pct  = progress_in_lang / lang_files * 100
         global_pct = progress_total / total_files * 100
         ← debounced write (500ms)

save goroutine (1 single):   ← debounced 500ms
  index_status.json ← global_pct + per-lang breakdown

watchdog goroutine (1 single):   ← 60s interval
  if no embed activity → Warn + restart Ollama if needed
```

### State Files

| File | Role | Relevant Fields |
|---|---|---|
| `state.json` | File tracking (modtime/size) | `files{}` — **no `last_percent`** |
| `index_status.json` | Progress tracking | `global_percent`, `languages{}`, `state`, `current_language` |

**`index_status.json` format:**
```json
{
  "workspace_id": "abc123",
  "workspace_root": "/home/user/project",
  "state": "running",
  "global_percent": 47,
  "current_language": "go",
  "languages_queued": ["md", "go", "python", "php", "html"],
  "languages": {
    "md": { "done": 120, "total": 120, "percent": 100, "state": "completed" },
    "go": { "done": 234, "total": 500, "percent": 46,  "state": "running"   },
    "py": { "done": 0,   "total": 89,  "percent": 0,   "state": "pending"   }
  },
  "started_at": "2026-03-07T12:00:00Z",
  "updated_at": "2026-03-07T12:05:23Z",
  "error": ""
}
```

### Workspace Serialization

- `indexingJobs sync.Map` remains for detecting an active job
- If indexing is requested for an already-active workspace → rejected immediately (existing behavior)
- If indexing is requested for another workspace while one is active → rejected with clear message (`ErrIndexingBusy`)
- No queue (YAGNI) — user can re-trigger after completion

### Logging Standard

**Format:** `[IDX] ws=<name> lang=<lang> [<n>/<total>] <file> (<lang_pct>%) global=<global_pct>%`

| Level | Content | Visible by Default |
|---|---|---|
| `Debug` | Per file processed | ❌ (MCP_LOG_LEVEL=debug) |
| `Info` | Language start/done, global complete | ✅ |
| `Warn` | Individual file error, stall detected | ✅ |
| `Error` | Language failure, Ollama/Qdrant down | ✅ |

**Info examples (default):**
```
[IDX] ws=rag-code-mcp  ▶ starting 5 languages (total files: 1247)
[IDX] ws=rag-code-mcp lang=md     ✅ DONE 120 files in 0m45s (global=10%)
[IDX] ws=rag-code-mcp lang=go     ✅ DONE 500 files in 4m12s (global=50%)
[IDX] ws=rag-code-mcp  ✅ COMPLETE all 5 languages in 8m42s
```

**Debug examples (MCP_LOG_LEVEL=debug):**
```
[IDX] ws=rag-code-mcp lang=go [  1/500] main.go     ( 0%) global=10%
[IDX] ws=rag-code-mcp lang=go [234/500] engine.go   (46%) global=23%
```

---

## What Does NOT Change

- `globalIndexSemaphore` — **removed** (provides no real benefit with serial embedding)
- `IndexItems` numWorkers=1 — remains (Ollama is serial anyway)
- Ollama circuit breaker — remains (useful)
- `EnsureLoaded` at language start — remains
- Periodic state.json save (file tracking) — remains

## What Changes

| Component | Change |
|---|---|
| `pkg/indexer/service.go` | Remove `globalIndexSemaphore` + file worker pool → simple for loop |
| `pkg/indexer/service.go` | Remove `state.SetLastPercent()` from file processing |
| `pkg/indexer/state.go` | Remove `LastPercent` field from `State` struct |
| `internal/service/engine/index_progress.go` | Add computed `global_percent` + debounced flush to `index_status.json` |
| `internal/service/engine/engine.go` | Pre-scan total files before loop; search logic reads `index_status.json` |
| `internal/service/engine/engine.go` | Add `ErrIndexingBusy` for second workspace |
| All indexing files | `log.Printf` → `logger.Instance.*` with standardized `[IDX]` format |

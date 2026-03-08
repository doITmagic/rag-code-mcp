# SmartSearch Execute() Refactor Plan

> Implementation should proceed task-by-task according to this plan.

**Goal:** Break the 309-line `Execute()` monolith into small, testable, single-responsibility functions following Go community standards (Uber Guide, Effective Go). Extract shared scoring logic into `pkg/scoring`.

**Architecture:** 
1. Create `pkg/scoring` for reusable pure functions duplicated across packages
2. Extract 6 logical pipeline stages into named functions in separate files
3. `Execute()` becomes a ~40-line orchestrator calling each stage

**Tech Stack:** Pure Go refactoring, no new dependencies.

---

## Duplications Found (via RagCode MCP)

| Function | Location 1 | Location 2 | Action |
|----------|-----------|-----------|--------|
| `filterTokens()` | `internal/service/search/search.go:214` | `engine/engine_fallback_search.go:248` | → `pkg/scoring.FilterTokens()` |
| `lexicalMatchScore()` | `internal/service/search/search.go:225` | `engine/engine_fallback_search.go:239` | → `pkg/scoring.LexicalMatchScore()` |
| `fallbackTokenMatchRatio()` | `engine/engine_fallback_search.go:225` | — | → `pkg/scoring.TokenMatchRatio()` |
| `pathProximity()` + helpers | `tools/smart_search_path_scope.go` | — | → `pkg/scoring.PathProximity()` |
| `longestCommonPath()` | `tools/smart_search_path_scope.go` | — | → `pkg/scoring.LongestCommonPath()` |

---

### Task 0: Create `pkg/scoring` — shared scoring primitives

**Files:**
- Create: `pkg/scoring/scoring.go` — exported pure functions
- Create: `pkg/scoring/path.go` — path proximity scoring  
- Create: `pkg/scoring/scoring_test.go` — tests
- Create: `pkg/scoring/path_test.go` — path tests
- Modify: `internal/service/search/search.go` — replace local with `scoring.FilterTokens`, `scoring.LexicalMatchScore`
- Modify: `internal/service/engine/engine_fallback_search.go` — replace local with `scoring.*`
- Modify: `internal/service/tools/smart_search_path_scope.go` — import from `scoring` or move there
- Delete: `internal/service/tools/smart_search_path_scope_test.go` — tests move to `pkg/scoring/path_test.go`

**`pkg/scoring/scoring.go`:**
```go
package scoring

// FilterTokens removes very short tokens (≤2 chars) from a token list.
func FilterTokens(tokens []string) []string

// LexicalMatchScore counts total token occurrences in content (frequency-weighted).
func LexicalMatchScore(content string, tokens []string) float64

// TokenMatchRatio returns the fraction of tokens found in text [0, 1].
func TokenMatchRatio(text string, tokens []string) float64
```

**`pkg/scoring/path.go`:**
```go
package scoring

// PathProximity computes a score multiplier based on how close
// a result's file path is to a reference scope directory.
func PathProximity(resultPath, scopePath string) float64

// ScopeDir extracts the reference directory from a file path.
func ScopeDir(filePath string) string

// LongestCommonPath returns the longest shared directory prefix.
func LongestCommonPath(a, b string) string
```

**Step 1:** Create `pkg/scoring/scoring.go` with exported functions
**Step 2:** Create `pkg/scoring/path.go` with path functions (from smart_search_path_scope.go)
**Step 3:** Create tests
**Step 4:** Update `search/search.go` to use `scoring.FilterTokens`, `scoring.LexicalMatchScore`
**Step 5:** Update `engine/engine_fallback_search.go` to use `scoring.*`
**Step 6:** Update `tools/smart_search_path_scope.go` to delegate to `scoring.PathProximity`
**Step 7:** Run: `go test ./... -count=1 -race`
**Step 8:** Commit: `refactor: extract pkg/scoring for shared scoring primitives`

---

## Current Structure Analysis

The `Execute()` method in `smart_search.go` (lines 100-408) has these responsibilities crammed into one function:

| Lines | Responsibility | Target |
|-------|---------------|--------|
| 100-115 | Input validation + defaults | `normalizeInput()` |
| 117-186 | Parallel search fan-out + result collection | `runParallelSearch()` |
| 188-191 | Error handling | `handleSearchError()` (exists) |
| 193-238 | Post-processing pipeline (merge, mode filter, score filter, path scope) | `applyFilters()` |
| 243-254 | Empty results check | inline (3 lines) |
| 257-306 | Response metadata construction (compact/full detection, fallback, warnings) | `buildResponseMeta()` |
| 308-406 | Result serialization (compact vs full) + telemetry + stale detection | `serializeResults()` |

---

### Task 1: Extract `normalizeInput()`

**Files:**
- Modify: `internal/service/tools/smart_search.go`
- Test: `internal/service/tools/smart_search_test.go` (add test)

**What:** Extract lines 100-115 into a pure function that validates and normalizes input.

```go
// normalizeInput validates the search input and applies defaults.
// Returns the effective query, limit, and modified input.
func normalizeInput(input SmartSearchInput, defaultLimit int) (string, int, SmartSearchInput, error) {
    query := strings.TrimSpace(input.Query)
    if query == "" {
        return "", 0, input, fmt.Errorf("query parameter is required")
    }
    limit := defaultLimit
    if input.Limit > 0 {
        limit = input.Limit
    }
    if input.Mode == "strict_docs" {
        input.IncludeDocs = true
    }
    return query, limit, input, nil
}
```

**Step 1:** Extract the function (pure, no receiver needed)
**Step 2:** Replace lines 100-115 with call to `normalizeInput`
**Step 3:** Run: `go test ./internal/service/tools/... -count=1 -race`
**Step 4:** Commit

---

### Task 2: Extract `searchMetadata` struct + `runParallelSearch()`

**Files:**
- Modify: `internal/service/tools/smart_search.go`

**What:** Extract lines 117-191 into a method. The local `searchResult` type and all the goroutine + channel logic moves out.

```go
// searchMetadata holds workspace context extracted from search results.
type searchMetadata struct {
    workspaceRoot   string
    workspaceID     string
    collection      string
    language        string
    detectionSource string
    mismatchRisk    string
}

// parallelSearchResult holds the output of runParallelSearch.
type parallelSearchResult struct {
    semantic *engine.SearchCodeResult
    hybrid   *engine.SearchCodeResult
    meta     searchMetadata
    err      error // first non-nil error from search strategies
}

// runParallelSearch executes semantic and hybrid searches concurrently,
// collects results, and extracts workspace metadata.
func (t *SmartSearchTool) runParallelSearch(ctx context.Context, filePath, query string, limit int, includeDocs bool) parallelSearchResult
```

**Step 1:** Extract struct + function
**Step 2:** Replace lines 117-191 in `Execute()` with one call
**Step 3:** Run tests
**Step 4:** Commit

---

### Task 3: Extract `applyFilters()`

**Files:**
- Modify: `internal/service/tools/smart_search.go`
- Test: `internal/service/tools/smart_search_test.go` (add test)

**What:** Extract lines 193-241 (merge + mode filter + score filter + path scope + doc grouping) into a single pipeline function.

```go
// filterConfig holds the filtering parameters for post-processing.
type filterConfig struct {
    Mode     string
    MinScore float32
    FilePath string // for path scoping
}

// applyFilters runs the full post-processing pipeline on merged results:
// mode filtering → score threshold → path scoping → doc grouping.
func (t *SmartSearchTool) applyFilters(merged []mergedResult, cfg filterConfig) []mergedResult
```

This function is ~40 lines calling the existing helpers: `applyModeFilter`, `applyScoreFilter`, `applyPathScoping`, `groupDocsByTree`.

**Step 1:** Extract `applyModeFilter()` (pure function, ~15 lines)
**Step 2:** Extract `applyScoreFilter()` (pure function, ~20 lines) 
**Step 3:** Create `applyFilters()` that chains them
**Step 4:** Replace lines 193-241 in Execute() with one call
**Step 5:** Run tests
**Step 6:** Commit

---

### Task 4: Extract `buildResponseMeta()`

**Files:**
- Modify: `internal/service/tools/smart_search.go`

**What:** Extract lines 257-306 (response object construction, fallback detection, warnings) into a function.

```go
// buildResponseMeta constructs the ToolResponse shell with metadata, warnings,
// and messaging based on whether results are from fallback or vector search.
func (t *SmartSearchTool) buildResponseMeta(meta searchMetadata, merged []mergedResult, useCompact bool) ToolResponse
```

**Step 1:** Extract function
**Step 2:** Replace lines 257-306 in Execute()
**Step 3:** Run tests
**Step 4:** Commit

---

### Task 5: Extract `serializeResults()`

**Files:**
- Modify: `internal/service/tools/smart_search.go`

**What:** Extract lines 308-406 (compact vs full serialization, telemetry, stale file detection) into a function.

```go
// serializeResults populates the ToolResponse with either compact or full result data,
// calculates telemetry savings, and detects stale indexed files.
func serializeResults(response *ToolResponse, merged []mergedResult, useCompact, isFallback bool)
```

**Step 1:** Extract function
**Step 2:** Replace lines 308-406 in Execute()
**Step 3:** Run tests
**Step 4:** Commit

---

### Task 6: Verify final `Execute()` orchestrator

**Files:**
- Verify: `internal/service/tools/smart_search.go`

**What:** The final `Execute()` should be ~40 lines:

```go
func (t *SmartSearchTool) Execute(ctx context.Context, input SmartSearchInput) (string, error) {
    query, limit, input, err := normalizeInput(input, t.searchLimit)
    if err != nil {
        return "", err
    }

    sr := t.runParallelSearch(ctx, input.FilePath, query, limit, input.IncludeDocs)
    if sr.semantic == nil && sr.hybrid == nil {
        return t.handleSearchError(sr.err, sr.meta.workspaceRoot, sr.meta.workspaceID)
    }

    merged := t.mergeResults(sr.semantic, sr.hybrid, limit)
    merged = t.applyFilters(merged, filterConfig{
        Mode: input.Mode, MinScore: input.MinScore, FilePath: input.FilePath,
    })

    if len(merged) == 0 {
        return noResultsResponse(query, sr.meta)
    }

    useCompact := len(merged) > compactResultCap || merged[0].score < highConfidenceThreshold
    if input.IncludeFullContent {
        useCompact = false
    }

    response := t.buildResponseMeta(sr.meta, merged, useCompact)
    serializeResults(&response, merged, useCompact, sr.meta.collection == "fallback")

    return response.JSON()
}
```

**Step 1:** Verify Execute() is ~40 lines
**Step 2:** Run full test suite: `go test ./... -count=1 -race`
**Step 3:** Commit: `refactor: break SmartSearch Execute into pipeline stages`

---

## File Organization (after refactor)

```
pkg/scoring/
├── scoring.go           # FilterTokens, LexicalMatchScore, TokenMatchRatio
├── scoring_test.go      # Tests for scoring functions
├── path.go              # ScopeDir, PathProximity, LongestCommonPath, CountSeparators
└── path_test.go         # Tests for path proximity (moved from tools/)

internal/service/tools/
├── smart_search.go                  # Execute orchestrator + Register + types (~120 lines)
├── smart_search_pipeline.go         # normalizeInput, runParallelSearch, applyFilters, buildResponseMeta, serializeResults
├── smart_search_path_scope.go       # thin wrapper calling scoring.PathProximity (applyPathScoping for []mergedResult)
├── smart_search_test.go             # Existing + new pipeline tests
└── smart_search_doc_grouping.go     # groupDocsByTree + readLines (moved from smart_search.go)
```

**Packages updated to use `pkg/scoring`:**
- `internal/service/search/search.go` → `scoring.FilterTokens`, `scoring.LexicalMatchScore`
- `internal/service/engine/engine_fallback_search.go` → `scoring.FilterTokens`, `scoring.LexicalMatchScore`, `scoring.TokenMatchRatio`
- `internal/service/tools/smart_search_path_scope.go` → `scoring.PathProximity`, `scoring.ScopeDir`

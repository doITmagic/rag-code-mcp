# RagCode MCP — Bug Tracker

This file documents confirmed bugs in the RagCode MCP server, with concrete reproduction examples and expected behavior.

---

## BUG-001: `rag_list_package_exports` falsely returns "No exported symbols found" for indexed Go packages

**Status:** ✅ Fixed (2026-03-09)  
**Date confirmed:** 2026-03-09  
**Affected tool:** `mcp_ragcode_rag_list_package_exports`  
**Severity:** Medium — produced incorrect responses that could mislead AI consumers  
**Fixed in:** `internal/service/tools/list_package_exports.go`

### Description

The `rag_list_package_exports` tool reports that a Go package contains no exported symbols, even though the source files contain public structs, functions, and variables (capitalized identifiers).

### Steps to reproduce

**Tool call input:**
```json
{
  "file_path": "/home/razvan/go/src/github.com/doITmagic/rag-code-mcp/pkg/indexer/service.go",
  "package": "github.com/doITmagic/rag-code-mcp/pkg/indexer"
}
```

**Response received (incorrect):**
```json
{
  "status": "success",
  "message": "No exported symbols found in package 'github.com/doITmagic/rag-code-mcp/pkg/indexer'",
  "context": {
    "workspace_root": "/home/razvan/go/src/github.com/doITmagic/rag-code-mcp",
    "detection_source": "file_path",
    "indexing_progress": {
      "started_at": "2026-03-09T07:33:28Z",
      "languages": {
        "go": {
          "on_disk": 232,
          "changed": 0,
          "processed": 0
        }
      }
    }
  }
}
```

### Actual exported symbols in `pkg/indexer/` (verified with grep)

Verified using `grep -rn "^(func|type|var|const)\s+[A-Z]"` on the `pkg/indexer/` directory:

**`service.go`:**
```go
type Options struct { ... }          // line 31
type Service struct { ... }          // line 40
func NewService(embedder llm.Provider, store storage.VectorStore) *Service  // line 47
```

**`state.go`:**
```go
type FileState struct { ... }        // line 12
type State struct { ... }            // line 21
func NewState() *State               // line 27
func LoadState(path string) (*State, error)  // line 34
```

**`index_status.go`:**
```go
type IndexStatus struct { ... }      // line 16
type LangStatus struct { ... }       // line 25
func SaveIndexStatus(workspaceRoot string, status *IndexStatus)  // line 32
func LoadIndexStatus(workspaceRoot string) *IndexStatus          // line 54
```

### Root cause (confirmed)

Verified by querying the vector database directly — **the data IS indexed**. A `rag_search` for `LangStatus`, `IndexStatus`, `SaveIndexStatus` returns results with scores of 0.86–0.94, sourced from `_source: "both"` (semantic + exact match). The data is in the index.

The real bug is a **package name mismatch** in `internal/service/tools/list_package_exports.go`:

```go
// The tool builds an exact-match filter using the full Go import path:
filter := map[string]interface{}{
    "package": packageName,  // e.g. "github.com/doITmagic/rag-code-mcp/pkg/indexer"
}
allResults, err := t.engine.ExactSearchPolyglot(ctx, wctx.ID, filter, 1000)
```

However, the vector index stores the short package name, not the full import path:
```json
{ "name": "LangStatus", "package": "indexer", ... }
```

The filter `"package": "github.com/doITmagic/rag-code-mcp/pkg/indexer"` never matches `"package": "indexer"` → `allResults` is always empty → the tool returns `"No exported symbols found"`.

### Applied fix

**File:** `internal/service/tools/list_package_exports.go`

```diff
-	filter := map[string]interface{}{
-		"package": packageName,
-	}
+	// The index stores the short package name (e.g. "indexer"), not the full Go
+	// import path (e.g. "github.com/doITmagic/rag-code-mcp/pkg/indexer").
+	// Normalize by taking the last path segment so both forms work.
+	filterPackage := packageName
+	if idx := strings.LastIndex(packageName, "/"); idx >= 0 {
+		filterPackage = packageName[idx+1:]
+	}
+	filter := map[string]interface{}{
+		"package": filterPackage,
+	}
```

This fix is backward-compatible: if the caller passes only the short name (e.g. `"indexer"`), `strings.LastIndex` returns `-1` and `filterPackage` is unchanged.

---

## BUG-002: `indexing_progress.changed` reports `0` even when files exist on disk

**Status:** Confirmed (related to BUG-001)  
**Date confirmed:** 2026-03-09  
**Affected tools:** All MCP tools that include `indexing_progress` in their response context  
**Severity:** Low — incorrect diagnostic information; does not directly affect search results

### Description

The `indexing_progress.languages.<lang>.changed` field may report `0` even though files are present on disk and may have been modified since the last full indexing run. This is because the metric reflects how many files were processed in the **current** indexing session, not how many differ from the last indexed state.

### Example

```json
"go": {
  "on_disk": 232,   // 232 Go files present on disk
  "changed": 0,     // no changes detected — misleading
  "processed": 0    // nothing processed in this session
}
```

In reality, the index may be completely stale — all 232 files could be unindexed — yet `changed` and `processed` both report `0` because no indexing session was triggered.

### Expected behavior

`changed` should reflect the number of files that differ from the last indexed snapshot (by `mtime` or content hash), not just files processed in the current in-flight session.

---

*Last updated: 2026-03-09 — BUG-001 fixed*

---

## BUG-003: Top-level Go functions with no AST relations are missing from the vector index

**Status:** Open  
**Date confirmed:** 2026-03-09  
**Affected component:** Go parser / indexer (`pkg/indexer`, `internal/parser`)  
**Severity:** Medium — `rag_list_package_exports` and `rag_search` silently omit exported constructor/loader functions

### Description

Some exported top-level Go functions are never written to the vector database by the indexer. They exist in source on disk, they are syntactically exported (capitalized name), but searching the vector store for them returns no dedicated entry — they appear only embedded inside the body content of *other* functions that call them.

### Affected symbols (confirmed via direct vector DB search)

All from `pkg/indexer/`:

| Symbol | File | Indexed? | Notes |
|---|---|---|---|
| `SaveIndexStatus` | `index_status.go:32` | ✅ yes | 6 AST relations |
| `LoadIndexStatus` | `index_status.go:54` | ❌ **no** | 0 dedicated index entry |
| `NewService` | `service.go:47` | ❌ **no** | `rag_find_usages` explicitly returned "No usages found" |
| `NewState` | `state.go:27` | ❌ **no** | No dedicated index entry |
| `LoadState` | `state.go:34` | ❌ **no** | No dedicated index entry |

### Diagnostic evidence

1. `rag_list_package_exports` for `pkg/indexer` returns 16 symbols — none of the 4 missing functions appear.
2. `rag_find_usages("NewService")` returns: `"No usages found for symbol 'NewService' based on Code Graph relations."` — the symbol has **zero AST relation entries** in Qdrant.
3. `rag_search` for `"func LoadIndexStatus"` only returns entries where `LoadIndexStatus` appears **in the body** of other functions (e.g. `engine.GetIndexStatus`, `engine.StartIndexingAsync`), never as a standalone symbol.
4. `SaveIndexStatus` (same file, same pattern) **is** indexed with 6 relations — confirming the issue is not file-level but symbol-level.

### Root cause (confirmed via direct Qdrant query)

**Direct Qdrant scroll on the collection reveals 25 points for package `indexer`.** Full list sorted by name confirms:

- `LangStatus` → `rel_count: 0`, **IS indexed** ✅
- `circuitBreakerThreshold` (private const) → `rel_count: 0`, **IS indexed** ✅
- `deleteCollectionTimeout` (private const) → `rel_count: 0`, **IS indexed** ✅

This **disproves** the relation-count-as-threshold hypothesis. Symbols with zero relations *are* indexed — the missing functions are simply absent.

**The pattern that distinguishes missing vs present functions:**

| Symbol | Indexed? | Called from outside the package? |
|---|---|---|
| `SaveIndexStatus` | ✅ | Yes — called from `engine.go` (different package) |
| `LoadIndexStatus` | ❌ | Only called from within `pkg/indexer/` itself |
| `NewService` | ❌ | Not tracked (0 AST relations despite being called from `engine.go`) |
| `NewState` | ❌ | Only called from within `pkg/indexer/service.go` |
| `LoadState` | ❌ | Only called from within `pkg/indexer/service.go` |

**Exact root cause found in `pkg/parser/go/analyzer.go`:**

The `go/doc` package automatically associates constructor/loader functions with the type they return:
- `NewService() *Service` → placed in `docPkg.Types["Service"].Funcs` by `go/doc`
- `LoadState() *State` → placed in `docPkg.Types["State"].Funcs` by `go/doc`
- `NewState() *State` → placed in `docPkg.Types["State"].Funcs` by `go/doc`
- `LoadIndexStatus() *IndexStatus` → placed in `docPkg.Types["IndexStatus"].Funcs` by `go/doc`

These functions **never appear** in `docPkg.Funcs` (top-level functions list).

In `AnalyzePackage` (lines 126–141), the type-processing loop iterates `typ.Methods` but **never `typ.Funcs`**:

```go
// pkg/parser/go/analyzer.go lines 126-141
for _, typ := range docPkg.Types {
    typeInfo := ca.analyzeTypeDecl(fset, typ, astFuncMap)
    typeIdx := len(info.Types)
    info.Types = append(info.Types, typeInfo)

    // ✅ Methods are processed
    for _, method := range typ.Methods {
        methodInfo := ca.analyzeFunctionDecl(fset, method, astFuncMap, typ.Name)
        info.Functions = append(info.Functions, methodInfo)
    }
    // ❌ typ.Funcs (constructors like NewService, LoadState) are NEVER processed!
}
```

`SaveIndexStatus` works because it returns `void` (no associated type), so `go/doc` places it in `docPkg.Funcs` — the only list that IS iterated at line 120.

### Fix (exact, minimal)

In `AnalyzePackage` in `pkg/parser/go/analyzer.go`, add iteration over `typ.Funcs` inside the type loop:

```diff
 for _, typ := range docPkg.Types {
     typeInfo := ca.analyzeTypeDecl(fset, typ, astFuncMap)
     typeIdx := len(info.Types)
     info.Types = append(info.Types, typeInfo)

     for _, method := range typ.Methods {
         methodInfo := ca.analyzeFunctionDecl(fset, method, astFuncMap, typ.Name)
         methodInfo.IsMethod = true
         methodInfo.Receiver = typ.Name
         info.Functions = append(info.Functions, methodInfo)
         info.Types[typeIdx].Methods = append(info.Types[typeIdx].Methods,
             ca.convertFunctionToMethodInfo(methodInfo, typ.Name))
     }
+
+    // Process constructor/factory functions associated with this type
+    // (go/doc moves New*, Load*, etc. here from the top-level Funcs list)
+    for _, fn := range typ.Funcs {
+        fnInfo := ca.analyzeFunctionDecl(fset, fn, astFuncMap)
+        info.Functions = append(info.Functions, fnInfo)
+    }
 }
```

### Note on tree-sitter

**tree-sitter is NOT needed** for this fix. The existing `go/ast` + `go/doc` approach is correct and more accurate than tree-sitter for Go — it's the standard library, built into the Go toolchain. The only problem is the missing `typ.Funcs` loop, which is a one-line fix.

---

*Last updated: 2026-03-09 — BUG-001 fixed, BUG-003 added*


# Optimization, Lifecycle & Cleanup Plan — 2026-10-15

**Context:** All critical, high, and medium correctness/security/parser/workspace findings from [`docs/plans/2026-10-14-review-findings.md`](./2026-10-14-review-findings.md) have been resolved and merged into `dev` (PR #50, `a234ec6`). This plan tracks the remaining **performance optimizations**, **daemon lifecycle improvements**, **search quality tuning**, and **code/CI hygiene** items.

---

## 1. Performance & Indexing Throughput Optimizations

### 1.1 Configurable `EmbedBatch` Size & Worker Pool (`pkg/indexer/service.go`, `internal/config/config.go`)
- **Current state:**
  - `pkg/indexer/service.go:552` hardcodes `const batchSize = 2`.
  - `pkg/indexer/service.go:541` hardcodes `numWorkers := 1`.
- **Why optimize:**
  - Sending only 2 symbols per `/api/embed` HTTP call adds unnecessary round-trip overhead, especially on GPU or fast multi-core setups where batch sizes of `8–32` significantly improve token throughput.
  - While CPU-only Ollama (`n_slots=1`) serializes embedding inference internally, having `1` worker blocks payload construction, `float64` → `float32` conversion, and Qdrant `Upsert` between batches.
- **Plan:**
  - Add `indexing.embed_batch_size` (default `8` or `16`, fallback to smaller sub-batches on timeout) and `indexing.embed_workers` (default `1` on CPU, configurable up to `4` for GPU/remote embedders) in [`internal/config/config.go`](../../internal/config/config.go) and `default.yaml`.
  - Pass these settings through `indexer.Options` into `IndexItems`.

### 1.2 Preserve Already-Indexed Collection States on Mid-Loop Fallback in `IndexFiles` (`internal/service/engine/engine.go`)
- **Current state:**
  - In [`internal/service/engine/engine.go:1048-1085`](../../internal/service/engine/engine.go#L1048-L1085), when a watcher batch contains files from multiple languages and one language's collection or state file does not exist yet (`!exists` at `:1060` or missing `statePath` at `:1064`), `IndexFiles` immediately returns `e.IndexWorkspace(ctx, root, false)` **before** saving the `states` map for collections already updated earlier in the loop.
- **Why optimize:**
  - Any files already embedded in earlier iterations of `IndexFiles` do not have their updated SHA256 hashes persisted to `state-*.json`, causing `IndexWorkspace` to re-embed them a second time.
- **Plan:**
  - Before delegating to `e.IndexWorkspace(ctx, root, false)` on lines `1061` / `1065` (or via a `defer` helper), flush all dirty `states` collected so far to disk.

### 1.3 Bounded Eviction & Batched Collection Check in `CleanupStaleFiles` (`internal/service/engine/engine.go`)
- **Current state:**
  - `staleCleanupCache` (`sync.Map` in [`internal/service/engine/engine.go`](../../internal/service/engine/engine.go)) never evicts old entries.
  - `CleanupStaleFiles` calls `CollectionExists` per stale file per language instead of caching existing collections per workspace run.
- **Plan:**
  - Deduplicate `CollectionExists` checks per `(workspace, collection)` within a cleanup pass and add TTL/size-based eviction to `staleCleanupCache`.

### 1.4 Cross-File Symbol & Package Cache During Single-Workspace Indexing (`pkg/parser/php`, `pkg/indexer`)
- **Current state:**
  - Framework enrichers (`laravel`, `wordpress`) and `IndexFile` parse each PHP file independently during sequential file indexing.
- **Why optimize:**
  - Caching `CollectionExists` per collection at the start of `IndexFiles` and avoiding redundant file reads when no framework enricher applies reduces disk I/O during large workspace indexing.

---

## 2. Daemon Lifecycle & Graceful Cancellation

### 2.1 Propagate Engine Shutdown Context to Background Indexing & Watcher Jobs (`internal/service/engine/engine.go`, `pkg/workspace/watch/watcher.go`)
- **Current state:**
  - Background indexing goroutines in [`internal/service/engine/engine.go:423, 999`](../../internal/service/engine/engine.go#L999) and watcher callbacks in [`pkg/workspace/watch/watcher.go:194`](../../pkg/workspace/watch/watcher.go#L194) create `context.Background()`.
- **Why optimize:**
  - When the daemon shuts down (`SIGINT`/`SIGTERM`, idle timeout, or `rag_apply_update`), in-flight indexing cannot be cancelled cleanly, so the periodic/final `state.Save` and `finalizeIndexStatus` may be interrupted mid-run.
- **Plan:**
  - Store a root `lifecycleCtx, cancel := context.WithCancel(context.Background())` on `Engine`, cancel it in `Engine.Close()`, and derive background indexing/watcher contexts from `e.lifecycleCtx`.
  - Ensure `IndexWorkspace` / `IndexFiles` save partial `state-*.json` progress on `context.Canceled` before exiting.

---

## 3. Hybrid & Semantic Search Quality Tuning

### 3.1 Adaptive / Configurable `defaultSemanticMinScore` (`internal/service/tools/smart_search.go`)
- **Current state:**
  - `defaultSemanticMinScore = 0.72` is hardcoded in [`internal/service/tools/smart_search.go`](../../internal/service/tools/smart_search.go).
- **Why optimize:**
  - Cosine similarity distributions vary across embedding models (`qwen3-embedding:0.6b` vs `nomic-embed-text` vs `mxbai-embed-large`) and query lengths.
- **Plan:**
  - Expose `search.min_semantic_score` in `config.yaml` (default `0.70`) and allow soft fallback to top-K hybrid results when all candidates score between `0.55` and `0.72`.

### 3.2 Hybrid Execution for Single-Word Queries in `isSymbolQuery` (`internal/service/tools/smart_search.go`)
- **Current state:**
  - `isSymbolQuery` (`smart_search.go:211`) treats any single plain word (e.g. `"authentication"`, `"caching"`, `"discounts"`) as an exact symbol lookup; if any symbol happens to match that name, semantic search is skipped completely.
- **Plan:**
  - Only treat a single-word query as a pure symbol lookup when it looks like a code identifier (`camelCase`, `PascalCase`, `snake_case`, `Class::method`, `pkg.Func`), or always blend semantic results alongside exact matches in `discovery` mode.

---

## 4. Parser, CI & Code Hygiene (Low Priority)

### 4.1 Standalone `.blade.php` & Extended Blade Directives (`pkg/parser/php/laravel/blade.go`, `pkg/parser/php/php_analyzer.go`)
- Allow [`pkg/parser/php/php_analyzer.go`](../../pkg/parser/php/php_analyzer.go) `CanHandle` to accept `.blade.php` directly and extract multiple directives per line plus `@includeIf`, `@includeWhen`, `@includeFirst`, multi-line `@props([...])`, and `<x-*>` Blade component tags in [`pkg/parser/php/laravel/blade.go`](../../pkg/parser/php/laravel/blade.go).

### 4.2 Windows CI Runner (`test.yml`)
- Add a `windows-latest` test matrix entry in [`.github/workflows/test.yml`](../../.github/workflows/test.yml) for `pkg/workspace/...`, `pkg/parser/...`, and `cmd/rag-code-install/...` to prevent Windows path regressions.

### 4.3 Dead Code & Script Cleanup
- Either register `rag_server_info` (`internal/service/tools/server_info.go`) in the MCP server or remove the unused file.
- Clean up obsolete `pkg/workspace/*/TASKS.md` files and fix the misleading log message `Tier 1 matched: using explicit path=<file path>` in `pkg/workspace/detector/detector.go` to log the resolved directory root instead of the input file path.
- Update `rebuild.sh` so local dev builds do not automatically increment the patch version in source files unless `--bump` is passed.


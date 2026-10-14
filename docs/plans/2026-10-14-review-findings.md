# Review & Audit Findings — 2026-10-14

Branch: `fix/nested-workspace-detection` (v2 line, PR #50 → `dev`, v2.1.110).
Context: `main` = v1 (1.1.21, legacy, `internal/ragcode/`); `dev` = v2 (daemon, `pkg/`). Do not compare/merge the two.
Status legend: `[ ]` open · `[x]` fixed · `[-]` won't fix / decided.

Rule of the session: **report → ask → wait for approval** before any edit/commit/push. Nothing below is fixed yet.

---

## 0. User requirements (from this session)

- Detection must **never climb to `$HOME`** (or drive root).
- We must **be sure we are inside the project**: force the IDE / its agent to tell us the root (explicit `workspace_root` / `file_path`, X-Workspace-Hint), instead of guessing by walking up.
- CPU-only Ollama in Docker is the baseline (no CUDA requirement). Host has an RTX 3050 Ti but GPU is optional.
- One embedding model: `qwen3-embedding:0.6b` (pinned in `internal/config/config.go:13`).
- Daemon must run with debug logging to surface more problems (`default.yaml: level: debug`, env `MCP_LOG_LEVEL=debug`).

## 1. Repo / branch state

- PR #50 `fix/nested-workspace-detection` → `dev`: 24 review threads all resolved; **CI red** (Test, Build, Lint). Local == origin.
- PR #59 `fix/installer-docker-output` → `main` (v1): CI green, no reviews. **Duplicate** of what the v2 branch already has in `cmd/rag-code-install/main.go:1084-1114`. Merging it to main later gives modify/delete conflicts with v2 (`cmd/install/main.go`, `docs/docker-setup.md` deleted on v2). Doc errors in PR 59: claims pinned tags but uses `:latest`; `nvidia/cuda:12.0-base` tag likely gone; Linux-only troubleshooting; no `-models-dir` hint for systemd Ollama. Decision: nothing to port to v2.
- Throughput plan (`docs/plans/2026-09-16-indexing-throughput.md`): T1 reactive backoff ✅; T2 `EmbedBatch` ✅ but batch size hardcoded **2** (`pkg/indexer/service.go:552`, plan says 16–32 configurable); T3 `numWorkers := 1` still hardcoded (`service.go:541`) ❌; T4 one-workspace-at-a-time ✅ via `indexingSlot` (`engine.go:45`).

## 2. CI failures (PR #50) — root causes

- [x] `cmd/rag-code-install/mcp_config_test.go:116` hardcodes `AppData/Claude` (Windows-only). On Linux `resolveIDEPaths` returns `~/.config/Claude` → dir missing → `TestAutoConfigurationUsesDetectedClientDirectories` fails. "Cannot configure Codex" log is a red herring (guarded at :129). Fix: create dirs from `resolveIDEPaths(home)` after Setenv, drop duplicate call at :122.
- [x] `pkg/parser/javascript/vue/analyzer.go:183-185` uses `filepath.Base` on a Windows path; on Linux `\` isn't a separator → name = whole path (`TestAnalyzer_ComponentName`). Fix: `filepath.Base(strings.ReplaceAll(p, "\\", "/"))` + `cases.Title(language.English, cases.NoLower)` so `MyWidget` stays `MyWidget`.
- [x] Windows-local only: `pkg/parser/php/laravel` tests hardcode `/tmp/...` (`ast_helper_test.go:21,65,96`, `controller_test.go:303`, `eloquent_test.go:77,213,244`, `integration_test.go:84,147`) → panic index out of range. Fix: `filepath.Join(t.TempDir(), ...)`.
- [x] `test.yml:61` `golangci-lint-action@v4` + `version: latest` breaks on Go 1.25 (`v1.64.8` built with Go 1.24). Fix: `golangci/golangci-lint-action@v6` + `version: v2.4`.
- [x] CI runs only on Ubuntu although there is Windows-specific code → added `windows-test` (`windows-latest`) job in `.github/workflows/test.yml`.

## 3. Workspace / path detection (PR #50 review, reproduced with scratch program)

- [x] **HIGH** Detector climbs past the project into `$HOME`. `pkg/workspace/detector/detector.go:196-212` `walkUp` keeps climbing to a lower tier up to MaxDepth 10; `.claude/.vscode/.cursor/.idea` are Tier-2 markers and exist in `~` → any project without `.git` (only `package.json`/`go.mod`) resolves to home. `Engine.IndexWorkspace` (`engine.go:1120`) then re-detects and indexes home; `pkg/indexer` has no invalid-root guard; `RegisterWorkspace(home)` error discarded at `resolver.go:97`. Fix: skip candidates where `watch.IsInvalidRoot(dir)`; never re-detect an already-confirmed root; re-check `IsInvalidRoot(wctx.Root)`. Consider dropping IDE dirs from markers entirely or only honoring them below a Tier-1/3 marker.
- [x] **HIGH** Confirmed `workspace_root` is not what gets indexed/registered. `engine.go:1120-1129` re-runs detection → different root (e.g. `mono` instead of `mono/packages/a`), indexes it, `RegisterWorkspace(mono)` → `absorbChildren` `RemoveAll`s children's `.ragcode`. Status/IDE rules written to confirmed root, index to another. Fix: `IndexWorkspace`/`IndexFiles` must resolve with `WorkspaceRoot` (as `DetectContextAsRoot`), never marker detection.
- [x] **HIGH** Exclude patterns matched as substrings of full path. `detector.go:352-361` `strings.Contains`; defaults `.git, build, dist, target, vendor, node_modules, .venv` → any file under `.github/`, any `x.github.io` repo, `~/src/distributed-x`, `~/buildkite` → "excluded from detection". Indexer uses exact dir-name match (`service.go:99-103`) — inconsistent. Fix: compare per path component.
- [x] **HIGH (Windows)** `IsInvalidRoot` case-sensitive. `pkg/workspace/watch/watcher.go:277-282`: `c:\users\scdoi` passes the guard in `index_workspace.go`, `Registry.Upsert`, `StartIndexingAsync`; `absorbChildren` lowercases → registering lowercase home absorbs **all** workspaces and `RemoveAll`s their `.ragcode`. Reachable via any tool call with `workspace_root` (`DetectFromParams` → `DetectContextAsRoot(..., true)` auto-indexes without confirm). Fix: `strings.EqualFold` on Windows, `filepath.Clean(os.TempDir())`.
- [x] **MEDIUM** Confirm gate bypass. `internal/service/tools/index_workspace.go:213` calls `DetectContextAsRoot(ctx, root, confirm)`; with `confirm=true` indexing starts before `verifyConfirmationToken` (:232). Fix: pass `false`.
- [x] **MEDIUM** "Prefer highest valid root within a tier" not implemented. `detector.go:203` `tier < bestTier` → nearest wins; Tier-1 breaks early (submodule beats parent). `ensureAllowed` error aborts the whole walk (`:198-199, 270`) instead of skipping → ancestor with `AGENTS.md` outside AllowedRoots makes a valid `go.mod` project fail OUTSIDE_ALLOWED_ROOTS (regression). Fixed (`tier <= bestTier` without early Tier-1 break; `ensureAllowed` skips disallowed ancestors when a valid child exists; `TestDetectSameTierPrefersOutermostRoot`, `TestDetectAllowedRootBoundaryDoesNotAbortValidChild`).
- [x] **MEDIUM** Nested `.ragcode` suppression cache: `State.Save` (`state.go:65`) now checks `cachedHasParentRagcode(workspaceRoot)` before creating a new `.ragcode` directory (`TestStateSave_BlocksCreatingChildRagcodeWhenParentHasRagcode`).
- [x] **MEDIUM** `absorbChildren` deletes entire child `.ragcode` incl. user-authored `.ragcode/root` pointer; Fixed in `registry.go:363` (`removeRagcodeOwnedFiles` removes only `index_status.json` and `state-*.json`, preserving user files like `.ragcode/root`; `TestRegistryCleanupAbsorbedPreservesUserFiles`).
- [x] **HIGH** `PromoteCandidate` (`registry.go:200`) lacks `IsInvalidRoot` guard that `Upsert` has (:141), and `Resolver.finalize` (`resolver.go:351`) lacked `IsInvalidRoot` guard when `RootValidator == nil`. Fixed in both `PromoteCandidate` and `Resolver.finalize` (`TestRegistryRefusesHomeRoot`, `TestResolveWorkspaceRootRefusesInvalidRoot`).
- [x] **HIGH** Registry `save()` (`registry.go:490`) plain `os.WriteFile`, no temp+rename. Fixed: `save()` now writes atomically via `os.CreateTemp` + `os.Rename`.
- [x] **LOW** Audit sink called under lock in `PromoteCandidate` (:248) and `RecordFeedback` (:536). Fixed: `audit.Record` moved outside `r.mu.Lock()`.
- [x] **MEDIUM** `read_file_context.go:89` `filepath.Abs(filePath)` resolves relative paths against daemon CWD, not `wctx.Root`. Fixed: resolves relative `file_path` against `wctx.Root` when the file exists on disk before falling back to `filepath.Abs`.
- [x] **MEDIUM** `pkg/indexer/stale.go:22` `FindDeletedRoot` loops forever on Windows for missing drive/share (`filepath.Dir("X:\\") == "X:\\"`); comparison case-sensitive. Fixed: stop when `filepath.Dir(dirClean) == dirClean` and use `strings.EqualFold` on Windows.
- [x] **LOW** `detector.go:341` AllowedRoots check case-sensitive on Windows. Fixed: `ensureAllowed` uses `strings.EqualFold` / `strings.ToLower` on Windows.
- [x] Added tests: `cleanupAbsorbed`, detector vs home with `.claude`, `.github` paths, same-tier nesting, AllowedRoots boundary, `IsInvalidRoot` case-insensitivity, `Resolver.finalize` invalid root refusal.

### Current detection cascade (engine.go:186-407) — for the "force IDE to tell us" redesign
1. `file_path` param → `DetectContext` → marker walk-up.
2. `workspace_root` param → `DetectContextAsRoot(..., triggerAutoIndex=true)` (trusted root, confidence 1.0, **auto-indexes without confirm**).
3. empty → X-Workspace-Hint header (adapter CWD) → marker walk-up.
4. empty → registry "last active workspace".
Adapter caches `X-Resolved-Workspace` as sticky `X-Workspace-Root`.
Idea to evaluate: make (3)/(4) non-indexing (resolve-only or error "pass workspace_root"), require explicit root for anything that indexes/registers, and let installer put `${workspaceFolder}` into mcp.json env (`RAGCODE_WORKSPACE`) where the IDE supports it (Cursor/VS Code do).

## 4. Security / deps (whole-project audit)

- [x] **CRITICAL** `go.mod:11` `modelcontextprotocol/go-sdk v1.4.0` — GO-2026-4773 (Cross-Site Tool Execution on HTTP servers w/o auth) + GO-2026-4770. Daemon serves `/mcp` on 127.0.0.1 unauthenticated; tools can index, install skills, apply updates. Fixed: upgraded `github.com/modelcontextprotocol/go-sdk` to `v1.8.0`.
- [x] `govulncheck` & updater checksum: upgraded `golang.org/x/text` `v0.37.0` → `v0.39.0` (GO-2026-5970), `golang.org/x/net` `v0.53.0` → `v0.56.0`, `golang.org/x/crypto` `v0.51.0` → `v0.53.0`, `google.golang.org/grpc` `v1.81.1` → `v1.83.1`, `github.com/fsnotify/fsnotify` `v1.7.0` → `v1.10.1` (all compatible with `go 1.25.0`); `updater.go:306` now compares exact asset filename (`strings.TrimPrefix(parts[1], "*") == assetName`) instead of `strings.Contains`.
- [x] `/debug/pprof/*` mounted unauthenticated outside SDK DNS-rebinding protection (`internal/daemon/run.go:223`). Fixed: gated behind `cfg.Logging.Level == "debug"` or `RAGCODE_ENABLE_PPROF=1`.
- [x] `attemptOllamaRestart` (`pkg/indexer/service.go:315-325`) runs `systemctl restart ollama`, `pkill -9 -f "ollama serve"`, spawns `ollama serve` — kills user's Ollama, useless on Windows/Docker. Fixed: gated behind `RAGCODE_ALLOW_OLLAMA_RESTART=1` (defaults to heartbeat polling only).
- [x] Indexing goroutines use `context.Background()` (`engine.go:999, 423`, `watcher.go:194`) → shutdown/auto-update (`os.Exit`) can't cancel; state not saved. Fixed: `Engine.lifecycleCtx` and `FileWatcher.lifecycleCtx` cancelled on `Close()` / `Stop()`.
- [x] Upgraded `github.com/odvcencio/gotreesitter` (`v0.6.0` → `v0.56.0`) and `github.com/fsnotify/fsnotify` (`v1.7.0` → `v1.10.1`).

## 5. Embedding / indexing

- [x] **v2 has no embed-text length guard** (v1 fixed issue #53 in `internal/ragcode/indexer.go` on `main`). `pkg/llm/ollama.go:173-190`, `pkg/indexer/service.go:677`. Fixed: `IndexItems` now caps oversized symbol embed texts at 30,000 runes (UTF-8 rune-safe).
- [x] EmbedBatch size hardcoded 2 (`service.go:552`); numWorkers hardcoded 1 (`:541`). Fixed: configurable via `embed_batch_size` / `embed_workers` (`SetEmbedBatchSize`, `SetEmbedWorkers`).
- [x] `IndexFiles` returns mid-loop into `IndexWorkspace` (`engine.go ~1058/1062`) → states for other collections never saved → re-embedding next run (waste, no data loss). Fixed: `saveLoadedStates()` flushes dirty collection states before falling back to `IndexWorkspace`.
- [x] `defaultSemanticMinScore` fixed 0.72 (`smart_search.go`) — model-dependent. Fixed: added soft fallback (`softSemanticMinScore = 0.55`, top 3 results) when `0.72` filters out all candidates.
- [x] `isSymbolQuery` (`smart_search.go:211`) routes any single plain word to exact lookup; if it matches, semantic search is skipped. Fixed: `isStrictIdentifierQuery` + `blendExactAndSemantic` runs both exact and semantic search for plain lowercase single-word queries in `discovery` mode.
- [x] `DeleteByPrefix` (`pkg/storage/qdrant.go:441`) sets next offset = last ID (inclusive → duplicate), scans whole collection per deleted dir. Fixed: skips the inclusive `offset` point on subsequent scroll pages and stops when no new points beyond `offset` are returned.
- [x] `staleCleanupCache` (`engine.go`, package-level sync.Map) never evicts; `CleanupStaleFiles` calls `CollectionExists` per file per language. Fixed: per-run `CollectionExists` cache map + `staleCleanupCacheMax = 2000` bound.

## 6. Language parsers (verified via `ragcode-parser-audit` workflow `wf_891e54f5-020`)

### 6.1 Go & Python (`pkg/parser/go`, `pkg/parser/python`)
- [x] **CRITICAL** `pkg/parser/python/extract.go:620,453`: regex `funcRe` silently drops functions/methods with multi-line signatures, call-defaults (`dict()`), return hints containing spaces (`Tuple[int, str]`, `dict[str, Any]`), or non-ASCII names. Fixed in `extract.go` (`parseDefHeader` + `advancePastParensAndTripleQuotes`).
- [x] **HIGH** `pkg/parser/go/analyzer.go:194`: typed `const`/`var` groups (`type Color int; const ( Red Color = iota ... )`) are filed by `go/doc` under `typ.Consts`/`typ.Vars`, which `analyzeFiles` never iterates → standard Go iota enums are missing from the index. Fixed.
- [x] **HIGH** `pkg/parser/go/analyzer.go:237`: `buildFunctionASTMap` keys bodies by bare name instead of `fn.Pos()`. Fixed: keyed by `token.Pos` (`fn.Pos()` / `fn.Decl.Pos()`).
- [x] **HIGH** `pkg/parser/go/analyzer.go:223,639`: `buildFunctionASTMap` generic receivers (`*Stack[T]`) & `typeToString` returning `"unknown"` for `IndexExpr`, `FuncType`, `Ellipsis`, `ChanType`. Fixed via `token.Pos` map and `types.ExprString`.
- [x] **HIGH** `pkg/parser/python/extract.go:135,454`: `parseAndCollect` splits on `"\n"` only; on CRLF files `decoratorRe` never matches `@property\r`, `@staticmethod\r`, `@classmethod\r`. Fixed: normalizes `\r\n` → `\n` at top of `parseAndCollect`.
- [x] **MEDIUM** `pkg/parser/go/analyzer.go:179`: `doc.AllMethods` includes promoted methods from embedded structs (`method.Level > 0`) in `typ.Methods`. Fixed: skips `method.Level > 0`.
- [x] **MEDIUM** `pkg/parser/python/extract.go:453,806`: `extractMethods` treats any indented `def` inside a class (nested functions, nested-class methods) as a class method; `findBlockEnd` treats column-0 lines inside triple-quoted strings as the end of the class/method. Fixed.
- [x] **MEDIUM** `pkg/parser/go/analyzer.go:464,502`: every `const`/`var` in a grouped `const (...)` / `var (...)` block gets the whole `GenDecl`'s `StartLine`/`EndLine` instead of its own `*ast.ValueSpec` span. Fixed.

### 6.2 JavaScript / TypeScript / Vue (`pkg/parser/javascript`)
- [x] **CRITICAL** `go.mod:12`: pinned `github.com/odvcencio/gotreesitter v0.6.0` collapses the TypeScript AST. Fixed: upgraded `gotreesitter` to `v0.56.0`.
- [x] **HIGH** `pkg/parser/javascript/treesitter.go:327`: anonymous default exports (`export default function () {}`, `export default () => ...`, `export default class {}`) fall through `processExportStatement` and produce 0 symbols. Fixed (`fileStem(filePath)`).
- [x] **HIGH** `pkg/parser/javascript/treesitter.go:635,666`: `extractTypeAlias` overwrites the alias name with the second `type_identifier` and matches `"type"` keyword as `Definition`; `extractEnum` skips `enum_assignment`. Fixed.
- [x] **MEDIUM** `pkg/parser/javascript/vue/analyzer.go:180-224`: filename fallback on Windows paths, `cases.Title` lowercasing, `reComponentName` matching `name:` inside `data()`, builtin composables emitted as exported functions without `IsCustom` filter, and SFC symbols `StartLine`/`EndLine`. Fixed.
- [x] **MEDIUM** `pkg/parser/javascript/treesitter.go:496`: `jsDocBefore` scans back past a plain `/* */` comment to an earlier `/** */` comment. Fixed.
- [x] **MEDIUM** `pkg/parser/javascript/treesitter.go:153,231,558`: tree-sitter path never calls `jsDocBefore` for classes, class methods, or interfaces. Fixed.
- [x] **MEDIUM** `pkg/parser/javascript/treesitter.go:84,349`: `abstract_class_declaration` (`export abstract class BaseRepo<T>`) is not handled in top-level/export switches. Fixed.
- [x] **MEDIUM** `pkg/parser/javascript/treesitter.go:467`: any `const`/`let` whose text contains `"=>"` or `"function"` is indexed as a `Function` symbol. Fixed via AST check on `variable_declarator` value node.

### 6.3 PHP / Laravel / WordPress (`pkg/parser/php`)
- [x] **HIGH** `pkg/parser/php/analyzer.go:517,577,997-1051`: PHP interfaces and traits (and their methods/properties) are emitted with `FilePath=""`, `StartLine=0`, `EndLine=0`, `Docstring=""` → `symbolsForFile` in `pkg/indexer/service.go:433` drops every one of them before embedding. Trait property signature also formats `$$prop` (double `$` at `:1048`). Fixed.
- [x] **HIGH** `pkg/parser/php/analyzer.go:186`: no `StmtEnum` / `StmtEnumCase` visitor on `symbolCollector` → PHP 8.1 enums produce 0 symbols. Fixed.
- [x] **HIGH** `pkg/parser/php/analyzer.go:204`: no `StmtGroupUse` visitor → group imports `use A\B\{X, Y as Z};` are ignored, and `laravel/eloquent.go:225` resolves related models to the wrong FQN. Fixed.
- [x] **MEDIUM** `pkg/parser/php/wordpress/hooks.go:40-50,149`: `walkExpr` in `php/analyzer.go:1161` never populates `MethodCall.Args`, so `extractHooksFromCalls` emits phantom `action()` hooks with empty name/callback and lines `0-0`. Fixed (including `*ast.ScalarEncapsed` and `*ast.ExprBinaryConcat` support).
- [x] **MEDIUM** `pkg/parser/php/laravel/routes.go:97,239` & `adapter.go:135`: fluent group chains (`Route::middleware(...)->prefix(...)->group(...)`) and `Route::apiResource` are ignored; `findRouteFiles` appends route files twice so every route chunk is duplicated. Fixed.
- [x] **MEDIUM** `pkg/parser/php/analyzer.go:272,343,818`: PHPDoc is lost on `final`/`abstract` classes and PHP 8 `#[Attribute]` methods; `extractTypeName` drops `*ast.Union` (`A|B`) and `*ast.Intersection` (`A&B`). Fixed.
- [x] **LOW** `pkg/parser/php/analyzer.go:1238`: any class extending bare `Controller`/`Model`/`Authenticatable` (e.g. Yii/Symfony) marks the project as Laravel regardless of imports/filesystem. Fixed (resolves `Extends` via `Imports` first).
- [x] **LOW** `pkg/parser/php/laravel/blade.go:62` & `php_analyzer.go:54`: standalone `.blade.php` outside Laravel yields 0 symbols; inside Laravel, only first directive per line is matched and `@includeIf`/`@includeWhen`/`<x-*>`/multi-line `@props` are missed. Fixed.

### 6.4 HTML / CSS / Docs / GoTemplate (`pkg/parser/html`, `css`, `docs`)
- [x] **CRITICAL** `pkg/parser/html/analyzer.go:254`: `buildElements` panics (`index out of range [0]`) on `<div class="">` (or whitespace-only `class`) with no `id`. Fixed.
- [x] **HIGH** `pkg/parser/docs/treesitter.go:61`: YAML tree-sitter grammar returns a truncated error tree (`root.HasError() == true`) on common files (`docker-compose.yml`, GitHub Actions workflows), dropping most of the file and using hidden rule names (`_r_blk_map_itm`) as signatures. Fixed (`fallbackLineChunks`).
- [x] **HIGH** `pkg/parser/html/gotemplate/analyzer.go:16-22`: `reRange`/`reWith` require `\.[\w.]+` and `reTemplate`/`reBlock` only accept optional `(\.[\w.]*)?`. Fixed.
- [x] **MEDIUM** `pkg/parser/html/gotemplate/analyzer.go:183`: only one `{{end}}` (and one opener per regex) is processed per line, so inline blocks on a single line desynchronize the stack. Fixed (`reAction.FindAllString` left-to-right).
- [x] **MEDIUM** `pkg/parser/css/analyzer.go:65-128`: SCSS/LESS `//` comments containing `{` and stray `}` break `braceDepth` for the rest of the file; top-level `@charset`/`@import`/`$vars` get glued into the first rule's selector; `startLine` is set at the previous `}` instead of the selector start. Fixed.
- [x] **MEDIUM** `pkg/parser/docs/treesitter.go:81,90`: nodes `< 10` bytes are dropped (losing short JSON pairs like `"k0":0`), larger objects fan out to one embedding per key/value pair, and `source[start:start+8192]` slices raw bytes without backing up to `utf8.RuneStart`. Fixed (`sliceUTF8Safe` + sibling coalescing).
- [x] **MEDIUM** `pkg/parser/html/analyzer.go:194`: `goquery.Text()` includes `<script>` and `<style>` source inside section/body prose chunks. Fixed (`doc.Find("script, style, noscript").Remove()`).
- [x] **MEDIUM** `pkg/parser/docs/markdown.go:43`: `TableOpen`, indented `CodeBlock`, and `HTMLBlock` tokens are ignored by the markdown chunker. Fixed.

### 6.5 Indexing pipeline (`pkg/indexer`, `pkg/parser/generic`, `internal/service/engine`)
- [x] **HIGH** `pkg/indexer/service.go:413,649`: `IndexFile` calls `DeleteByFilter(file_path)` before embedding, and `IndexItems` returns on the first batch error (`:649`) before any `Upsert` → one failed/timed-out batch wipes the entire file from Qdrant. Fixed: `IndexItems` upserts all valid points even when a batch fails.
- [x] **MEDIUM** `pkg/indexer/service.go:99,777`: `exclude_patterns` documented as globs in `config.go:192`, matched as exact dir-name equality in indexer/counter (`matchesExcludePatterns` now supports dir names, relative paths, trailing slashes, and globs).
- [x] **MEDIUM** `pkg/indexer/minified.go:76,113`: `exceedsLineDensity` (>500 chars/line, no minimum size or extension check) classifies compact single-line JSON/YAML files (e.g. 600-byte `openapi.json`) as minified and excludes/deletes them. Fixed: `.json`/`.yaml`/`.yml` under 4 KiB are not classified as minified (`TestMinified_DoesNotSkipSmallCompactJSON`).
- [x] **LOW** `pkg/indexer/service.go:106,784`: `WalkDir` calls `shouldSkipFile` (which opens and reads up to 256 KiB via `exceedsLineDensity`) on every file before checking `parser.GetByFile`. Fixed: `parser.GetByFile` and language filter run before `shouldSkipFile`.
- [x] **LOW** `internal/service/engine/engine_fallback_search.go:239`: `FallbackDirectSearch` hashes `file:name:start:end` (without `QualifiedName`), whereas `pkg/indexer/service.go:701` includes `QualifiedName` → duplicate results when `smart_search` merges vector + fallback hits. Fixed.
- [x] **LOW** `pkg/parser/generic/analyzer.go:44`: `.env` is indexed, but `.env.local` / `.env.example` / `.env.production` are skipped because `filepath.Ext` returns `.local` / `.example`. Fixed.

## 7. Docs / hygiene / CI

- [x] `server.json:42`, `llms.txt:63`, `llms-full.txt:837-934`, `cmd/mcp-http-client-test/main.go:165` list non-existent tools: `rag_search_code`, `rag_get_function_details`, `rag_find_type_definition`, `rag_find_implementations`, `rag_search_docs`, `rag_get_code_context`. `doc_index_workspace.md`, `llms-full.txt:732` omit required `confirmation_token`. Regenerated and updated with full language/framework support (Go/GoTemplates, PHP/Laravel/Blade/WordPress/WooCommerce/Oxygen Builder, JS/TS/React/Vue, Python, HTML/CSS/SCSS, Docs).
- [x] `server.json` version stuck at 2.1.26 (code 2.1.110): `release.yml` pushes to main with `|| echo "Failed to push"`. Fixed: bumped `server.json` to `2.1.110` and removed `|| echo "Failed to push"` in `release.yml`.
- [x] `.gitignore:17` `rag-code-mcp` unanchored → ignores any new file in `cmd/rag-code-mcp/`; `:12` `test-*` too broad. Fixed: anchored `/rag-code-mcp` and `/test-*`, removed duplicate `.trello.json`.
- [x] `build-binaries.yml` force-adds ignored `bin/`, auto-PRs binaries, fails on fork PRs. Fixed: removed `.github/workflows/build-binaries.yml` (goreleaser ships binaries).
- [x] `rebuild.sh` bumps patch version on every build (→ 2.1.110), Unix-only. Fixed: only bumps when `--bump` is passed.
- [x] Dead code: stale comment `service.go:257` updated; `pkg/workspace/*/TASKS.md` removed.

## 8. Live debug session (2026-10-14)

- Started containers: `ragcode-qdrant` (qdrant/qdrant:latest, :6333/:6334, vol `ragcode-qdrant-data`), `ragcode-ollama` CPU (ollama/ollama:latest, :11434, vol `ragcode-ollama-models`). Pulling `qwen3-embedding:0.6b`.
- Built daemon to scratchpad: `%TEMP%\claude\...\scratchpad\run\rag-code-mcp.exe` (not via `rebuild.sh`).
- Next: run daemon with `MCP_LOG_LEVEL=debug`, index this repo via MCP, watch `[WS-DETECT]` logs for: root landing in home, `.github` excluded, batch sizes, Ollama context errors. Record observations here.

### 8.1 Live run results (23:13–23:16, daemon v2.1.110 built from HEAD)
Setup: scratch copy of `default.yaml` with `auto_update: false` (otherwise the daemon may self-update + `os.Exit` mid-debug) and `auto_create_ide_rules: false`. Called `/mcp` via curl (initialize → tools/call). Data dir on Windows is `%LOCALAPPDATA%\ragcode\` (logs, registry), not `~/.ragcode` — but the daemon still creates an empty `~/.ragcode`.
- [x] **Confirmed: confirm-gate bypass (§3 MEDIUM).** `rag_index_workspace` with `confirm:true, confirmation_token:"WRONG"` → response "Invalid confirmation_token", but log shows `DetectContextAsRoot ... triggerAutoIndex=true` → `Auto-triggering incremental index` → `[IDX] Starting background indexing`. Indexing started anyway. Upgrade to **HIGH** (wrong token still indexes + registers).
- [x] **Confirmed: IndexWorkspace re-detects (§3 HIGH #2).** After `StartIndexingAsync`, log shows `DetectContext called: path=<root>` → `Resolver override: explicit_file_path → file_path`. Same root here only because the repo has `.git`; mechanism is there.
- [x] **Confirmed: `.github` paths fail detection (§3 HIGH #3).** `rag_read_file_context` with `file_path=.github/workflows/test.yml` → `Resolver override: explicit_file_path → registry_fallback`, response warning "Workspace root was inferred from history". Right root only because this is the only registered workspace; with another last-active workspace it resolves to the wrong project.
- [x] New, LOW: log line `Tier 1 matched: using explicit path=<file path>` prints the file as if it were the root — misleading when debugging. Fixed (`using explicit file_path=%s`).
- Root never landed in home for the explicit-root path. Home-climb (`file_path` in a project without `.git`) and lowercase-home tests were **not** run live: both would register/index `C:\Users\scdoi` and `absorbChildren` → destructive. Cover with unit tests instead (§8a step 2).
- **Embedding throughput is the real blocker for the CPU baseline.** `qwen3-embedding:0.6b` in Docker CPU (n_threads=10, n_ctx=4096, ~990% CPU): 24 embeds / 6353 tokens in ~105 s ≈ **60 tokens/s**; single `/api/embed` calls (batch 2) take 2–15 s. `main.go` (5 symbols) took 32 s; 2/268 Go files after ~45 s → full repo ETA is hours. Ollama is the bottleneck (GIN timings), not the daemon; batch=2/workers=1 (§5) doesn't matter while `n_slots=1`. To investigate: Docker Desktop/WSL CPU limits, host power mode, native Windows Ollama vs Docker, optional GPU (RTX 3050 Ti idle, 0 MiB used).
- No embed-length failures: largest input 801 tokens, `truncated = 0`. Ollama `n_ctx=4096` and the client doesn't set `truncate:false`, so oversize input gets **silently truncated** rather than failing the batch → §5 length guard is a quality issue (lost tail of big symbols), not a crash. Lower to MEDIUM.
- 0 ERROR/WARN lines in the daemon log during the run. File counts: go 268, docs 72, html 4, generic 3.
- [x] Cleanup after the run completed: removed `<repo>/.ragcode/`, `%LOCALAPPDATA%\ragcode\registry.json`, empty `~/.ragcode`, and Qdrant collection `ragcode-a7faab9632799ef4e4a40c533966541431368f92-go`.

### 8.2 Step 2 (§3 HIGH fixes) — done in working tree, uncommitted
- [x] `watch.IsInvalidRoot`: case-insensitive on Windows (`samePath` helper), temp compared cleaned. Test `TestIsInvalidRoot`.
- [x] `detector.walkUp`: stops at home/temp/volume root (`watch.IsInvalidRoot`), so `~/.claude` etc. can no longer make home the workspace; `.ragcode/root` metadata pointing to an invalid root is refused. Test `TestDetectStopsBelowHome`.
- [x] `detector.shouldExclude`: per path component (case-insensitive on Windows) instead of `strings.Contains`. Test `TestDetectExcludeMatchesComponents` (`.github`, `buildkite`, `node_modules`).
- [x] `engine.IndexWorkspace` / `IndexFiles`: resolve the given root via `DetectContextAsRoot(ctx, root, false)` (no marker re-detection) + `IsInvalidRoot` re-check.
- [x] `index_workspace.go`: `DetectContextAsRoot(..., false)`; indexing only after token verification. No unit test (tool needs a real engine; `NewIndexWorkspaceTool(nil)` only covers the home guard).
- `go build ./...`, `go vet` OK; `go test ./pkg/workspace/... ./internal/service/...` all pass.

### 8.3 New finding from step 2
- [x] **HIGH** `pkg/indexer/index_status.go:79` `hasParentRagcode` walks up 10 parents and treats any `.ragcode` dir without `bin/` as a parent workspace → `SaveIndexStatus` refuses to create `.ragcode/index_status.json`. `~/.ragcode` (the default data dir on Linux/macOS, and created empty by the daemon on Windows — observed in 8.1) is therefore a "parent" of **every** workspace under home → no index_status for any of them. Fixed: `hasParentRagcode` stops at `watch.IsInvalidRoot(dir)` (`TestSaveIndexStatusIgnoresHomeRagcode`), and `internal/daemon/run.go:255` now uses `utils.RagCodeHome()` instead of creating `~/.ragcode` on Windows.

## 8a. HANDOFF — how to continue in a new chat (smaller model)

Paste this to the new session: *"Read docs/plans/2026-10-14-review-findings.md and continue from §8a. Rule: report → ask → wait for approval before any edit/commit/push. No Co-Authored-By trailers."*

State at handoff:
- Nothing in the repo was modified except this file (untracked). Branch `fix/nested-workspace-detection` == origin.
- Containers running: `ragcode-qdrant` (:6333/:6334), `ragcode-ollama` CPU (:11434). Check model: `docker exec ragcode-ollama ollama list` (expects `qwen3-embedding:0.6b`; if missing: `docker exec ragcode-ollama ollama pull qwen3-embedding:0.6b`).
- Daemon binary built at `%LOCALAPPDATA%\Temp\claude\c--Users-scdoi-go-src-github-com-doITmagic-rag-code-mcp\359ea173-b686-4c6d-ba2e-d427de4ca291\scratchpad\run\rag-code-mcp.exe` (may be gone; rebuild with `go build -o <scratch>/rag-code-mcp.exe ./cmd/rag-code-mcp`, NOT `rebuild.sh`).
- Parser-audit workflow (`wf_891e54f5-020`) may not have finished; its journal is at `%USERPROFILE%\.claude\projects\c--Users-scdoi-go-src-github-com-doITmagic-rag-code-mcp\359ea173-b686-4c6d-ba2e-d427de4ca291\subagents\workflows\wf_891e54f5-020\journal.jsonl`. If unreadable, re-run a smaller check: `go test ./pkg/parser/...` and look at the two known parser bugs in §2.

Next steps, in order (each needs user approval before editing):
1. **Live debug** (no code change): run daemon `MCP_LOG_LEVEL=debug <bin> --daemon --config internal/config/default.yaml` (verify flags in `cmd/rag-code-mcp/main.go:26-34`), call `rag_index_workspace` with `workspace_root=<this repo>` + confirm flow via `cmd/mcp-http-client-test` or curl to `http://127.0.0.1:39000/mcp`. Watch `[WS-DETECT]` lines: root must be this repo, never `C:\Users\scdoi`; check `.github/` files not "excluded from detection"; note embed batch sizes and any Ollama context-length errors. Write observations in §8.
2. **Fix §3 HIGH detection bugs** (smallest diffs): 
   - `detector.go` walkUp: skip dir if `watch.IsInvalidRoot(dir)`; compare excludes per path component instead of `strings.Contains`.
   - `watcher.go:277` `IsInvalidRoot`: `strings.EqualFold` on Windows + `filepath.Clean`.
   - `engine.go:1120` `IndexWorkspace`: resolve given root with `WorkspaceRoot` (no re-detection), re-check `IsInvalidRoot`.
   - `index_workspace.go:213`: pass `false` for triggerAutoIndex.
   Add tests: home-with-.claude, `.github` path, lowercase home, wrong-token-no-index.
3. **Fix §2 CI** (3 test files) → push → PR #50 green.
4. **Redesign "IDE must tell us the root"**: make X-Workspace-Hint/registry fallbacks resolve-only (no auto-index/register); error message tells agent to pass `workspace_root`; installer writes `RAGCODE_WORKSPACE=${workspaceFolder}` in mcp.json where IDE supports it. Discuss with user first.
5. Then §4 (go-sdk bump), §5, §7.

## 9. Proposed order of fixes (pending approval)
1. §3 four HIGH detection bugs + confirm-gate bypass (data-safety first).
2. §2 CI fixes (3 small test fixes) so PR #50 goes green.
3. §4 go-sdk bump + govulncheck deps.
4. §5 embed length guard + batch size config.
5. §7 docs/server.json regeneration.

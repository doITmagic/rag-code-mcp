# Harta Arhitecturii rag-code-mcp

## pkg/workspace/ — Detecție și rezolvare workspace

### contract/types.go
- `ResolveWorkspaceRequest` — input: `FilePath`, `WorkspaceRoot`, `Workspace`, `Roots[]`
- `ResolveWorkspaceResponse` — output: `ResolvedRoot`, `WorkspaceID`, `MismatchRisk`, `MarkersFound[]`, `Branch`, `HeadSHA`
- `WorkspaceCandidate` — candidat intern: `Root`, `Markers[]`, `Reason`, `Confidence`
- `DeriveWorkspaceID(root, branch, worktree)` — generează ID-ul unic al workspace-ului
- `DerivePathContextKey(root, branch, head, worktree)` — cheie pentru invalidare cache

### contract/workspace_id.go
- `DeriveWorkspaceID()` și `DerivePathContextKey()` — implementare

### detector/detector.go
- `Detector.DetectFromFilePath(ctx, filePath)` → `*WorkspaceCandidate`
  - Urcă în directoare până găsește markeri (go.mod, composer.json, .git, etc.)
  - Returnează `Markers[]` — lista markerilor găsiți (IMPORTANT: din markeri deducem limbajul)
  - `DefaultOptions().Markers` — lista completă de markeri suportați

### resolver/resolver.go
- `Resolver.Resolve(ctx, ResolveWorkspaceRequest)` → `*ResolveWorkspaceResponse`
  - Cascadă: workspace_root → file_path (Detector) → alias (Registry) → roots list
  - Apelează BranchAnnotator pentru git metadata
  - Setează `MismatchRisk` (low/medium/high)

### registry/registry.go
- Persistă workspace-uri confirmate pe disk (JSON)
- `ResolveAlias()`, `RecordFeedback()`, `PromoteCandidate()`

### branchstate/state.go
- `Manager.CompareAndUpdate(ctx, root)` → branch, HEAD, worktreeID
- Detectează schimbări de branch (trigger pentru reindexare)

---

## pkg/parser/ — Parsare cod sursă

### parser.go
- Registry global de analyzers (map[name]Analyzer)
- `Register(Analyzer)` — înregistrează un analyzer
- `GetByFile(filePath) Analyzer` — returnează analyzer-ul potrivit după extensie (CanHandle)
- `GetByName(name) Analyzer` — returnează analyzer după nume ("go", "php", etc.)
- `Analyzer` interface: `Name() string`, `CanHandle(filePath) bool`, `Analyze(ctx, path) (*Result, error)`
- `Symbol` struct: Name, Type, Package, Content, Signature, Docstring, StartLine, EndLine, FilePath, Language
- `Result` struct: `Symbols[]`, `Language string`

### go/analyzer.go
- Analyzer pentru Go, se înregistrează cu `Name()="go"`, `CanHandle(".go")`

### php/analyzer.go + php/laravel/
- Analyzer pentru PHP + Laravel (Eloquent, Controllers, Routes)

### python/analyzer.go
- Analyzer pentru Python

### html/analyzer.go
- Analyzer pentru HTML

### generic/analyzer.go
- Fallback pentru limbaje fără analyzer specific

---

## pkg/storage/ — Stocare vectori

### interface.go
- `VectorStore` interface:
  - `Upsert(ctx, collection, []Point)`
  - `Search(ctx, collection, SearchQuery)` → `[]SearchResult`
  - `SearchCodeOnly(ctx, collection, SearchQuery)` → `[]SearchResult` (filtrează chunk_type=code)
  - `SearchDocsOnly(ctx, collection, SearchQuery)` → `[]SearchResult` (filtrează chunk_type=doc)
  - `SearchByChunkType(ctx, collection, SearchQuery, chunkType)`
  - `CollectionExists(ctx, name)` → bool
  - `CreateCollection(ctx, name, dimension)`
  - `GetCollectionInfo(ctx, name)` → `*CollectionInfo`
- `Point` struct: ID, Vector[]float32, Payload map[string]any
- `SearchResult` struct: Point, Score float32
- `SearchQuery` struct: Vector, Limit, Filter, MinScore

### qdrant.go
- `NewQdrantStore(host, port, tls, apiKey)` → `VectorStore`
- Implementare Qdrant pentru toate metodele din interface

---

## pkg/llm/ — Embedding și LLM

### provider.go
- `Provider` interface: `Embed(ctx, text)` → `[]float64`, `GetEmbeddingDimension()` → uint64

### ollama.go
- `OllamaProvider` — implementare cu langchaingo/ollama

---

## internal/service/ — Servicii interne

### engine/engine.go
- `Engine` struct: indexer, search, detector, branchManager
- `NewEngine(indexer, search)` — constructor
- `WorkspaceContext` struct: Root, ID, Branch, WorktreeID
  - **LIPSĂ**: Markers[] — detectorul le returnează dar engine-ul le aruncă!
- `DetectContext(ctx, path)` → `*WorkspaceContext`
  - Apelează detector.DetectFromFilePath → obține Root + Markers (Markers se pierd!)
  - Apelează branchManager.CompareAndUpdate → obține Branch, WorktreeID
  - Calculează ID cu contract.DeriveWorkspaceID
- `IndexWorkspace(ctx, path, language)` — indexează un workspace
- `SearchCode(ctx, filePath, query, limit, includeDocs)` → `*SearchCodeResult`
  - Apelează DetectContext → obține wctx
  - Apelează inferLanguage(filePath, wctx) — **PROBLEMA**: hardcodat, nu folosește Markers
  - Calculează collection = "ragcode-{wctx.ID}-{lang}"
  - Verifică CollectionExists
  - Apelează search.Search sau search.SearchCodeOnly
- `inferLanguage(filePath, wctx)` — **DE REFĂCUT**: trebuie să folosească:
  1. parser.GetByFile(filePath).Name() — din extensia fișierului
  2. wctx.Markers — din markerii workspace-ului (go.mod→go, composer.json→php)
  - Dar wctx nu are Markers! Trebuie adăugat.

### indexer/indexer.go
- `Service.IndexItems(ctx, collection, []parser.Symbol)` — embed + upsert în Qdrant
- Generează ID unic per simbol: sha256(filePath:name:startLine:endLine)

### search/search.go
- `Service.Search(ctx, collection, queryText, limit)` → `[]storage.SearchResult`
  - Embed queryText cu LLM → vector → store.Search
- `Service.SearchCodeOnly(ctx, collection, queryText, limit)` → `[]storage.SearchResult`
- `Service.CollectionExists(ctx, collection)` → bool

### internalutil/float.go
- `Float64To32([]float64)` → `[]float32` — conversie pentru vectori

---

## internal/service/tools/ — Tool-uri MCP

### workspace_helpers.go
- `DetectAndRegisterWorkspace(manager, params)` — **VECHI**: folosește internal/workspace
- `AttachAIWarning(result, wsResp)` — adaugă warning dacă MismatchRisk != "low"
- `HandleWorkspaceDetectionError(err, path)` — formatează erori de detecție
- `extractFilePathFromParams(params)` — extrage file_path din params MCP
- `inferLanguageFromPath(filePath)` — **DUPLICAT cu engine.inferLanguage!**

### search_interfaces.go
- `CodeSearcher` interface: `SearchCodeOnly(ctx, collection, query)` → `[]storage.SearchResult`

### search_local_index.go — `rag_search_code`
- **STARE ACTUALĂ**: folosește `internal/memory` și `internal/workspace` (VECHI, nu compilează)
- **CE TREBUIE**: să primească `*engine.Engine` și să apeleze `engine.SearchCode()`

### hybrid_search.go — `rag_hybrid_search`
- **STARE ACTUALĂ**: folosește `internal/memory`, `internal/workspace`, `internal/llm` (VECHI)
- Logică complexă: 60% semantic + 40% lexical scoring

### find_implementations.go — `rag_find_implementations`
- **STARE ACTUALĂ**: folosește `internal/memory`, `internal/workspace` (VECHI)

### find_type_definition.go — `rag_find_type_definition`
- **STARE ACTUALĂ**: folosește `internal/memory`, `internal/workspace` (VECHI)

### get_function_details.go — `rag_get_function_details`
- **STARE ACTUALĂ**: folosește `internal/memory`, `internal/workspace` (VECHI)

### list_package_exports.go — `rag_list_package_exports`
- **STARE ACTUALĂ**: folosește `internal/memory`, `internal/workspace` (VECHI)

### search_docs.go — `rag_search_docs`
- **STARE ACTUALĂ**: folosește `internal/memory`, `internal/workspace` (VECHI)

### index_workspace.go — `rag_index_workspace`
- **STARE ACTUALĂ**: folosește `internal/memory`, `internal/workspace` (VECHI)

### get_code_context.go — `rag_get_code_context`
- Citește fișiere direct de pe disk (nu folosește Qdrant) — probabil OK

### install_skill.go — `rag_install_skill`
- Instalează skill-uri în workspace — probabil nu depinde de memory/workspace

### list_skills.go — `rag_list_skills`
- Listează skill-uri disponibile

### updates.go — `rag_check_update`, `rag_apply_update`
- Verifică și aplică update-uri

### evaluate_ragcode.go — `rag_evaluate`
- Evaluare calitate

### utils.go
- `buildSymbolDescriptorsFromDocs(docs)` — construiește descriptori din rezultate search
- `buildSymbolsFromResults(results)` — mapează payload Qdrant → parser.Symbol

---

## Probleme identificate și plan de rezolvare

### Problema 1: WorkspaceContext pierde Markers
- `engine.detectRoot()` apelează `detector.DetectFromFilePath()` care returnează `WorkspaceCandidate` cu `Markers[]`
- Dar `detectRoot()` returnează doar `string` (root), aruncând Markers
- **Fix**: `WorkspaceContext` trebuie să includă `Markers []string`

### Problema 2: inferLanguage duplicat și hardcodat
- Există în `engine.go` (hardcodat cu switch pe extensii)
- Există în `workspace_helpers.go` (`inferLanguageFromPath` — același lucru)
- Logica corectă există în `inspirations/rag-code-mcp/internal/workspace/language_detection.go`:
  - `GetPrimaryLanguage(root, markers)` — din markeri (go.mod→go, composer.json→php)
- **Fix**: O singură funcție în engine care folosește:
  1. `parser.GetByFile(filePath).Name()` — din extensia fișierului (prioritate 1)
  2. Markers din WorkspaceContext — din markerii detectați (prioritate 2, fallback)

### Problema 3: Tool-urile folosesc pachete vechi (internal/memory, internal/workspace)
- Toate tool-urile din `internal/service/tools/` importă `internal/memory` și `internal/workspace`
- Aceste pachete nu mai există în noua structură
- **Fix**: Tool-urile trebuie să primească `*engine.Engine` și să apeleze metodele lui

### Ordinea de rezolvare:
1. Fix `WorkspaceContext` să includă `Markers[]` (engine.go)
2. Fix `inferLanguage` să folosească `parser.GetByFile` + Markers (engine.go)
3. Adaugă metode în Engine pentru fiecare operație necesară tool-urilor
4. Rescrie tool-urile să folosească Engine în loc de internal/memory + internal/workspace
5. Compilare și teste

---

## Transport & Process Architecture — Daemon + Adapter

### Overview

Single binary (`rag-code-mcp`) with two modes:
- **Default (adapter)**: Thin Stdio ↔ Unix socket bridge (~1MB RAM)
- **`--daemon`**: Heavy backend with Qdrant, Ollama, Engine, MCP Server

### Packages

#### `internal/daemon/`
- `pidfile.go` — PID file write/read/remove, process alive check
- `health.go` — `/health` endpoint (version, uptime, PID)
- `server.go` — `ListenAndServe()`: Unix socket + optional TCP HTTP listeners
- `run.go` — Full daemon startup (config, health checks, LLM, Qdrant, Engine, MCP tools)

#### `internal/adapter/`
- `adapter.go` — `RunBridge()`: reads stdin JSON-RPC → POST /mcp via Unix socket → stdout
- `lifecycle.go` — `IsDaemonRunning()`, `StartDaemon()`, `StopDaemon()`, `CleanupStaleFiles()`

### Files
- `~/.ragcode/daemon.sock` — Unix domain socket (primary channel)
- `~/.ragcode/daemon.pid` — PID + version + start time
- Port `:3000` — Optional debug-only HTTP listener for curl/external agents (binds to `127.0.0.1`; disabled unless explicitly enabled via config; treat as privileged interface — requires auth if used in multi-user environments)

### Protocol
- **JSON-RPC over HTTP/1.1** — no SSE, no sessions, no handshake
- **Request**: `POST /mcp` with JSON body
- **Response**: `200 OK` with JSON body
- **Health**: `GET /health` → JSON status

### Key Design Decisions
- Daemon survives IDE restarts (not tied to any stdin)
- Multiple adapters connect to single daemon concurrently
- `X-Workspace-Hint` header carries CWD from IDE for workspace detection fallback
- Version upgrade: adapter detects newer version → kill old daemon → restart
- Stale socket/PID detection: connect fail → cleanup → restart daemon

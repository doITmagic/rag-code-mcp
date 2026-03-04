# V2 - Delivery Plan

## Goal
Deliver a modular V2 detection core that is deterministic, testable, and safe for IDE-driven MCP usage.

## Task 1: Define V2 acceptance criteria
### Subtasks
- 1.1 Define success metrics (deterministic resolution rate, ambiguity handling, fail-fast quality).
- 1.2 Define non-goals for V2 (no embedding/search engine rewrite in this phase).
- 1.3 Define rollout gates for integration into existing tools.

## Task 2: Build module-by-module roadmap
### Subtasks
- 2.1 Lock contracts for `contract`, `resolver`, `detector`, `branchstate`, `registry`.
- 2.2 Sequence implementation with tests-first milestones.
- 2.3 Assign ownership and review checkpoints per module.

## Task 3: Integration and migration strategy
### Subtasks
- 3.1 Add feature flag for gradual adoption in production tools.
- 3.2 Migrate tools to single resolver path in controlled batches.
- 3.3 Remove legacy fallback behavior once parity is confirmed.

## Task 4: Quality and release
### Subtasks
- 4.1 Validate behavior with AI-like end-to-end scenarios.
- 4.2 Add observability (reason codes, fallback counters, ambiguity counters).
- 4.3 Create release checklist and rollback plan.

## Task 5: Issue #21 hardening (Path Resolver V2)
### Subtasks
- 5.1 Add branch-aware context key (`workspace_root + branch + head + worktree_id`).
- 5.2 Add response metadata envelope in all relevant tool responses.
- 5.3 Add `branch_mismatch_risk` computation (`low|medium|high`).
- 5.4 Add deterministic confidence and fallback semantics.

## Task 6: Feedback and promotion workflow
### Subtasks
- 6.1 Add request-side `path_feedback` schema (`mismatch`, suggested path, optional reason).
- 6.2 Store suggested paths as candidates, not as trusted context.
- 6.3 Promote candidate paths only after successful resolution and execution.
- 6.4 Add audit logs for feedback ingestion and promotion events.

## Implementation Checklist (Phase 1 / Phase 2)

### Phase 1 - Core metadata and deterministic safety
- [x] **[P0]** Implement branch-aware `path_context_key` (`workspace_root + branch + head + worktree_id`).
- [x] **[P0]** Add response metadata envelope (`path_resolution_source`, `path_resolution_confidence`, `used_fallback`).
- [x] **[P0]** Add `branch_mismatch_risk` computation (`low|medium|high`) with deterministic rules.
- [x] **[P0]** Add invalidation hardening (branch isolation + anti-loop behavior on missing paths).
- [x] **[P1]** Add confidence decay policy on HEAD mismatch/rewrite.

### Phase 2 - Feedback loop and promotion workflow
- [x] **[P0]** Add request-side `path_feedback` contract and validation.
- [x] **[P0]** Persist suggestions as non-trusted candidates.
- [x] **[P0]** Promote candidate paths only after successful resolution + execution.
- [ ] **[P1]** Add audit logs and metrics for feedback ingestion/promotion.

## Legacy Parity Tasks (V1 → V2)

### Workspace Module
- [x] **[P0]** Port workspace cache (TTL, helpers) from `internal/workspace/cache.go` → `pkg/workspace/cache`.
- [x] **[P0]** Reintroduce runtime marker/exclusion configuration APIs.
- [x] **[P0]** Restore helper APIs `DetectFromParams`, `CollectionName`, `Metadata` for search/indexing.
- [x] **[P1]** Rewire indexing metrics integration (file/chunk counts) or document new owner pipeline.

### Storage Module (Qdrant)
- [x] **[P0]** Add `SearchDocsOnly`, `SearchCodeOnly`, `searchByChunkType` equivalents (or adapters).
- [x] **[P0]** Add utility endpoints `GetCollectionInfo`, `GetCollectionPointCount`, cleanup/merge helpers.
- [x] **[P0]** Ensure payload mapping/dedup (file, chunk_id) parity with legacy implementation.
- [x] **[P1]** Document adapter pattern for search service if functionality moves there.

### Workspace Manager Features
- [x] **[P0]** Implement filesystem watchers (legacy `manager.watchers`).
- [x] **[P0]** Port incremental reindex safeguards/indexing guards.
- [x] **[P0]** Reintroduce multi-language analyzer selection logic.
- [x] **[P1]** Decide whether these live under `pkg/workspace/watch` or other module and document ownership.

## Task 7: Smart Search Consolidation

### Goal
Reduce tool-urile de căutare la un singur `rag_search` inteligent, minimizând "decision fatigue" pentru agenții LLM.
Elimină `rag_search_code` (search_local_index.go) și mută toate capabilitățile în `rag_search` (smart_search.go).

### Subtasks
- [x] **[P0]** Adaugă parametrul `include_full_content: bool` la `SmartSearchInput` — când `true`, ignoră logica adaptivă compact/full și returnează mereu codul sursă integral.
- [x] **[P0]** Adaugă parametrul `include_docs: bool` la `SmartSearchInput` — când `true`, caută și în chunk-urile de documentație markdown (necesită Task 8).
- [x] **[P1]** Actualizează description-ul tool-ului `rag_search` să menționeze noii parametri.
- [ ] **[P1]** Deprecate/Șterge `search_local_index.go` (`rag_search_code`) — verifică că toate capabilitățile (Graph Context Expansion, mode discovery/exact) sunt acoperite de `smart_search.go`.
- [ ] **[P2]** Actualizează `doc_search_local_index.md` → transformă în `doc_smart_search.md` cu documentație unificată.
- [ ] **[P2]** Actualizează testele existente din `tests/` pentru a reflecta noua schemă de input.

### Dependențe
- Task 8 (pentru `include_docs`)

### Tool-uri care rămân (fiecare ortogonal, fără suprapunere):
| Tool | Rol |
|---|---|
| `rag_search` | Căutare universală (semantic + hybrid + docs) |
| `rag_find_usages` | Navigare AST Graph (cine folosește simbolul X?) |
| `rag_call_hierarchy` | Traversare caller/callee tree |
| `rag_list_package_exports` | Listare exports per pachet |
| `rag_read_file_context` | Citire fișier AST-aware |
| `rag_index_workspace` | Indexare manuală |

## Task 8: Indexare Documentație Markdown

### Goal
Indexează fișierele `.md` din workspace (README, guides, API docs) în aceeași colecție Qdrant cu tag `chunk_type: "markdown"`, permițând căutare semantică cross-domain (cod + documentație) fără tool-uri separate.

### Arhitectură
- **Chunking:** `langchaingo/textsplitter` (`MarkdownHeaderTextSplitter`) cu heading hierarchy, overlap, și păstrarea code blocks/tables.
- **Embedding:** Același model Ollama ca pentru cod.
- **Storage:** Aceeași colecție Qdrant, diferențiat prin `chunk_type: "markdown"` în payload.
- **Potrivire cod ↔ doc:** 100% prin similaritate semantică (embedding cosine similarity) — fără regex, fără extragere explicită de simboluri, language-agnostic.

### Subtasks

#### Faza A — Dependință și chunking
- [x] **[P0]** `go get github.com/tmc/langchaingo` — adaugă dependința.
- [x] **[P0]** Creează `pkg/indexer/markdown.go` — wrapper peste `textsplitter.MarkdownHeaderTextSplitter` cu:
  - `WithHeadingHierarchy(true)` — prepune heading-urile părinte la fiecare chunk.
  - `WithCodeBlocks(true)` — păstrează code blocks întregi.
  - `chunkSize: 2000`, `chunkOverlap: 200` — configurabil.
- [x] **[P0]** Adaugă teste unitare pentru chunking markdown (`pkg/indexer/markdown_test.go`).

#### Faza B — Indexare în pipeline-ul existent
- [x] **[P0]** Extinde `pkg/indexer/service.go` — la scanarea workspace-ului, detectează fișiere `.md` / `.markdown` (excluzând `node_modules/`, `vendor/`, `.git/`).
- [x] **[P0]** Indexează chunk-urile markdown cu payload:
  ```json
  {
    "chunk_type": "markdown",
    "file_path": "docs/architecture.md",
    "section_heading": "## Indexer Service",
    "content": "chunk text..."
  }
  ```
- [x] **[P0]** Integrează indexarea docs în state tracking (`state.json` — `mod_time` / `size` diff) pentru re-indexare incrementală.
- [ ] **[P1]** Adaugă progress tracking pentru docs în `IndexProgress` (ex: `docs: {done: 5, total: 8}`).

#### Faza C — Căutare integrată în Smart Search
- [x] **[P0]** Când `include_docs: true`, `smart_search.go` adaugă o a 3-a goroutină de căutare semantică filtrată pe `chunk_type == "markdown"`. *(realizat prin pasarea `includeDocs` la `engine.SearchCode` care folosește `SearchWithVector` în loc de `SearchCodeWithVector`)*
- [x] **[P0]** Rezultatele din docs se merge-uiesc cu cele din cod, fiecare marcat cu `_source: "docs"`. *(merge-ul se face în `engine.SearchCode` — rezultatele conțin metadata `chunk_type` pentru diferențiere)*
- [x] **[P1]** Adaugă filtru Qdrant pe storage layer: `SearchDocsOnly()` / `SearchCodeOnly()` echivalent. *(deja existent în `pkg/storage/qdrant.go`)*

#### Faza D — Suport pentru alte formate (viitor)
- [ ] **[P2]** `.txt` — split pe paragrafe cu `RecursiveCharacterSplitter`.
- [ ] **[P2]** `.json` / `.yaml` — flatten keys ca text și indexare ca documentație structurată.
- [ ] **[P2]** `.rst` / `.adoc` — convertor la markdown + chunking standard.

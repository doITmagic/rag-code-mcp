# internal/service/tools

Implementation of the MCP tools exposed by the RagCode AI assistants server.

> **Full Documentation**: [`docs/tools/`](/docs/tools/)

---

## Available Tools

| Tool | File | Documentation |
|------|--------|-------------|
| `rag_search` | `smart_search.go` | [doc_search_local_index.md](/docs/tools/doc_search_local_index.md) |
| `rag_find_usages` | `find_usages.go` | [doc_find_usages.md](/docs/tools/doc_find_usages.md) |
| `rag_call_hierarchy` | `call_hierarchy.go` | [doc_call_hierarchy.md](/docs/tools/doc_call_hierarchy.md) |
| `rag_list_package_exports` | `list_package_exports.go` | [doc_list_package_exports.md](/docs/tools/doc_list_package_exports.md) |
| `rag_read_file_context` | `read_file_context.go` | [doc_read_file_context.md](/docs/tools/doc_read_file_context.md) |
| `rag_index_workspace` | `index_workspace.go` | [doc_index_workspace.md](/docs/tools/doc_index_workspace.md) |
| `rag_evaluate` | `evaluate_ragcode.go` | [doc_evaluate_ragcode.md](/docs/tools/doc_evaluate_ragcode.md) |
| `rag_check_update` | `update_tools.go` | [doc_updates.md](/docs/tools/doc_updates.md) |
| `rag_apply_update` | `update_tools.go` | [doc_updates.md](/docs/tools/doc_updates.md) |
| `rag_list_skills` | `agent_skills.go` | (none) |
| `rag_install_skill` | `agent_skills.go` | (none) |

---

## Package Structure

```text
internal/service/tools/
├── smart_search.go                 # Execute() orchestrator + Register + types
├── smart_search_pipeline.go        # Pipeline stages: normalizeInput, runParallelSearch,
│                                   #   applyFilters, buildResponseMeta, serializeResults
├── smart_search_path_scope.go      # Thin adapter → pkg/scoring.PathProximity
├── smart_search_path_scope_test.go
├── find_usages.go
├── call_hierarchy.go
├── list_package_exports.go
├── read_file_context.go
├── index_workspace.go
├── evaluate_ragcode.go
├── update_tools.go                 # Check and apply updates tools
├── agent_skills.go                 # List and install skills tools
├── healthcheck.go
├── response.go                     # ToolResponse, ContextMetadata, JSON helpers
└── README.md
```

### External Packages Used

| Package | Purpose |
|--------|------|
| `pkg/scoring` | Pure scoring functions: token filtering, lexical match, path proximity |
| `pkg/telemetry` | Context savings calculations (bytes avoided / tokens saved) |
| `pkg/storage` | SearchResult, Point types |
| `internal/service/engine` | SearchCode, HybridSearchCode, DetectContext |
| `internal/skills` | Fetch and installation of skill packs |
| `internal/updater` | Checking and applying component updates |

---

## Implementation Pattern

Any new tool must adhere to:

1. **Implement tool interface**: `Register(server)` + `Execute(ctx, input) (string, error)`
2. **Prefer `file_path`**: required for accurate workspace resolution in multi-root environments
3. **Resolve context**: `t.engine.DetectContext(ctx, filePath)` for per-branch/repo isolation
4. **ExactSearch vs Search**: exact for AST metadata, semantic for concepts
5. **Standardized response**: all tools return `ToolResponse` with `Data`, `ContextMetadata`, `Telemetry`

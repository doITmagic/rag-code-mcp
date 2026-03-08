# internal/service/tools

Implementarea tool-urilor MCP expuse de serverul RagCode AI assistants.

> **Documentație completă**: [`docs/tools/`](/docs/tools/)

---

## Tool-uri disponibile

| Tool | Fișier | Documentație |
|------|--------|-------------|
| `rag_search` | `smart_search.go` | [doc_search_local_index.md](/docs/tools/doc_search_local_index.md) |
| `rag_find_usages` | `find_usages.go` | [doc_find_usages.md](/docs/tools/doc_find_usages.md) |
| `rag_call_hierarchy` | `call_hierarchy.go` | [doc_call_hierarchy.md](/docs/tools/doc_call_hierarchy.md) |
| `rag_list_package_exports` | `list_package_exports.go` | [doc_list_package_exports.md](/docs/tools/doc_list_package_exports.md) |
| `rag_read_file_context` | `read_file_context.go` | [doc_read_file_context.md](/docs/tools/doc_read_file_context.md) |
| `rag_index_workspace` | `index_workspace.go` | [doc_index_workspace.md](/docs/tools/doc_index_workspace.md) |
| `rag_evaluate` | `evaluate_ragcode.go` | [doc_evaluate_ragcode.md](/docs/tools/doc_evaluate_ragcode.md) |

---

## Structura pachetului

```
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
├── healthcheck.go
├── response.go                     # ToolResponse, ContextMetadata, JSON helpers
└── README.md
```

### Pachete externe utilizate

| Pachet | Scop |
|--------|------|
| `pkg/scoring` | Funcții pure de scoring: token filtering, lexical match, path proximity |
| `pkg/telemetry` | Calculul economiei de context (bytes avoided / tokens saved) |
| `pkg/storage` | Tipuri SearchResult, Point |
| `internal/service/engine` | SearchCode, HybridSearchCode, DetectContext |

---

## Pattern de implementare

Orice tool nou trebuie să respecte:

1. **Implement tool interface**: `Register(server)` + `Execute(ctx, input) (string, error)`
2. **Prefer `file_path`**: necesar pentru rezolvarea corectă a workspace-ului în environmente multi-root
3. **Resolve context**: `t.engine.DetectContext(ctx, filePath)` pentru izolare per branch/repo
4. **ExactSearch vs Search**: exact pentru metadata AST, semantic pentru concepte
5. **Standardized response**: toate tool-urile returnează `ToolResponse` cu `Data`, `ContextMetadata`, `Telemetry`

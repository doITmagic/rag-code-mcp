# Tool: `rag_list_package_exports`

**Source File**: `list_package_exports.go`

`rag_list_package_exports` is responsible for extracting the public interface of any package, module, or namespace tracked by the RAG MCP Engine.

## Background
Historically, package exploration depended on fuzzy LLM prompts targeting keywords like *"package X exports"*. This approach missed rare types, hallucinated definitions, and relied on unpredictable vector scoring mechanisms.

Now, this tool operates deterministically over the **Code Graph payload metadata**.

## Query Mechanism

Working in tandem with `searchSvc.ExactSearch()`, this tool issues precise vector-less filters against the Qdrant backend, instructing it to retrieve all code chunk documents that share the exact namespace or target `package`.

Filter representation:
```json
{
  "package": "<packageName>"
}
```

The system streams back all symbols bound to that namespace across all available language collections detected in the workspace root.

## Features
- **Deterministic and Complete**: Identifies all indexed symbols (interfaces, struct, function, config vars) within the `package` namespace exactly as parsed by specialized TreeSitter agents.
- **Export Filters**: Automatically discards internal or private modules based on language heuristics (e.g., lowercased symbols in Golang are excluded from the final output).
- **Groupings and Structures**: Sorts and slices outputs by `symbol_type` (functions vs. methods vs. structs), offering structured representation (`ToolResponse.Data` as an explicit array of objects) perfectly formatted for LLM processing, while supplying standard human-readable Markdown to `ToolResponse.Message`.

## Use Cases
- Perfect for exploring third-party integrations or local utilities (e.g., "What's available in `github.com/gin-gonic/gin`?").
- Verifying the implementation coverage of a specific package boundary before interacting with it.

## Telemetry
This tool aggregates byte counts derived from summing up specific lengths of `Name`, `Signature`, and `Description` metadata compared to reading the full raw files those packages belong to, logging precise byte savings.

# Tool: `rag_index_workspace`

**Source File**: `index_workspace.go`

`rag_index_workspace` gives the AI explicit control over the RAG Code engine's internal knowledge base updates. Rather than waiting for passive file polling or external Git triggers, instances can enforce immediate re-syncing of local paths.

## Query Mechanisms

This tool doesn't query the search API. Instead, it triggers the internal **Indexer Service Pipeline**.

1. **Resolution phase**: Validates the strictly provided `workspace_root` parameter, checks boundaries & dependencies, and enforces an AI confirmation sequence via the `confirm: true` parameter rule before proceeding.
2. **Asynchronous Hand-off**: Executes `engine.StartIndexingAsync(wctx.Root, wctx.ID, nil, recreate)`.

## Features
- **Delta vs Full Rebuilds**: By default, the indexing engine operates incrementally, comparing local file checksums with indexed checksum metadata to avoid re-verifying identical content. However, the `rag_index_workspace` tool accepts a boolean parameter `"recreate": true` allowing the AI to forcibly drop the Qdrant collections (for the detected context) and recompute vectors and ASTs from scratch.
- **Non-blocking (Fire-and-Forget)**: Deep parsing and embedding can take minutes for monorepos (e.g. converting 1000s of Go structs to vectors via LLM). To prevent MCP timeouts (which kill unresponsive tool calls), this tool starts a fast goroutine and returns a success `ToolResponse` immediately to the LLM. 
- **Graceful Fault-over**: Ensures `ContextMetadata` is enriched immediately with the `wctx.Root` target, so the AI gets positive confirmation that the root was successfully resolved.

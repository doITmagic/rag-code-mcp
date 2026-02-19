# Tools Package

This package implements the specific capabilities exposed by the RagCode MCP server to the AI. Each tool follows a standard pattern of registration and execution.

## Supported Tools

| Tool Name | Class | Description |
|:---|:---|:---|
| `rag_search_code` | `SearchLocalIndexTool` | Standard semantic search within the current codebase. |
| `rag_hybrid_search` | `HybridSearchTool` | High-precision search combining vectors with lexical matching. |
| `rag_index_workspace` | `IndexWorkspaceTool` | Triggers a full or incremental index scan of the repository. |
| `rag_search_docs` | `SearchDocsTool` | Searches within markdown documentation files. |
| `rag_install_skill` | `InstallSkillTool` | Installs repository-local configuration/scripts. |

## Implementation Pattern

Every tool must implement the logic to:
1. **Decode Parameters**: Map generic MCP JSON-RPC arguments to Go structures.
2. **Resolve Context**: Working with the `Engine` to find the correct repository root.
3. **Execute Core Logic**: Calling methods on `Engine` or `Search` services.
4. **Format Response**: Returning a human-friendly (or AI-friendly) text representation of the results.

## Observability

All tool calls are automatically logged to the terminal using the `logger.Instance.Highlight` method for immediate visibility during development and debugging.

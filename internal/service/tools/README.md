# Tools Package

This package implements the specific capabilities exposed by the RagCode MCP server to the AI. Each tool follows a standard pattern of registration and execution.

## Supported Tools

| Tool Name | Class | Description |
|:---|:---|:---|
| `rag_search_code` | `SearchLocalIndexTool` | Unified search (Semantic & Hybrid). Automatically performs **Code Graph** context expansion, pulling associated dependencies seamlessly. |
| `rag_index_workspace` | `IndexWorkspaceTool` | Triggers a full or incremental index scan of the repository. |
| `rag_search_docs` | `SearchDocsTool` | Searches within markdown documentation files. |
| `rag_install_skill` | `InstallSkillTool` | Installs repository-local configuration/scripts. |

### Code Graph / Context Expansion
The `rag_search_code` tool supports **Graph Resolution**. This means when returning a central symbol (like a function or a struct), the server looks into the Abstract Syntax Tree (AST) relationships persisted in the Qdrant DB. If related types, interfaces, or structs are identified, it auto-fetches them and injects their code verbatim into the final JSON array returned to the AI. This massively limits hallucinations and saves standard "pin-ball" queries.

## Implementation Pattern

Every tool must implement the logic to:
1. **Decode Parameters**: Map generic MCP JSON-RPC arguments to Go structures.
2. **Resolve Context**: Working with the `Engine` to find the correct repository root.
3. **Execute Core Logic**: Calling methods on `Engine` or `Search` services.
4. **Format Response**: Returning a human-friendly (or AI-friendly) text representation of the results.

## Observability

All tool calls are automatically logged to the terminal using the `logger.Instance.Highlight` method for immediate visibility during development and debugging.

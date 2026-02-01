# Copilot Instructions - RagCode MCP

## ⚖️ The Golden Rule
**For any information about the code (location, structure, logic, or usage), you MUST use RagCode MCP tools. Never guess code details from memory; always search the local index first.**

## Project Overview
RagCode is a Model Context Protocol (MCP) server that provides semantic code search (RAG) for local codebases using Ollama (embeddings) and Qdrant (vector storage). It supports multiple languages through a pluggable analyzer architecture.

## Architecture & Patterns
- **Core Components**:
  - `Indexer`: Orchestrates analysis, embedding, and storage.
  - `PathAnalyzer`: Interface for language-specific AST analysis (Go, PHP/Laravel, Python, HTML).
  - `CodeChunk`: The canonical v2 data structure for all indexed code symbols (functions, types, files).
  - `Workspace.Manager`: Handles multi-workspace multi-language isolation via language-specific Qdrant collections.
- **Data Flow**: Tools -> `Workspace.Manager.DetectWorkspace` -> Language Detection -> Qdrant Collection (`ragcode-{id}-{lang}`) -> Search Results.
- **Convention**: The project is migrating from `APIChunk` to `CodeChunk`. Always use `CodeChunk` for new features.

## Developer Workflows
- **Build/Install**: Use `go run ./cmd/install/main.go` to build binaries and configure local IDEs.
- **Runtime Binaries**: Installed to `~/.local/share/ragcode/bin/` by default.
- **Testing**: Use standard `go test ./...`. Use `t.TempDir()` for workspace/filesystem isolation.
- **Logging**: MCP server logs to `mcp.log` next to the executable. Check `MCP_LOG_LEVEL=debug` for issues.

## MCP Tools Usage
- `search_code`: Use as the primary entry point for exploration. **Crucial**: Always provide the `file_path` parameter as it's used for workspace and language detection.
- `index_workspace`: Triggered automatically on first query per workspace, but can be manually invoked for major changes.

## Integration Points
- **Ollama**: Requires `phi3:medium` (reasoning) and `nomic-embed-text` (embeddings) by default.
- **Qdrant**: Runs in Docker as `ragcode-qdrant` on port 6333.

## Romania/Hungarian Support (Note)
The project identifies as `rag-code-mcp`. Old configurations naming it `do-ai` or `coderag` are deprecated and paths must be updated to the new project structure in `github.com/doITmagic/rag-code-mcp`.

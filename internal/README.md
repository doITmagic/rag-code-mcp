# Internal Packages

This directory contains private library code that is specific to the `rag-code-mcp` application. Following Go conventions, code in `internal/` cannot be imported by other projects, ensuring a clean boundary between the public API and implementation details.

## Directory Structure

| Package | Description |
|:---|:---|
| [`config`](./config) | Configuration management (YAML loading, environment overrides). |
| [`healthcheck`](./healthcheck) | Startup validation for external dependencies (Ollama, Qdrant). |
| [`logger`](./logger) | Unified logging system with terminal colors and file rotation. |
| [`service`](./service) | High-level business logic orchestration (Engine, Search, Tools). |
| [`skills`](./skills) | System for installing and managing repository-local capabilities. |
| [`updater`](./updater) | Logic for self-updating the MCP server. |
| [`utils`](./utils) | Common helper functions (file paths, string manipulation). |

## Architectural Role

The `internal` directory serves as the "glue" that connects the generic packages in `pkg/` (like `parser`, `storage`, and `indexer`) into a cohesive application.

- **Orchestration**: The `service` sub-packages coordinate how data flows between the filesystem, the LLM, and the vector database.
- **Environment**: Packages like `config` and `logger` set up the execution environment.
- **Extensions**: The `skills` system allows the application to extend its own functionality within a specific workspace.

## Documentation Policy

Each sub-directory in `internal` must contain its own `README.md` that describes:
1. The specific problem the package solves.
2. Key types and interfaces.
3. Usage examples.

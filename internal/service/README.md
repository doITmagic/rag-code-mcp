# Service 

The `service` directory is the core of the `rag-code-mcp` application. It contains the high-level logic that fulfills the MCP tool requests by coordinating lower-level packages.

## Sub-Packages

### [`engine`](./engine)
The master orchestrator. It manages workspace detection, background indexing jobs, and Git state tracking. It coordinates the core indexing logic which resides in [`pkg/indexer`](../../pkg/indexer).

### [`search`](./search)
Handles semantic and hybrid search logic. It combines embedding generation with vector similarity queries and lexical re-ranking algorithms.

### [`tools`](./tools)
Implements the MCP Tool interface. Each file in this directory typically corresponds to one tool exposed to the AI (e.g., `rag_search`, `rag_find_usages`, `rag_index_workspace`).

### [`internalutil`](./internalutil)
Small shared utilities used specifically within the service layer, such as data conversion helpers.

## Design Philosophy

1. **Decoupling**: Services should not depend on specific LLM or Database implementations. They use interfaces from `pkg/llm` and `pkg/storage`.
2. **Context Awareness**: All services are designed to resolve the repository root and branch-local state before performing actions.
3. **Resilience**: Search operations are designed to fail gracefully or trigger background recovery (re-indexing) if data is missing or outdated.

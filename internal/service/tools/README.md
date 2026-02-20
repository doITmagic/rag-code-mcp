# RAG Code MCP - Tools Package

This package implements the core capabilities exposed by the RagCode MCP server to the AI assistant. Each tool is designed to be highly deterministic, context-aware, and built on top of our **Code Graph AST** and Semantic Search engine. 

Below is a detailed engineering overview of each major tool, its query mechanisms, and how it operates under the hood.

---

## 1. `rag_search_code` (Search Local Index)
**File**: `search_local_index.go`

This is the flagship tool of the MCP server, replacing standard fuzzy searches with a highly precise engine.

### Query Mechanisms
- **Discovery Mode (`mode="discovery"`)**: Performs a purely **Semantic Vector Search**. The user's query is converted to an embedding (e.g. 1024-dim vector) and compared against the code chunks in Qdrant using Cosine Similarity.
- **Exact Mode (`mode="exact"`)**: Performs a **Hybrid Search**. It combines a Semantic Vector Search (60% weight) with Lexical/BM25 scoring (40% weight). It filters candidates by exact keyword matching on the tokens inside the `content` payload.

### How it Works & Important Points
- **The Magic of Code Graph**: Regardless of the mode chosen, after Qdrant returns the best matching code chunk, the tool inspects its `Relations` metadata (extracted during indexing via AST). 
- **Auto-Fetching Context**: If the matching symbol depends on other files, interfaces, or structs, `rag_search_code` automatically executes background queries to retrieve their code and injects it into the final response as `_graph_expansion`.
- **Why it matters**: The AI receives not just the function it searched for, but also the interfaces it implements and the structs it consumes, completely eliminating the need for follow-up "pin-ball" queries.

---

## 2. `rag_find_usages` (Find Usages)
**File**: `find_usages.go`

A deterministic replacement for the legacy `find_implementations` tool. Instead of relying on fuzzy string searching, this tool leverages AST relationships natively stored in the database.

### Query Mechanism
- **Exact DB Match**: It completely bypasses the LLM Embedder. It uses the `ExactSearch` capability to query Qdrant directly using a strict filter on the `Relations` array:
  ```json
  { "Relations[].target_name": "SymbolName" }
  ```

### How it Works & Important Points
- **100% Determinist**: Because the indexer parses the AST and maps out exactly what function calls what, this tool queries those explicit edges.
- **Zero Hallucination**: You get exactly the files and line numbers where `SymbolName` is invoked, instantiated, or implemented.
- **Telemetry**: Calculates the exact bytes of the snippet returned versus the full file sizes to demonstrate the RAG context savings.

---

## 3. `rag_list_package_exports` (List Package Exports)
**File**: `list_package_exports.go`

Provides a complete, structured list of all public functions, classes, and types inside a specified package or module.

### Query Mechanism
- **Exact DB Match**: Similar to `rag_find_usages`, this tool bypasses semantic embeddings. It queries the vector database using a strict filter on the `package` payload field:
  ```json
  { "package": "packageName" }
  ```

### How it Works & Important Points
- **Data Structuring**: It pulls all indexed chunks for that package, filters out unexported (private) symbols automatically based on language rules (e.g. uppercase in Go), and groups the results by `Type` (function, class, struct).
- **Polyglot Fallback**: It searches across all known language collections (`go`, `php`, `python`, `javascript`) to find the requested module.
- **AI-Friendly Output**: It returns both a deeply structured JSON data object for the AI's internal state, and a well-formatted Markdown string for immediate reading.

---

## 4. `rag_read_file_context` (Read File Context)
**File**: `read_file_context.go`

Instead of dumping an entire 2000-line file into the AI's context window, this tool extracts specific node chunks (classes, methods) using exact line numbers.

### Query Mechanism
- **File & Line Intersection**: It reads from the local disk, but it also interacts with the index. When you request a specific block of lines, it looks for the closest index chunks that encompass those lines to provide semantic meaning.

### How it Works & Important Points
- **Telemetry Savings**: This tool is the prime example of context saving. It calculates the `baselineBytes` (size of the full file) against `actualBytes` (the size of the requested chunk) and attaches the savings percentage to the `Context.Telemetry` response.
- **Smart Expansion**: If you ask for lines 10-20, but the AST detected that a function spans lines 10-50, the tool is smart enough to serve the complete, unbroken functional chunk.

---

## Standard Implementation Pattern

Every new tool added to this package MUST adhere to the following lifecycle:

1. **Implement Tool Interface**: Register its name, description, and input schema securely.
2. **Require `file_path`**: Always request a `file_path` or `workspace_root` from the AI to ensure multi-root or polyglot workspaces resolve to the correct index collection (`ragcode-<ws_id>-<lang>`).
3. **Resolve Engine Context**: Call `t.engine.DetectContext(ctx, filePath)` to guarantee the tool executes within the boundaries of the correct Git branch and repository.
4. **Use `ExactSearch` vs `Search`**: If you are looking for specific metadata (like a package name or an AST relation), use `searchSvc.ExactSearch`. If you are looking for conceptual ideas ("how do I authenticate"), use `searchSvc.Search`.
5. **Standardized Response**: All tools MUST return their data wrapped in a JSON `ToolResponse`. This includes the core message, structured `Data`, and `ContextMetadata` (including `Telemetry` and `DetectionSource`).

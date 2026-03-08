# RagCode Lite: Architectural Concept (SQLite)

## Objective 🎯
Develop a "Lite" mode for **RagCode MCP**, designed specifically for small to medium projects. The goal is to **completely eliminate the dependency on Qdrant (Docker) and LLM embedding models**, providing users with an instant, zero-configuration setup that runs directly from a single cross-platform compiled executable.

## Why SQLite and not Embedded Document or Vector Databases? 🧐
- **No CGO / C++**: Integrating embedded vector engines or local LLM libraries (like `llama.cpp` or `sqlite-vec`) breaks portability and the ease of pure-Go cross-compilation.
- **Document Databases (e.g., Bleve, CloverDB)**: Offer excellent lexical search (TF-IDF/BM25), **but** are weak at efficiently managing *relationships* between documents.
- **SQLite (The Winning Choice)**:
  1. Natively supports advanced Full-Text Search via the **FTS5** extension (with BM25 algorithmic ranking, excellently simulating code "similarity").
  2. Is the **king of Relationships (JOINs)**: Extremely fast at mapping the synthetic graph of source code.

---

## "Code Graph" Architecture (Semantic Network Without LLMs) 🕸️

The main idea is to compensate for the lack of "semantic vector understanding" through a **strict, hard-coded extraction of structural relationships (AST)** from the code using Tree-sitter, directly from multi-language parsers (Go, PHP, Python, etc.).

### 1. Data Model (SQLite Schema)
Unlike a vector-store that stores a "flat" payload, RagCode Lite will represent the project as a Relational Graph:

- **`nodes` / `symbols` Table**:
  - `id` (unique hash)
  - `name` (class or function name)
  - `type` (class, function, interface, variable)
  - `file_path`, `package`
  - `code_snippet` (raw content)
  - *Coupled with an FTS5 virtual index on the code/name field for instantaneous lexical hybrid search.*

- **`edges` / `relations` Table**:
  - `source_id` (Who uses/calls)
  - `target_name` / `target_id` (What is used/called)
  - `relation_type` (Enum)

### 2. Extracted Relation Types (Multi-Language)
- `IMPLEMENTS`: Ex: `class MyController implements ControllerInterface`
- `HAS_METHOD`: Ex: `class User` has `func login()`
- `USES_TYPE`: Ex: A function takes an instantiated object as parameter (`func process(u User)` where `User` is imported from another file).
- `CALLS`: A function calls another function.

### 3. The Search Phase ("Smart Context Resolution")

This is where the true power of the Lite mode lies, overcoming the limitations of a classic Vector Store RAG.

**Current Problem (with Vectors/Classic RAG):**  
When an AI requests `process()`, the database brings back only the `process` function. The AI realizes the function has a parameter `u User`, but doesn't know the structure of `User`. It will have to consume time and tokens to make a second `mcp_search` call to discover its details (hoping for an embedding match).

**RagCode Lite Solution (SQLite Code Graph):**
1. The MCP receives a query for a resource (e.g., `process`).
2. Finds the `node` based on name/content via SQLite FTS5.
3. Before returning the response to the AI, RagCode executes a **Recursive Relational Query (JOIN)**: *"Bring me the full code of the `process` node AND (WHERE) all definitions of target nodes linked to this node by a `USES_TYPE` or `CALLS` relation"*.
4. **Result**: The AI instantly receives the definition of the `process` function **along with** the structure/code for `User` exactly from the original file where it was imported. Everything is resolved in a single ultra-precise move.

## Next Development Steps 🚀
1. **Parsers (Tree-sitter)**:
   - Modify the Analyze functions in `pkg/parser/*` to capture not just the `Symbol`, but also a native list of `Relation`s resolving the AST of imports and parameter types.
2. **SQLite Storage Implementation**:
   - Finalize the current `pkg/storage/sqlite.go` file, transforming it into a double-table manager (`symbols` + `relations`) with the FTS5 extension for efficient searches.
3. **Graph Search**:
   - Implement contextual resolution at the Search layer, where a `SearchResult` is populated with additional "Dependencies", providing the AI with absolutely perfect context without mathematical vector "guessing".

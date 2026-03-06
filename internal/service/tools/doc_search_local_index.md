# Tool: `rag_search_code`

**Source File**: `search_local_index.go`

`rag_search_code` is the primary and most powerful tool for codebase discovery in the entire MCP server. It acts as the intelligent bridge between an AI Assistant's intent and the vector graph database.

## Query Mechanisms

This tool exposes two distinct ways to query the codebase, controlled by the `mode` parameter:

1. **Discovery Mode (`mode="discovery"`) - Pure Semantic Search**
   * **How it works**: Uses the Local Embedder to transform the AI's natural language query into a high-dimensional vector.
   * **DB Query**: Executes a standard Cosine Similarity search inside the Qdrant database.
   * **Use Case**: Best for conceptual questions like *"How is authentication handled?"* or *"Where is the database configuration?"*

2. **Exact Mode (`mode="exact"`) - Hybrid Search**
   * **How it works**: Combines the semantic vector similarity with BM25 lexical keyword matching.
   * **DB Query**: Hits Qdrant's Hybrid Search endpoints, weighting token overlaps heavily (40%) alongside semantic similarity (60%).
   * **Use Case**: Best for finding specific structs, known function names, or exact error messages.

## The Magic: Code Graph Expansion

The most critical feature of `rag_search_code` is its **AST-aware Context Expansion**. 

Once the top matching chunk is retrieved from Qdrant, the tool checks its `Relations` payload. These relations are strict edges determined during indexing (e.g., "Struct A implements Interface B" or "Function X calls Function Y").

If relations are found:
1. The tool performs automatic, silent sub-queries for the related dependencies.
2. It fetches the exact source code of those dependencies.
3. It bundles them into the final response under the label `_graph_expansion`.

**Why is this revolutionary?**
Normally, an AI finds a function, realizes it returns an unknown `ResultBlock` struct, and has to spend another turn querying for `ResultBlock`. **Code Graph Expansion** eliminates this. The AI receives the requested function *and* the definition of `ResultBlock` in a single turn, providing complete, self-contained context.

## Telemetry
It tracks the exact sizes of the fetched code chunks and compares them to the raw sizes of their original files, returning the calculated savings in the `Context.Telemetry` property.

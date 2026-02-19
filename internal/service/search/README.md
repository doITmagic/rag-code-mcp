# Search Service

The `search` package implements the core discovery logic of RagCode. It abstraction the complexities of vector similarity and lexical matching into a simple interface.

## Search Strategies

### 1. Semantic Search (`Search`)
Uses the LLM provider to generate an embedding for the user's query and then performs a cosine similarity search against the Qdrant vector database. This is best for finding conceptually related code even if specific keywords don't match.

### 2. Hybrid Search (`HybridSearch`)
Combines semantic results with a lexical (keyword-based) re-ranking algorithm.
- First, it fetches a larger candidate set using semantic search.
- Then, it calculates a lexical score based on token frequency and exact keyword matches (e.g., function names, struct types).
- Finally, it merges the scores to ensure that exact matches for unique identifiers are pushed to the top of the results.

## Configuration

The search behavior (limits, thresholds) is controlled through the global `config` and can be overridden per tool call.

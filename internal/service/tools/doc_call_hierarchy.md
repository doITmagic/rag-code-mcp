# Tool: `rag_call_hierarchy`

**Source File**: `call_hierarchy.go`

`rag_call_hierarchy` is an advanced navigation tool that allows for automated, recursive exploration of the code's call graph. It bridges the gap between individual symbol lookups and full architectural understanding.

## Query Mechanisms

This tool operates as a stateful, recursive orchestrator over the **Code Graph Relations**. It primarily uses vector-less `ExactSearch` to traverse AST edges.

1. **Incoming Mode (`direction="incoming"`)**:
   * Identifies all "Caller" nodes by querying the database for any chunk where the `Relations` array contains a `target_name` matching the current symbol.
   * DB Filter: `{ "Relations[].target_name": "<SymbolName>" }`
   
2. **Outgoing Mode (`direction="outgoing"`)**:
   * Identifies "Callee" nodes by searching for the symbol itself and extracting all `target_name` entries from its own `Relations` payload metadata.

## Recursive Resolution Engine

The tool implements a depth-first search (DFS) with a configurable `depth` (default 2, max 5). 

- **Circular Dependency Detection**: It maintains a `visited` map per-session to detect and flag recursions (e.g., Function A calling Function B which calls Function A). These are visualized with a 🔄 marker.
- **Polyglot Stitching**: The engine automatically searches across multiple language collections (Go, Python, JS, PHP). If a Go function calls a JavaScript microservice endpoint (indexed as a relation), the tool will attempt to bridge the call graph across languages.

## Features
- **Structural Tree Metadata**: Returns a JSON-ready tree structure (`CallNode`) containing the signature, type, package, and file path for every node in the hierarchy.
- **Visual Markdown Representation**: Provides an indented tree view using standard tree-drawing characters (`└─`) for immediate human readability in the AI's chat window.
- **Context Preservation**: Like all search tools, it attaches `ContextMetadata` with detection sources and potential branch mismatch risks.

## Use Cases
- **Impact Analysis**: *"If I change this core utility function, which high-level services will be affected 3 levels up?"*
- **Execution Flow Tracking**: *"How does a request travel from the Gin Router to the SQL Repository?"*
- **Refactoring**: Understanding the complexity of a module's dependencies before attempting to decouple it.

## Telemetry
This tool does not currently log byte-savings telemetry, as its primary value is "Cognitive Speed" (reducing the number of manual search turns) rather than direct data minimization.

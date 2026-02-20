# Tool: `rag_find_usages`

**Source File**: `find_usages.go`

`rag_find_usages` provides deterministic identification of where and how a symbol (function, class, module or type) is utilized across the entirety of a workspace.

## Heritage
This tool replaced the legacy fuzzy-search tool `find_implementations`. Previously, discovering where an interface was implemented relied heavily on string proximity and heuristic keywords embedded in LLM queries (e.g. "Engine implementation usage"). This proved brittle and imprecise.

## Query Mechanisms

`rag_find_usages` is built directly on top of the **Code Graph AST (Relations Matrix)** stored inside Qdrant during index time.

It completely **bypasses the Semantic Embedder (LLM)** and issues an Exact Match database filter:

```json
{
  "Relations[].target_name": "<SymbolName>"
}
```

By querying the exact path of the payload JSON, the database operates efficiently and precisely, scanning the `Relations` arrays of every indexed AST node (both methods, classes or standalone functions) to identify callers, implementers, and dependent types referencing the `SymbolName`.

## Features
- **Deterministic and Robust**: Resolves complex usage paths correctly regardless of lexical variations or generic terms used by developers. Zero semantic hallucinations!
- **Relation Context Mapping**: Not only does it return the line numbers and snippets of where something is used, but it also extracts the specific `<type>` of the relation match (e.g. `call` or `implements`), explaining **why** the snippet was returned.
- **Polyglot Execution**: Iterates across possible language indexes (`go`, `python`, `javascript`, `php`) mapped to the requested workspace dynamically.

## Use Cases
- Perfect for safely finding references prior to refactoring a function.
- Finding which structs satisfy a given Go interface.
- Discovering what modules call a legacy utility function. 

## Telemetry
This tool measures the baseline sizes of all parent source files associated with every snippet returned. The byte-savings are calculated dynamically against the slim snippets included in the ToolResponse and sent via the `Context.Telemetry` pipeline.

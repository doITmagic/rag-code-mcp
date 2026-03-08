# Tool: `rag_read_file_context`

**Source File**: `read_file_context.go`

`rag_read_file_context` operates as the primary instrument for streaming exact sections of local disk files bound by specific blocks, lines, or semantic chunks into the AI's limited context window.

## Background
Rather than flooding an AI context window with 5,000 lines of `main.go`, this tool allows the AI to surgically request lines `start_line` to `end_line`.

However, traditional `head -n` or `tail -n` commands fail miserably when a function happens to end *just outside* the requested byte boundary.

## Query Mechanisms

This tool combines local file-system parsing with intelligent **Code Graph (Index)** verification to guarantee syntactic correctness.

1. **Range Clamping**: The tool expands the requested line ranges based on indexed AST chunk boundaries. If the AI asks for lines 10-20, but the nearest vector database document (a `Code` chunk) identifies a structured method spanning lines 12-25, the tool automatically realigns and encompasses the complete logical block (lines 12-25).
2. **Raw Disk Fallback**: For files not mapped dynamically by the indexer (e.g. arbitrary `.env` or simple Markdown texts), the tool safely handles standard `os.Open` scanning.

## Features
- **Semantic Overlap Engine**: Automatically aligns user-requested parameters with meaningful Code chunks (interfaces, classes, functions) mapped in Qdrant payloads to prevent serving crippled or malformed Go/Python snippets.
- **Auto-Formatting**: Re-indexes the output array string to prepend absolute physical line integers so the LLM has exact tracking and orientation for applying patch/sed tools.
- **Context Metadata Mapping**: Wraps the extracted snippets inside the unified `ToolResponse.Context` containing Git origin paths, Mismatch Risk (in case of detached branches or out-of-sync indexes) and Telemetry variables.

## Use Cases
- Perfect for debugging stack traces (`panicked at src/main.go:55`). The AI can immediately pull `50-60` and the tool will ensure the entire function structure enclosing line 55 is served.

## Telemetry
This tool sits at the heart of our **Savings Telemetry**. It calculates operations measuring the size of the exact subset extracted, against a `os.Stat` of the overarching source code baseline size, demonstrating the percentage-wise reductions in LLM processing costs.

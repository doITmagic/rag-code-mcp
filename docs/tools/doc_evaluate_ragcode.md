# Tool: `rag_evaluate`

**Source File**: `evaluate_ragcode.go`

`rag_evaluate` is a unique introspective tool designed for continuous improvement of the RagCode MCP server from the perspective of the very AI instances that use it.

## Query Mechanisms

This tool does not query the code graph repository. Instead, it queries the **internal state and configuration** of the running MCP Engine and dynamically orchestrates a prompt requesting self-reflection from the AI context.

1. **State Diagnostics**: Resolves the current workspace boundary.
2. **Health Aggregation**: Pings the underlying Ollama embeddings server and Qdrant backend to log their ping/status responses.
3. **Prompt Generation**: Dynamically constructs a Markdown questionnaire prompting the AI to write down its pain points, cache misses, or hallucination events during the active session.

## Features
- **Unchained Execution**: Unlike search tools, `rag_evaluate` executes successfully even if the `DetectContext` phase fails or falls-back. It intentionally allows evaluation independent of workspace bounds.
- **Diagnostic Visibility**: It dumps the active system configuration (e.g. `LLM=qwen3ro`, `Embed=nomic-embed-text`) into the LLM's context window. This teaches the AI exactly what hardware/models are actively providing its context, allowing for highly specific telemetry feedbacks like *"The nomic embeddings model seems to fail mapping the PHP namespaces correctly"*.
- **Agentic Loop**: Acts as an anchor point for automated End-to-End tests where test agents utilize tools, and run `rag_evaluate` at the end of their task to assert tool qualities.

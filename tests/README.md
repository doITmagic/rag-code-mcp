# Integration & Tool Tests

This directory contains scripts and tools for testing the RagCode MCP server and its various search/indexing capabilities.

## Contents

| File | Description |
|:---|:---|
| `trigger_index.go` | The primary integration test script. It simulates an MCP client connecting via SSE and triggers initialization, indexing, and search tool calls. |
| `functional_sse_tools_test.go` | Go tests for validating tool execution over the SSE transport. |
| `complex_results.txt` | Sample output from complicated search queries to verify result quality. |

## How to Run Integration Tests

### 1. Start the RagCode Server
Ensure the main server is running:
```bash
go run cmd/rag-code-mcp/main.go
```

### 2. Run the Trigger Script
Execute the test script from the root of the project:
```bash
go run tests/trigger_index.go
```

## Manual Evaluation
Use this directory to store your manual evaluation results or edge-case query tests to ensure that changes in the parsing/embedding logic do not regress search quality.

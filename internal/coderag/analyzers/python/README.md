# Python Code Analyzer

TODO: Implement Python code analyzer for CodeRAG MCP.

## Structure

- `analyzer.go` - Implementation of `codetypes.PathAnalyzer` for Python
- `api_analyzer.go` - Implementation of `codetypes.APIAnalyzer` for Python
- `types.go` - Python-specific internal types

## Implementation Guidelines

1. Implement `codetypes.PathAnalyzer` interface:
   - `AnalyzePaths(paths []string) ([]codetypes.CodeChunk, error)`

2. Implement `codetypes.APIAnalyzer` interface:
   - `AnalyzeAPIPaths(paths []string) ([]codetypes.APIChunk, error)`

3. Set `Language` field to `"python"` in all returned chunks

4. Use Python AST parsing (e.g., via `go/ast` equivalent or Python subprocess)

5. Register in `language_manager.go`:
   ```go
   case LanguagePython:
       return python.NewCodeAnalyzer()
   ```

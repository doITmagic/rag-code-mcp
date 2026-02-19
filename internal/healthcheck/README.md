# Healthcheck Package

The `healthcheck` package ensures that the RagCode MCP server's environment is properly configured before the server starts accepting requests.

## Responsibilities

- **Ollama Connectivity**: Verifies that the Ollama server is reachable and that both chat and embedding models are pulled and ready.
- **Storage Connectivity**: Confirms that the Qdrant vector database is up and accessible.
- **Resource Readiness**: Checks for necessary filesystem permissions and configuration availability.

## Startup Check

The health check is performed once during application initialization in `main.go`:

```go
if err := healthcheck.CheckAll(ctx, cfg); err != nil {
    log.Fatalf("Environment check failed: %v", err)
}
```

## Failure Behavior

If any critical dependency is missing or unreachable, the application will exit with a non-zero status code and a clear error message describing which component failed. This prevents the server from running in a "degraded" state where it might return empty search results without warning the user.

# Logger Package

The `logger` package provides a unified logging interface for the RagCode MCP server. It is designed to be safe for use in MCP (Model Context Protocol) environments where `stdout` is reserved for protocol communication.

## Features

- **Protocol Safety**: Automatically redirects standard logging to `stderr` or a localized log file to avoid corrupting MCP JSON-RPC messages.
- **Terminal Colors**: Uses ANSI escape codes to highlight important events in development consoles.
- **File Rotation**: Automatically manages log file size to prevent disk exhaustion.
- **Search Highlighting**: Includes a specialized `Highlight` method for making incoming search requests stand out in the logs.

## Usage

### Basic Logging
```go
import "github.com/doITmagic/rag-code-mcp/internal/logger"

logger.Instance.Info("Application started on %s", port)
logger.Instance.Warn("Connection delay detected")
logger.Instance.Error("Failed to save state: %v", err)
```

### Search Highlighting
Specialized logging for incoming tool calls to make them visible in the console:
```go
logger.Instance.Highlight("rag_search_code: '%s' (context: %s)", query, filePath)
```

## Level Control
Log verbosity is controlled via the `MCP_LOG_LEVEL` environment variable:
- `debug`
- `info` (default)
- `warn`
- `error`

## Output Configuration
- **Stderr**: All logs are printed to `stderr` by default.
- **File**: Set `MCP_LOG_FILE` to a path to enable persistent logging with rotation.

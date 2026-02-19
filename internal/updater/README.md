# Updater Package

The `updater` package provides the mechanism for the RagCode MCP server to self-update to the latest version.

## Features

- **GitHub Integration**: Checks for new releases on the project's GitHub repository.
- **Binary Replacement**: Safely downloads and replaces the running executable.
- **Version Tracking**: Maintains knowledge of the currently installed version versus the latest available.

## Usage

The update process is typically triggered by a specific MCP tool or an automated background check.

```go
import "github.com/doITmagic/rag-code-mcp/internal/updater"

// Check for updates
newVersion, available := updater.CheckForUpdates()
if available {
    // Perform update
    err := updater.PerformUpdate()
}
```

## Safety Considerations

- **Integrity**: Future versions will include checksum verification for downloaded binaries.
- **Rollback**: Updates happen via binary replacement; ensure proper backup/versioning of the installation directory is maintained.

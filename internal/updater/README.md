# Updater Package

The `updater` package provides the mechanism for the RagCode MCP server to self-update to the latest version.

## Features

- **GitHub Integration**: Checks for new releases on the project's GitHub repository.
- **Binary Replacement**: Safely downloads, unpacks, and replaces the running executable.
- **Integrity Verification**: Verifies SHA256 checksums to ensure the downloaded archive is valid before applying.
- **Caching**: Tracks available updates to prevent spamming GitHub API rate limits.

## Usage

The update process is typically triggered by a specific MCP tool (`rag_check_update`, `rag_apply_update`) or an automated background check on daemon startup.

```go
import "github.com/doITmagic/rag-code-mcp/internal/updater"

// Check for updates
info, err := updater.CheckForUpdates(ctx, "v2.1.60", false)
if err != nil {
    // Handle error
}

if info != nil {
    // Non-nil info means an update is available
    err := updater.DownloadVerifyAndApply(ctx, info)
    if err != nil {
        // Handle error
    }
}
```

## Safety Considerations

- **Verification**: Updates are cryptographically verified using `checksums.txt` included in releases.
- **Rollback**: Updates happen via binary replacement; if the process fails mid-way, the original executable is restored from a temporary backup.

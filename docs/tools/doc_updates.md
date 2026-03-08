# Tool: `rag_check_update` & `rag_apply_update`

**Source File**: `updates.go`

These two companion tools provide self-maintaining capabilities to the RagCode MCP server, allowing the AI assistant to detect when a newer version exists on GitHub and gracefully trigger an upgrade.

> **Note:** These tools are only registered when `auto_update: false` in config. By default, the daemon handles updates automatically on startup, so these tools are hidden to reduce AI context overhead.

## 1. `rag_check_update`

### Query Mechanisms
- **External API Query**: Hooks into the public GitHub Releases API (`api.github.com/repos/doITmagic/rag-code-mcp/releases/latest`) to check the newest tagged version.
- **Cache Mechanism**: Results are cached for 24 hours in `~/.ragcode/update_cache.json` (written atomically via temp-file + rename, permissions `0600`). Pass `"force": true` to bypass the cache.
- **Version Comparison**: Compares the natively injected build version (`t.version` passed via `LDFLAGS`) against the Semantic Version tag discovered remotely using `Masterminds/semver`.
- **Remote Stable Model Check**: Fetches `config.yaml` from the `main` branch on GitHub to detect if the recommended embedding model has changed. Displays a warning if the local model differs from the remote stable model.

### Features
- **Deterministic**: Provides the exact download URLs, tag, and version details in the structured `ToolResponse.Data` response so the AI can summarize what's new for the user.
- **Structured Response**: Returns a `ToolResponse` JSON with `status`, `message`, `error`, `context` (current version), and `data` (full `UpdateInfo` object) fields.

---

## 2. `rag_apply_update`

### Query Mechanisms
- **Artifact Download**: Performs an HTTP streaming download of the release artifact (`.tar.gz` or `.zip`) aligned with the host's operating system and architecture (`GOOS`/`GOARCH`).
- **SHA256 Verification**: Downloads `checksums.txt` from the release, locates the hash for the platform-specific archive, computes the SHA256 of the downloaded file, and compares them. Fails if the hashes don't match.

### Features
- **Installer Handoff**: Downloads the artifact to a securely generated `os.CreateTemp` path, extracts it to a temp directory, then spawns `rag-code-install --upgrade -y` from the extracted directory. The current process exits via `os.Exit(0)` to hand control to the installer. The installer handles process stopping (via PID file) and file copying to `~/.ragcode/bin/`.
- **Cleanup**: On error paths, the temp directory is automatically cleaned up. On successful handoff, the installer takes ownership.
- **Graceful Restart**: Once the installer completes, the AI prompts the user to restart their IDE or MCP client so the new version takes over.

---

## 3. Daemon Auto-Update

### Mechanism
- **Automatic by Default**: The `auto_update: true` setting in `config.yaml` (default) enables automatic update checking on daemon startup.
- **Background Worker**: 10 seconds after the daemon starts, a background goroutine calls `updater.CheckForUpdates()` (using the 24h cache), and if a new version is found, calls `updater.DownloadVerifyAndApply()` — which downloads, verifies SHA256, and spawns the installer.
- **Shared Helper**: `DownloadVerifyAndApply()` is the unified code path used by both the daemon auto-update worker and the `rag_apply_update` MCP tool.
- **Disable**: Set `auto_update: false` in `~/.ragcode/bin/config.yaml` to disable automatic updates.

---

## 4. Adapter Version Upgrade

When the adapter (stdio bridge) starts, it checks if the running daemon is on an older version. If so, it stops the old daemon and restarts with the current binary version. This ensures that after a manual update, the daemon is always running the latest installed version.

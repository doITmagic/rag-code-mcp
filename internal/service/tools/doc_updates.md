# Tool: `rag_check_update` & `rag_apply_update`

**Source File**: `updates.go`

These two companion tools provide self-maintaining capabilities to the RagCode MCP server, allowing the AI assistant to detect when a newer version exists on GitHub and gracefully trigger an in-place upgrade.

## 1. `rag_check_update`

### Query Mechanisms
- **External API Query**: Hooks into the public GitHub Releases API (`api.github.com/repos/doITmagic/rag-code-mcp/releases/latest`) to check the newest tagged version.
- **Cache Mechanism**: By default, checks are cached to avoid API rate limiting. This can be overridden by passing `"force": true`.
- **Version Comparison**: Compares the natively injected build version (`t.version` passed via `LDFLAGS`) against the Semantic Version tag discovered remotely.

### Features
- **Deterministic**: Provides the exact changelog and download URLs in the structured `ToolResponse.Data` response so the AI can summarize new features for the user.

---

## 2. `rag_apply_update`

### Query Mechanisms
- **Artifact Download**: Performs an HTTP streaming download of the release artifact (ZIP or TAR.GZ) aligned with the host's operating system and architecture (`GOOS`/`GOARCH`).
- **Cryptographic Verification**: *(Future Hook)* Validates the BLAKE3/SHA256 checksum associated with the release to ensure binary integrity before executing a swap.

### Features
- **In-place Binary Swap**: Downloads the artifact to a securely generated `os.CreateTemp` path. Unpacks the new `rag-code-mcp` executable and aggressively swaps it with the current running binary (using standard POSIX rename/replace syscalls depending on OS).
- **Graceful Handoff**: Once the new binary is placed on disk, it prompts the AI to notify the user to restart their IDE or MCP client so the new process takes over.

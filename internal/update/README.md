[RagCode](../../README.md) › [internal](../README.md) › update

# update

Finds newer rag-code-mcp releases and installs them over the installed
binary: the release check behind `rag_server_info`, the automatic update a
server runs when it starts, and `rag-code-mcp -update`.

## Where it sits

| Caller | What it uses |
|:---|:---|
| [`cmd/rag-code-mcp`](../../cmd/rag-code-mcp/README.md) | `Latest`, `Less`, `Run` for the automatic update (`auto_update`) and the `-update` flag; `Remember` at start |
| [`tools`](../../tools/README.md) | `Latest` and `Less` for the `update` block of `rag_server_info`; `GitHubLatest` as the default release URL; `Replaced` and `RestartNote` in every answer |

It calls [`index`](../../index/README.md) for `WriteAtomic` (the cache) and
`LockFile` (one updater at a time), and [`config`](../config/README.md) for
`HomeDir`.

## Files

| File | Holds |
|:---|:---|
| [`update.go`](update.go) | `GitHubLatest`, `Remember`, `Replaced`, `RestartNote`, `Latest` (cached release check), `Less` (version order), `Run` (lock + `Install`), `Install`, `AssetName`, archive download, checksum check, extraction, binary replacement |

### Tests

| File | Covers |
|:---|:---|
| [`update_test.go`](update_test.go) | `Less` (pre-releases, build metadata, dev builds); install from a directory; no install when the release is older, equal or the current version is a dev build; a wrong or missing checksum leaves the binary alone; install from a GitHub-style JSON endpoint, and no download for an older tag; `Run` skips while another process holds `update.lock`; zip extraction for Windows; the `.old` rename only on Windows; `Replaced` after the binary is renamed over |

## Key types and functions

| Name | Contract |
|:---|:---|
| `GitHubLatest` | `https://api.github.com/repos/doITmagic/rag-code-mcp/releases/latest`; GitHub leaves pre-releases out of it |
| `Cached() (latest, fresh)` | The tag of the last check from the cache and whether it is fresh (same ages as `Latest`); no request. `rag_server_info` answers from it. |
| `Latest(ctx, client, url) string` | The latest tag at `url`. Read from `$RAGCODE_HOME/cache/update.json` when checked in the last 24 hours (one hour after a failed check, cached with an empty tag); otherwise fetched and written back. A request canceled by the caller is not cached. `""` means unknown |
| `Less(a, b) bool` | `a < b` for `vMAJOR.MINOR.PATCH` tags (leading `v` optional, `+build` metadata ignored). A pre-release is older than its final release; two pre-releases of one version compare equal; a tag that does not parse (a `dev` build) is never older |
| `Run(ctx, source, target, current) (string, error)` | `Install` under `$RAGCODE_HOME/cache/update.lock`. Returns `""` without error when another process holds the lock |
| `Install(ctx, source, target, current) (string, error)` | Installs the release at `source` over `target` when it is newer than `current`; returns the installed version, or `""` when there is nothing newer |
| `Remember(path)`, `Replaced() bool` | `cmd/rag-code-mcp` remembers the installed binary it started from; `Replaced` is true once that file was replaced (new inode or modification time), by an update or a reinstall |
| `RestartNote` | The sentence `tools` appends to every answer while `Replaced` is true, so the agent tells the user to restart the server |
| `AssetName(goos, goarch)` | The goreleaser archive name: `rag-code-mcp_<os>_<arch>.tar.gz`, `.zip` on Windows |

## How Install works

1. **Source.** A directory: it must hold `AssetName(runtime.GOOS,
   runtime.GOARCH)` and `checksums.txt`. Anything else is a URL answering like
   the GitHub latest-release API (`tag_name` and `assets` with
   `browser_download_url`). A tag that is not newer than `current` stops here,
   before any download.
2. **Checksum.** The archive's SHA-256 must match its line in
   `checksums.txt`. A mismatch or a missing line is an error.
3. **Extract.** `rag-code-mcp` (`rag-code-mcp.exe` on Windows) is read from
   the `.tar.gz` or `.zip` and written to a temporary file next to `target`.
4. **Run it.** The new binary must answer `-version` within 10 seconds, with
   a version newer than `current`. A binary built for another platform, a
   broken one or an older one (downgrade) is not installed.
5. **Replace.** On Linux and macOS the file is renamed over `target`; a
   server still running the old binary keeps it until it exits. Windows
   cannot overwrite a running `.exe` but can rename it, so the old one moves
   to `target.old` first (removed by the next update).

Downloads have a 5-minute timeout and a 256 MiB cap.

## Automatic update

When `auto_update` is on, `cmd/rag-code-mcp` runs one update in the
background after the server starts, only when the running process is the
installed binary (`~/.ragcode/bin/rag-code-mcp`): a dev build, a Homebrew
binary or a copy elsewhere is left alone. With the default source it asks
`Latest` first, so GitHub is contacted at most once a day. It logs
`update: installed; restart the MCP clients to use it` or
`update: failed`. The servers that are already running keep the old binary
until the MCP client restarts them. Each of them remembered the installed
binary at start (`Remember`); once it is replaced, every tool answer ends with
`RestartNote` (`RagCode was updated on disk: restart this MCP server ...`),
so the agent asks the user to reconnect the server.

## Configuration

| Key | Effect |
|:---|:---|
| `auto_update` | On by default; `false` turns the automatic update off (`-update` still works) |
| `update_source` | Empty: GitHub. A directory or a URL as in step 1, to test an update without a GitHub release. Environment override: `RAGCODE_UPDATE_SOURCE` |

## Files written

| Path | What |
|:---|:---|
| `$RAGCODE_HOME/cache/update.json` | the cached latest tag and when it was checked |
| `$RAGCODE_HOME/cache/update.lock` | held while one process installs an update |
| `$RAGCODE_HOME/bin/rag-code-mcp[.exe]` | replaced by a newer release |
| `$RAGCODE_HOME/bin/rag-code-mcp.exe.old` | Windows only: the replaced binary |

## Testing

```bash
go test -race ./internal/update/
```

The install tests use a shell script as the release binary and skip on
Windows. A manual end-to-end test: build an installed binary with
`-ldflags "-X main.Version=v2.0.0"` into `$RAGCODE_HOME/bin`, put a newer
archive and its `checksums.txt` (`sha256sum`) in a directory, then start the
server or run `-update` with `RAGCODE_UPDATE_SOURCE=<that directory>`.

## See also

- [cmd/rag-code-mcp](../../cmd/rag-code-mcp/README.md): the `-update` flag and the startup sequence
- [config](../config/README.md): `auto_update`, `update_source`
- [tools](../../tools/README.md): the `update` block of `rag_server_info`

[RagCode](../../README.md) › [internal](../README.md) › config

# config

Package `config` owns the one user-facing configuration file of RagCode,
`~/.ragcode/config.yaml`, and the process logger. It:

- defines `Config` and its built-in defaults;
- finds the global RagCode directory (`$RAGCODE_HOME` or `~/.ragcode`);
- loads the YAML on top of the defaults, writes the file from the embedded
  `default.yaml` when it is missing, applies environment overrides and
  validates the result;
- creates the `slog` text logger and an append-only log file with size-based
  rotation that several server processes can share.

There is one configuration for the whole machine. Nothing per workspace lives
here; per-workspace data is written by the [`engine`](../../engine/README.md)
and the [`store`](../../store/README.md).

## Where it sits

```mermaid
graph LR
    MAIN[cmd/rag-code-mcp] -->|Load, NewLogger, OpenLogFile, HomeDir| CFG[config]
    APP[internal/app] -->|Config, HomeDir| CFG
    INST[internal/install] -->|HomeDir, DefaultPath, Load| CFG
    UN[internal/uninstall] -->|HomeDir| CFG
    ENG[engine] -->|Config, HomeDir| CFG
    TOOLS[tools] -->|Config| CFG
```

- **Called by:** [`cmd/rag-code-mcp`](../../cmd/rag-code-mcp/README.md),
  [`app`](../app/README.md), [`install`](../install/README.md),
  [`uninstall`](../uninstall/README.md), [`engine`](../../engine/README.md),
  [`tools`](../../tools/README.md).
- **Calls:** only the standard library and `gopkg.in/yaml.v3`. It imports no
  other package of the module, so anything can depend on it.

## Files

| File | Contents |
|:---|:---|
| `config.go` | `Config`, `Default`, `HomeDir`, `DefaultPath`, `Load`, environment overrides (`applyEnv`), `Validate`, `Redacted` (a URL with its password shown as `xxxxx`, for messages and logs). Embeds `default.yaml`. |
| `default.yaml` | The commented configuration written to `config.yaml` on first run. Mirrors `Default()` (a test checks it). |
| `log.go` | `NewLogger` and `OpenLogFile` with the rotating `logFile` writer. |

Tests:

| File | Covers |
|:---|:---|
| `config_test.go` | `default.yaml` equals `Default()`; loading a file; a missing file is written from the default; invalid YAML; environment overrides; every `Validate` rule; `HomeDir`/`DefaultPath` with and without `RAGCODE_HOME`; logger levels; log rotation on open, every 1000 writes, failed rotation keeps writing, following a rotation done by another process; old Qdrant keys from v1 are ignored; `embed_threads` default and `0`; the `embed_max_tokens` bounds (256..32768, at most 8192 without `embed_model`); `Redacted` hides only a URL's password. |
| `zz_verify_test.go` | Verification tests: odd inputs (empty file, wrong types, ...) error or load but never panic; path is a directory; missing file with an unwritable parent; environment values on a fresh file are validated; empty environment values do not override; YAML tags are snake_case; exact defaults; logger levels; `OpenLogFile` errors; concurrent writes across rotations; the logger through the log file. |

## Configuration keys

All keys are optional; a missing key keeps the default. Unknown keys are
ignored (so a v1 `config.yaml` with Qdrant settings, or a v2.0 one with
`ollama_base_url` and `ollama_embed`, still loads; those keys no longer do anything).

| YAML key | Go field | Default | Meaning / used by |
|:---|:---|:---|:---|
| `embed_runtime` | `EmbedRuntime` | `auto` | Which llama.cpp build runs the model: `auto`, `cpu` or `vulkan`. `auto` picks Vulkan when an NVIDIA driver and a Vulkan loader are found, else CPU; macOS has one build (Metal). Anything else is invalid. See [`llama`](../llama/README.md). |
| `embed_model` | `EmbedModel` | empty | Absolute path of a `.gguf` embedding model to use instead of the downloaded one. The index names the model after the file name without `.gguf`, then `@` and a 12-character fingerprint of the file (its size and its first and last MiB), e.g. `my-embed@3fa9c01b77d2`, so another file, even under the same name, re-embeds every workspace after a restart. A file replaced while RagCode runs is refused (indexing and search fail with an error naming both labels) until RagCode is restarted or the old file is put back. |
| `llama_server` | `LlamaServer` | empty | Absolute path of a `llama-server` binary to use instead of the downloaded one. |
| `embed_threads` | `EmbedThreads` | `max(2, NumCPU/3)` | CPU threads llama.cpp may use for embeddings (`-t`, `-tb`). `0` lets llama.cpp choose (every performance core). Must not be negative. |
| `embed_max_tokens` | `EmbedMaxTokens` | `8192` | How much of a function, class or section the model reads, in tokens (about 3 bytes of code each, so 8192 tokens are roughly 24 KB). A longer symbol is embedded by its first part only; its stored content is whole. `8192` is the context window of the built-in model. A lower value makes long symbols faster to index and bounds the memory of the embedding server (on one laptop's CPU a 24 KB symbol took 61 s and 1.5 GB; a 12 KB one 23 s and 0.9 GB). Between `256` and `32768`, and at most `8192` without `embed_model`. With your own `embed_model`, set it to that model's context length: a model trained for less is cut to its own limit anyway, but every long symbol then costs one refused request first. Changing it makes the next index run of each workspace re-embed every file. |
| `min_free_memory_mb` | `MinFreeMemoryMB` | `750` | Free memory needed to start the embedding server (measured: the model holds 0.3 GB of RAM on a GPU and up to 0.9 GB on CPU for inputs up to 12 KB, 1.5 GB on CPU for a symbol at the 8192-token limit; a load with under 600 MB free failed). The check covers loading only: on a CPU machine short of memory, lower `embed_max_tokens`, since a long symbol raises the peak. With less, a call that has to start the server answers `low_memory`; a server already running is not checked. `0` turns the check off. Must not be negative. |
| `log_level` | `LogLevel` | `info` | `debug`, `info`, `warn` or `error`. |
| `log_max_size_mb` | `LogMaxSizeMB` | `10` | Size at which `ragcode.log` is rotated. Must be positive. |
| `exclude_patterns` | `ExcludePatterns` | `node_modules`, `vendor`, `target`, `build`, `dist`, `__pycache__` | Directory names skipped while indexing and watching. Hidden directories are always skipped. Setting the key replaces the whole list. |
| `max_files` | `MaxFiles` | `20000` | A workspace whose full walk finds more indexable files is not indexed: the run fails with the count and the tools show it as `index.error`, so a wrong root (a folder of many projects) does not cost hours of embedding. Watcher runs on an indexed workspace are not counted. `0` = no limit. Must not be negative. Read at server start. |
| `allowed_workspace_paths` | `AllowedWorkspacePaths` | empty | When set, only workspaces under one of these directories can be used. Entries must be absolute. Checked by the [`engine`](../../engine/README.md). |
| `auto_index` | `AutoIndex` | `true` | Index a workspace on its own (first use, catch-up at start, watcher). `false` leaves indexing to an explicit `rag_index_workspace`, and no watcher starts. |
| `watch_enabled` | `WatchEnabled` | `true` | Watch indexed workspaces for file changes (only when `auto_index` is also on). |
| `watch_debounce` | `WatchDebounce` | `1s` | Go duration the watcher waits for changes to settle. Must not be negative. |
| `auto_create_ide_rules` | `AutoCreateIDERules` | `true` | Let `rag_index_workspace` write `<workspace>/.ragcode/RAGCODE.md` and the IDE pointer lines. |
| `auto_update` | `AutoUpdate` | `true` | At server start, install a newer release over `~/.ragcode/bin/rag-code-mcp` (GitHub asked at most once a day). See [update](../update/README.md). |
| `update_source` | `UpdateSource` | empty | Where releases come from: empty is GitHub; a directory with the release archive and `checksums.txt`, or a URL answering like the GitHub latest-release API, for testing. |

`embed_threads`, `embed_model` and `llama_server` are commented out in
`default.yaml` on purpose: the written file does not pin a CPU count or a
path, so the defaults keep following the machine.

### Environment overrides

Applied after the file, before validation. An empty variable does not
override.

| Variable | Overrides |
|:---|:---|
| `RAGCODE_EMBED_RUNTIME` | `embed_runtime` |
| `RAGCODE_EMBED_MODEL` | `embed_model` |
| `RAGCODE_LLAMA_SERVER` | `llama_server` |
| `MCP_LOG_LEVEL` | `log_level` |
| `RAGCODE_UPDATE_SOURCE` | `update_source` |
| `RAGCODE_ALLOWED_PATHS` | `allowed_workspace_paths`, as an OS path list (`:` on Linux/macOS, `;` on Windows); blank entries are dropped |
| `RAGCODE_HOME` | not a key: moves the whole global directory (see `HomeDir`) |

Priority, highest first: environment, `config.yaml`, `Default()`. The server's
`-log-level` flag is applied after `Load`, so it wins over all of them.

## Key functions

### `HomeDir() string` and `DefaultPath() string`

`HomeDir` returns `$RAGCODE_HOME` when set, otherwise `<user home>/.ragcode`
(falling back to `.ragcode` in the working directory if the user home cannot
be determined). The result is always absolute: a relative `RAGCODE_HOME` is
resolved against the working directory, because `llama-server` runs from its
own directory and would not find a relative path.
`DefaultPath` is `HomeDir()/config.yaml`. Every other package builds its paths
(`bin/`, `index/`, `logs/`, `roots.json`) from `HomeDir`.

### `Load(path string) (Config, error)`

```go
cfg, err := config.Load(config.DefaultPath())
if err != nil {
    return err // "config: ..." with the file and field
}
```

1. Start from `Default()`.
2. If `path` does not exist, create its directory (`0755`) and write the
   embedded `default.yaml` there (`0644`); the defaults are used as is.
   Otherwise unmarshal the YAML over the defaults.
3. Apply the environment overrides.
4. `Validate`.

Errors are prefixed `config:`: `create dir`, `write default`, `read <path>`,
`parse <path>`, or the first invalid field. On error the partially filled
`Config` is returned too.

### `Validate() error`

Returns the first invalid field: `embed_model` not an absolute path ending in
`.gguf`, `embed_runtime` other than `auto`, `cpu` or `vulkan`, `llama_server`
not an absolute path, unknown `log_level`, negative `embed_threads`,
`embed_max_tokens` outside 256..32768 or over 8192 with the built-in model,
negative `max_files`, negative `min_free_memory_mb`,
non-positive `log_max_size_mb`, negative `watch_debounce`, or a relative
`allowed_workspace_paths` entry.

### `NewLogger(level string, w io.Writer) *slog.Logger`

A `slog` text handler at `level`. An unknown level falls back to `info`
(`Load` has already rejected unknown levels from the file and environment).

## Log file and rotation

`OpenLogFile(path, maxMB)` returns an `io.WriteCloser` used by
[`cmd/rag-code-mcp`](../../cmd/rag-code-mcp/README.md) for
`$RAGCODE_HOME/logs/ragcode.log`.

- On open: if the file is larger than `maxMB`, it is renamed to
  `path + ".1"` (one backup; an older `.1` is replaced), then `path` is
  opened for appending (`0644`).
- Every 1000 writes the current size is checked; over the limit the file is
  closed, rotated and reopened.
- **Several processes share the file.** Before each write the writer checks
  that `path` is still the file it holds open (`os.SameFile`). If another
  server rotated it, this one reopens `path`, so it never keeps appending to
  an unlinked file.
- **Windows:** renaming a file another process holds open fails. During
  writes the rename error is ignored and writing continues to the oversized
  file; the rotation is retried 1000 writes later. The rotation at open is
  not forgiving: `OpenLogFile` returns the error, and the server then logs
  to stderr (`logging to stderr: ...`).
- Writes are serialised with a mutex, so the writer is safe for concurrent
  use within one process.

## Testing

```bash
go test ./internal/config/
```

No build tags. Tests use `t.TempDir()` and `t.Setenv`, so they do not touch
the real `~/.ragcode`.

## See also

- [internal](../README.md)
- [cmd/rag-code-mcp](../../cmd/rag-code-mcp/README.md): loads the config and opens the log
- [engine](../../engine/README.md): `auto_index`, `watch_*`, `allowed_workspace_paths`, `exclude_patterns`
- [embed](../../embed/README.md): `embed_runtime`, `embed_model`, `llama_server`, `embed_threads`, `embed_max_tokens`, `min_free_memory_mb`
- [install](../install/README.md): writes the config on install

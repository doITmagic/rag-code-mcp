# RagCode MCP v3

An MCP server that indexes a code workspace and answers code questions for AI
clients: semantic search, usages, call hierarchy, package exports and file
context. Clean rewrite of v2 with the same features and about half the code.

Separate Go module (`github.com/doITmagic/rag-code-mcp/v3`). It does not import
v2 packages; v2 stays frozen and shippable until v3 replaces it.

## Requirements

- **Ollama 0.40 or later** with the `embeddinggemma-2:270m` model. It is the
  only dependency. The installer can run it in Docker (`-ollama docker`, the
  default), use one already running (`-ollama local`), or leave it to you
  (`-ollama skip`).
- **Hardware:** at least 4 CPU cores, 8 GB RAM and an SSD; 8 cores and 16 GB
  are recommended. A GPU for Ollama speeds up indexing but is not required.

No database server is needed: the index is an embedded SQLite file per
workspace.

## Install

```bash
cd v3
CGO_ENABLED=0 go build -o dist/ ./cmd/...
dist/rag-code-install -y            # add -ollama local|skip, -skip-ide, -gpu=false
```

The installer copies `rag-code-mcp` to `~/.ragcode/bin`, writes
`~/.ragcode/config.yaml`, starts Ollama, pulls the model and registers the
server in the IDE configs it finds. A RagCode v1 install is removed first; its
indexes are not compatible, so projects are indexed again.

`dist/rag-code-install -uninstall -y` removes everything; add `-docker` to also
remove the `ragcode-ollama` container.

## Where data lives

| Path | Content |
|---|---|
| `~/.ragcode/config.yaml` | settings (Ollama URL and model, exclude patterns, watch, log level) |
| `~/.ragcode/index/<collection>/` | the workspace index: `index.db`, `state.json`, `index_status.json`, `index.lock` |
| `~/.ragcode/roots.json` | the workspaces the server knows |
| `<workspace>/.ragcode/RAGCODE.md` | the instructions the AI clients read |

`<collection>` is `ragcode-` plus the first 16 hex digits of the SHA-1 of the
workspace path. Set `RAGCODE_HOME` to move `~/.ragcode`.

## Tools

`rag_search`, `rag_find_usages`, `rag_call_hierarchy`,
`rag_list_package_exports`, `rag_read_file_context`, `rag_index_workspace`,
`rag_server_info`. Each takes `workspace_root`; the client passes it.

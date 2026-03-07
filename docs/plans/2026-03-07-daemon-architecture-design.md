# Daemon Architecture Design — RagCode MCP

**Date:** 2026-03-07
**Status:** Approved
**Branch:** TBD

## Problem

The current architecture ties the MASTER process (Qdrant, Ollama, indexer) to the stdin of the first IDE that launched it.

- IDE1 starts `rag-code-mcp` → becomes MASTER (Stdio + HTTP :3000)
- IDE2 starts `rag-code-mcp` → detects port 3000 occupied → enters PROXY mode (Stdio→HTTP with SSE parsing)
- IDE1 closes → stdin closes → **context canceled → EVERYTHING dies**

Additional problems:
- Complex and fragile SSE parsing (`extractSSEPayload`, sessionid, handshake)
- Port conflicts on :3000
- Indexing stops when IDE closes
- Index progress invisible between instances

## Solution: Daemon + Unix Socket + Thin Adapters

### Principle

A single binary (`rag-code-mcp`) with two modes:
- **No flag** (default) → Stdio Adapter: checks/starts daemon, bridges stdin↔socket
- **`--daemon`** → Daemon: persistent process with Qdrant, Ollama, Engine, MCP Server

### Process Diagram

```
┌──────────┐   ┌──────────┐   ┌──────────┐
│  IDE #1  │   │  IDE #2  │   │  IDE #3  │
└────┬─────┘   └────┬─────┘   └────┬─────┘
     │ stdin/stdout  │ stdin/stdout  │ stdin/stdout
     ▼               ▼               ▼
┌──────────┐   ┌──────────┐   ┌──────────┐
│ Adapter  │   │ Adapter  │   │ Adapter  │
│  (~1MB)  │   │  (~1MB)  │   │  (~1MB)  │
└────┬─────┘   └────┬─────┘   └────┬─────┘
     │               │               │
     └───────┬───────┘───────────────┘
             │ JSON/HTTP over Unix Socket
             │ ~/.ragcode/daemon.sock
             ▼
     ┌───────────────────┐
     │   DAEMON (1 inst) │
     │                   │         ┌────────────────┐
     │  MCP Server       │◄────────│ curl / Python  │
     │  Engine           │  HTTP   │ (optional)     │
     │  Ollama (1 conn)  │ :3000   └────────────────┘
     │  Qdrant (1 conn)  │
     └───────────────────┘
```

## Section 1: General Architecture

### Daemon Files

| File | Purpose |
|------|---------|
| `~/.ragcode/daemon.sock` | Unix domain socket |
| `~/.ragcode/daemon.pid` | PID + daemon process version |
| `~/.ragcode/daemon.lock` | File lock for startup race condition |
| `~/.ragcode/logs/ragcode.log` | Daemon logs |
| `~/.ragcode/registry.json` | Workspace registry |

### Adapter Startup Flow

1. Check `~/.ragcode/daemon.pid` → PID exists?
2. PID alive? (`kill -0 PID`)
3. Socket connect (`dial unix daemon.sock`)?
4. `GET /health` → 200 OK?
5. Version == ours?
6. Any step fails → cleanup + `StartDaemon()`
7. All OK → enter bridge mode

### What Gets Removed Entirely

- `extractSSEPayload()` — SSE parsing
- `PortIsOccupied()` — port scanning
- `KillProcessOnPort()` — kill on port
- `QueryMasterVersion()` via JSON-RPC initialize
- Skill `ragcode-sse` → rewritten as `ragcode-http`

## Section 2: Protocol & Lifecycle

### Communication: HTTP/1.1 over Unix socket, pure JSON

**Request (Adapter → Daemon):**
```http
POST /mcp HTTP/1.1
Content-Type: application/json
X-Workspace-Hint: /home/user/project

{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{...}}
```

**Response (Daemon → Adapter):**
```http
HTTP/1.1 200 OK
Content-Type: application/json

{"jsonrpc":"2.0","id":1,"result":{...}}
```

Zero SSE. Zero `text/event-stream`. Zero sessionid.

### Health Endpoint

```
GET /health → 200 OK
{
  "status": "ok",
  "version": "2.1.54",
  "uptime_seconds": 3600,
  "pid": 12345,
  "ollama": "connected",
  "qdrant": "connected",
  "active_indexing": ["workspace-abc123"]
}
```

### X-Workspace-Hint Header

Each adapter captures CWD (Current Working Directory = IDE workspace) at startup and sends it as the `X-Workspace-Hint` header with every request. The daemon uses it as a fallback when the tool doesn't receive `file_path`.

### PID File Format

```
PID=12345
VERSION=2.1.54
STARTED=2026-03-07T07:05:00+02:00
SOCKET=~/.ragcode/daemon.sock
```

### Daemon Lifecycle

- **Start:** Health checks → Init Engine → Listen socket + HTTP → Write PID → Signal ready
- **Running:** Serves MCP requests concurrently, background indexing, watchers
- **Shutdown (SIGTERM):** Stop watchers → Finalize in-flight requests (5s grace) → Close listeners → Remove socket + PID
- **Crash (kill -9):** Socket + PID remain on disk → stale detection on next adapter startup

### Idle Timeout (optional)

```yaml
daemon:
  idle_timeout: 0  # 0 = run forever (default)
```

## Section 3: Code Restructuring

### New Files

| File | Est. Lines | Role |
|------|-----------|------|
| `internal/daemon/daemon.go` | ~120 | Heavy process — extracted from main.go |
| `internal/daemon/health.go` | ~40 | Health endpoint JSON |
| `internal/daemon/pidfile.go` | ~60 | Write/Read/Remove PID, stale detection |
| `internal/adapter/adapter.go` | ~50 | Bridge stdin ↔ Unix socket (pure JSON) |
| `internal/adapter/lifecycle.go` | ~80 | EnsureDaemon, StartDaemon, StopDaemon |

### Modified Files

| File | Change |
|------|--------|
| `cmd/rag-code-mcp/main.go` | Simplified: 365→~60 lines, only decides mode |
| `.agent/skills/ragcode-sse/` | Rewritten as ragcode-http |
| `ARCHITECTURE.md` | Updated |

### Deleted Files

| File | Reason |
|------|--------|
| `internal/proxy/portutil.go` | Replaced by adapter/lifecycle.go |
| `internal/proxy/proxy.go` | Replaced by adapter/adapter.go |
| `internal/proxy/proxy_test.go` | Tests for deleted code |

### What Does NOT Change

- `internal/service/engine/` — zero changes
- `internal/service/tools/` — zero changes
- `internal/service/search/` — zero changes
- `pkg/indexer/` — zero changes
- `pkg/parser/*` — zero changes
- `pkg/storage/` — zero changes
- `pkg/workspace/` — zero changes
- `internal/config/`, `internal/logger/`, `internal/healthcheck/` — zero changes

### Net impact: ~-50 lines (less total code)

## Section 4: Error Handling, Testing & Migration

### Error Scenarios

| Scenario | Recovery |
|----------|---------|
| Daemon crash | Stale detection → cleanup → restart |
| Ollama down | Existing circuit breaker works |
| Qdrant down | JSON-RPC error → IDE sees the error |
| Socket file deleted | Connect fail → kill PID → restart |
| 2 adapters start simultaneously | flock() on daemon.lock |
| Upgrade (adapter v2, daemon v1) | Kill daemon v1 → start v2 |

### Race Condition Protection

`daemon.lock` with `syscall.Flock(LOCK_EX)` — only one adapter checks/starts the daemon.

### Testing

**New unit tests:**
- `internal/daemon/pidfile_test.go`
- `internal/daemon/health_test.go`
- `internal/adapter/adapter_test.go`
- `internal/adapter/lifecycle_test.go`

**Integration tests:**
- `TestDaemonStartAndBridge`
- `TestMultipleAdapters`
- `TestDaemonSurvivesAdapterDeath`
- `TestDaemonUpgrade`
- `TestStaleDaemonCleanup`

### Migration

1. Implementation on feature branch
2. Merge to dev — zero breaking changes (implicitly backward compatible)
3. Update skills
4. Final cleanup

### New Configuration (config.yaml)

```yaml
daemon:
  socket_path: ""             # default: ~/.ragcode/daemon.sock
  http_host: 127.0.0.1        # bind only to loopback by default (secure local access)
  http_port: 3000             # HTTP on http_host:port; 0 = disabled. For external exposure (e.g. 0.0.0.0) hardening (auth, TLS) is required and must be explicitly opted in.
  idle_timeout: 0             # 0 = run forever
```

# Daemon Architecture Design — RagCode MCP

**Data:** 2026-03-07
**Status:** Aprobat
**Branch:** TBD

## Problema

Arhitectura curentă leagă procesul MASTER (Qdrant, Ollama, indexer) de stdin-ul primului IDE care l-a lansat.

- IDE1 pornește `rag-code-mcp` → devine MASTER (Stdio + HTTP :3000)
- IDE2 pornește `rag-code-mcp` → detect port 3000 ocupat → intră în PROXY (Stdio→HTTP cu SSE parsing)
- IDE1 se închide → stdin se închide → **context canceled → TOTUL moare**

Probleme suplimentare:
- SSE parsing complex și fragil (extractSSEPayload, sessionid, handshake)
- Port conflicts pe :3000
- Indexare se oprește la închiderea IDE-ului
- Index progress invizibil între instanțe

## Soluția: Daemon + Unix Socket + Adaptorii Thin

### Principiu

Un singur binary (`rag-code-mcp`) cu două moduri:
- **Fără flag** (default) → Adapter Stdio: verifică/pornește daemon, bridge stdin↔socket
- **`--daemon`** → Daemon: proces persistent cu Qdrant, Ollama, Engine, MCP Server

### Diagrama proceselor

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
     │  Engine           │  HTTP   │ (opțional)     │
     │  Ollama (1 conn)  │ :3000   └────────────────┘
     │  Qdrant (1 conn)  │
     └───────────────────┘
```

## Secțiunea 1: Arhitectura Generală

### Fișierele daemon-ului

| Fișier | Scop |
|--------|------|
| `~/.ragcode/daemon.sock` | Unix domain socket |
| `~/.ragcode/daemon.pid` | PID + versiunea procesului daemon |
| `~/.ragcode/daemon.lock` | File lock pentru race condition la startup |
| `~/.ragcode/logs/ragcode.log` | Log-uri daemon |
| `~/.ragcode/registry.json` | Workspace registry |

### Fluxul de pornire Adapter

1. Verifică `~/.ragcode/daemon.pid` → PID există?
2. PID viu? (`kill -0 PID`)
3. Socket connect (`dial unix daemon.sock`)?
4. `GET /health` → 200 OK?
5. Versiune == a noastră?
6. Oricare eșuează → cleanup + `StartDaemon()`
7. Toate OK → intră în bridge mode

### Ce se elimină complet

- `extractSSEPayload()` — SSE parsing
- `PortIsOccupied()` — port scanning
- `KillProcessOnPort()` — kill pe port
- `QueryMasterVersion()` via JSON-RPC initialize
- Skill `ragcode-sse` → rescris ca `ragcode-http`

## Secțiunea 2: Protocol & Lifecycle

### Comunicare: HTTP/1.1 peste Unix socket, JSON pur

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

Fiecare adapter captează CWD (Current Working Directory = workspace-ul IDE-ului) la pornire și-l trimite ca header `X-Workspace-Hint` cu fiecare request. Daemon-ul îl folosește ca fallback când tool-ul nu primește `file_path`.

### PID File Format

```
PID=12345
VERSION=2.1.54
STARTED=2026-03-07T07:05:00+02:00
SOCKET=~/.ragcode/daemon.sock
```

### Daemon Lifecycle

- **Start:** Health checks → Init Engine → Listen socket + HTTP → Write PID → Signal ready
- **Running:** Servește cereri MCP concurent, indexare background, watchers
- **Shutdown (SIGTERM):** Stop watchers → Finalizează in-flight (5s grace) → Close listeners → Remove socket + PID
- **Crash (kill -9):** Socket + PID rămân pe disc → stale detection la următorul adapter

### Idle Timeout (opțional)

```yaml
daemon:
  idle_timeout: 0  # 0 = run forever (default)
```

## Secțiunea 3: Restructurarea Codului

### Fișiere noi

| Fișier | Linii ~est. | Rol |
|--------|------------|-----|
| `internal/daemon/daemon.go` | ~120 | Procesul greu — extras din main.go |
| `internal/daemon/health.go` | ~40 | Health endpoint JSON |
| `internal/daemon/pidfile.go` | ~60 | Write/Read/Remove PID, stale detection |
| `internal/adapter/adapter.go` | ~50 | Bridge stdin ↔ Unix socket (JSON pur) |
| `internal/adapter/lifecycle.go` | ~80 | EnsureDaemon, StartDaemon, StopDaemon |

### Fișiere modificate

| Fișier | Schimbare |
|--------|-----------|
| `cmd/rag-code-mcp/main.go` | Simplificat: 365→~60 linii, doar decide mod |
| `.agent/skills/ragcode-sse/` | Rescris ca ragcode-http |
| `ARCHITECTURE.md` | Actualizat |

### Fișiere șterse

| Fișier | Motiv |
|--------|-------|
| `internal/proxy/portutil.go` | Înlocuit de adapter/lifecycle.go |
| `internal/proxy/proxy.go` | Înlocuit de adapter/adapter.go |
| `internal/proxy/proxy_test.go` | Teste pentru cod șters |

### Ce NU se schimbă

- `internal/service/engine/` — zero schimbări
- `internal/service/tools/` — zero schimbări
- `internal/service/search/` — zero schimbări
- `pkg/indexer/` — zero schimbări
- `pkg/parser/*` — zero schimbări
- `pkg/storage/` — zero schimbări
- `pkg/workspace/` — zero schimbări
- `internal/config/`, `internal/logger/`, `internal/healthcheck/` — zero schimbări

### Impact net: ~-50 linii (mai puțin cod total)

## Secțiunea 4: Error Handling, Testing & Migrare

### Scenarii de eroare

| Scenariul | Recovery |
|-----------|----------|
| Daemon crash | Stale detection → cleanup → restart |
| Ollama down | Circuit breaker existent funcționează |
| Qdrant down | Eroare JSON-RPC → IDE vede eroarea |
| Socket file șters | Connect fail → kill PID → restart |
| 2 adaptori pornesc simultan | flock() pe daemon.lock |
| Upgrade (adapter v2, daemon v1) | Kill daemon v1 → start v2 |

### Race condition protection

`daemon.lock` cu `syscall.Flock(LOCK_EX)` — doar un adapter verifică/pornește daemon-ul.

### Testing

**Teste unitare noi:**
- `internal/daemon/pidfile_test.go`
- `internal/daemon/health_test.go`
- `internal/adapter/adapter_test.go`
- `internal/adapter/lifecycle_test.go`

**Teste de integrare:**
- `TestDaemonStartAndBridge`
- `TestMultipleAdapters`
- `TestDaemonSurvivesAdapterDeath`
- `TestDaemonUpgrade`
- `TestStaleDaemonCleanup`

### Migrare

1. Implementare pe feature branch
2. Merge pe dev — zero breaking changes (implicit backward compatible)
3. Update skills
4. Cleanup final

### Configurare nouă (config.yaml)

```yaml
daemon:
  socket_path: ""             # default: ~/.ragcode/daemon.sock
  http_host: 127.0.0.1        # bind only to loopback by default (secure local access)
  http_port: 3000             # HTTP on http_host:port; 0 = dezactivat. Pentru expunere externă (ex. 0.0.0.0) e necesar hardening (auth, TLS) și opt-in explicit.
  idle_timeout: 0             # 0 = run forever
```

# Daemon Architecture Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Replace the fragile Stdio+SSE proxy architecture with a persistent Unix socket daemon + thin Stdio adapters, ensuring one heavy process serves all IDEs independently.

**Architecture:** Single binary with two modes: `--daemon` (heavy backend on Unix socket + optional HTTP) and default (thin stdin↔socket bridge). Daemon survives IDE restarts. Zero SSE — JSON-only communication.

**Tech Stack:** Go 1.24, `net` (Unix socket), `net/http`, `os/exec` (daemon fork), `syscall` (setsid, flock), MCP Go SDK `mcp.NewStreamableHTTPHandler`

**Design Doc:** `docs/plans/2026-03-07-daemon-architecture-design.md`

---

### Task 1: PID File Management

**Files:**
- Create: `internal/daemon/pidfile.go`
- Test: `internal/daemon/pidfile_test.go`

**Step 1: Write the failing tests**

```go
// internal/daemon/pidfile_test.go
package daemon

import (
    "os"
    "path/filepath"
    "testing"

    "github.com/stretchr/testify/assert"
    "github.com/stretchr/testify/require"
)

func TestWriteAndReadPID(t *testing.T) {
    dir := t.TempDir()
    path := filepath.Join(dir, "daemon.pid")

    err := WritePID(path, os.Getpid(), "2.1.54")
    require.NoError(t, err)

    info, err := ReadPID(path)
    require.NoError(t, err)
    assert.Equal(t, os.Getpid(), info.PID)
    assert.Equal(t, "2.1.54", info.Version)
    assert.NotEmpty(t, info.StartedAt)
}

func TestReadPID_NotExists(t *testing.T) {
    _, err := ReadPID("/nonexistent/daemon.pid")
    assert.Error(t, err)
}

func TestRemovePID(t *testing.T) {
    dir := t.TempDir()
    path := filepath.Join(dir, "daemon.pid")

    _ = WritePID(path, 12345, "1.0.0")
    err := RemovePID(path)
    assert.NoError(t, err)

    _, err = os.Stat(path)
    assert.True(t, os.IsNotExist(err))
}

func TestIsProcessAlive(t *testing.T) {
    assert.True(t, IsProcessAlive(os.Getpid()))
    assert.False(t, IsProcessAlive(999999999))
}
```

**Step 2: Run tests to verify they fail**

Run: `cd /home/razvan/go/src/github.com/doITmagic/rag-code-mcp && go test ./internal/daemon/ -v -run TestWriteAndReadPID`
Expected: FAIL — package doesn't exist yet

**Step 3: Write minimal implementation**

```go
// internal/daemon/pidfile.go
package daemon

import (
    "fmt"
    "os"
    "strconv"
    "strings"
    "syscall"
    "time"
)

// PIDInfo contains daemon process metadata read from the PID file.
type PIDInfo struct {
    PID       int
    Version   string
    StartedAt string
}

// WritePID writes daemon metadata to the PID file.
func WritePID(path string, pid int, version string) error {
    content := fmt.Sprintf("PID=%d\nVERSION=%s\nSTARTED=%s\n",
        pid, version, time.Now().Format(time.RFC3339))
    return os.WriteFile(path, []byte(content), 0644)
}

// ReadPID reads and parses the PID file.
func ReadPID(path string) (*PIDInfo, error) {
    data, err := os.ReadFile(path)
    if err != nil {
        return nil, err
    }

    info := &PIDInfo{}
    for _, line := range strings.Split(string(data), "\n") {
        parts := strings.SplitN(line, "=", 2)
        if len(parts) != 2 {
            continue
        }
        switch parts[0] {
        case "PID":
            info.PID, _ = strconv.Atoi(parts[1])
        case "VERSION":
            info.Version = parts[1]
        case "STARTED":
            info.StartedAt = parts[1]
        }
    }

    if info.PID == 0 {
        return nil, fmt.Errorf("invalid PID file: no PID found")
    }
    return info, nil
}

// RemovePID deletes the PID file.
func RemovePID(path string) error {
    return os.Remove(path)
}

// IsProcessAlive checks if a process with the given PID is running.
func IsProcessAlive(pid int) bool {
    process, err := os.FindProcess(pid)
    if err != nil {
        return false
    }
    return process.Signal(syscall.Signal(0)) == nil
}
```

**Step 4: Run tests to verify they pass**

Run: `go test ./internal/daemon/ -v`
Expected: PASS (all 4 tests)

**Step 5: Commit**

```bash
git add internal/daemon/pidfile.go internal/daemon/pidfile_test.go
git commit -m "feat(daemon): add PID file management (write, read, remove, alive check)"
```

---

### Task 2: Health Endpoint

**Files:**
- Create: `internal/daemon/health.go`
- Test: `internal/daemon/health_test.go`

**Step 1: Write the failing tests**

```go
// internal/daemon/health_test.go
package daemon

import (
    "encoding/json"
    "net/http"
    "net/http/httptest"
    "testing"
    "time"

    "github.com/stretchr/testify/assert"
    "github.com/stretchr/testify/require"
)

func TestHealthHandler_ReturnsOK(t *testing.T) {
    handler := HealthHandler("2.1.54", time.Now())
    req := httptest.NewRequest("GET", "/health", nil)
    w := httptest.NewRecorder()

    handler.ServeHTTP(w, req)

    assert.Equal(t, http.StatusOK, w.Code)
    assert.Equal(t, "application/json", w.Header().Get("Content-Type"))

    var resp HealthResponse
    err := json.Unmarshal(w.Body.Bytes(), &resp)
    require.NoError(t, err)
    assert.Equal(t, "ok", resp.Status)
    assert.Equal(t, "2.1.54", resp.Version)
    assert.Greater(t, resp.PID, 0)
}

func TestHealthHandler_UptimeIncreases(t *testing.T) {
    startTime := time.Now().Add(-60 * time.Second)
    handler := HealthHandler("1.0.0", startTime)
    req := httptest.NewRequest("GET", "/health", nil)
    w := httptest.NewRecorder()

    handler.ServeHTTP(w, req)

    var resp HealthResponse
    _ = json.Unmarshal(w.Body.Bytes(), &resp)
    assert.GreaterOrEqual(t, resp.UptimeSeconds, 60)
}
```

**Step 2: Run tests — expected FAIL**

Run: `go test ./internal/daemon/ -v -run TestHealth`

**Step 3: Write implementation**

```go
// internal/daemon/health.go
package daemon

import (
    "encoding/json"
    "net/http"
    "os"
    "time"
)

// HealthResponse is the JSON payload returned by the health endpoint.
type HealthResponse struct {
    Status        string `json:"status"`
    Version       string `json:"version"`
    UptimeSeconds int    `json:"uptime_seconds"`
    PID           int    `json:"pid"`
}

// HealthHandler returns an http.HandlerFunc that reports daemon health.
func HealthHandler(version string, startTime time.Time) http.HandlerFunc {
    return func(w http.ResponseWriter, r *http.Request) {
        resp := HealthResponse{
            Status:        "ok",
            Version:       version,
            UptimeSeconds: int(time.Since(startTime).Seconds()),
            PID:           os.Getpid(),
        }
        w.Header().Set("Content-Type", "application/json")
        json.NewEncoder(w).Encode(resp)
    }
}
```

**Step 4: Run tests — expected PASS**

Run: `go test ./internal/daemon/ -v -run TestHealth`

**Step 5: Commit**

```bash
git add internal/daemon/health.go internal/daemon/health_test.go
git commit -m "feat(daemon): add health endpoint (version, uptime, PID)"
```

---

### Task 3: Daemon Server (Unix Socket + HTTP)

**Files:**
- Create: `internal/daemon/server.go`
- Test: `internal/daemon/server_test.go`

**Step 1: Write tests** — test that daemon listens on Unix socket, responds to /health, writes PID file, and cleans up on context cancellation.

**Step 2: Implement** `ListenAndServe(ctx, ListenConfig)` — creates Unix socket listener, optional TCP listener, serves mux with /health + MCP handler, blocks on ctx.Done, cleans up socket+PID on exit.

**Step 3: Run tests, commit**

```bash
git commit -m "feat(daemon): add Unix socket + HTTP listener with lifecycle management"
```

---

### Task 4: Stdio Adapter Bridge

**Files:**
- Create: `internal/adapter/adapter.go`
- Test: `internal/adapter/adapter_test.go`

**Step 1: Write tests** — test JSON-RPC forwarding through Unix socket, X-Workspace-Hint header, error handling for daemon unreachable.

**Step 2: Implement** `RunBridge(ctx, socketPath, stdin, stdout, workspaceHint)` — reads stdin line-by-line, POST to daemon via Unix socket, writes JSON response to stdout. Zero SSE.

**Step 3: Run tests, commit**

```bash
git commit -m "feat(adapter): thin Stdio↔Unix socket bridge with JSON-only protocol"
```

---

### Task 5: Adapter Lifecycle (EnsureDaemon)

**Files:**
- Create: `internal/adapter/lifecycle.go`
- Test: `internal/adapter/lifecycle_test.go`

**Step 1: Write tests** — test IsDaemonRunning (no PID, stale PID, stale socket), CleanupStaleFiles, version comparison.

**Step 2: Implement** `IsDaemonRunning`, `StartDaemon` (fork with setsid, poll /health), `StopDaemon` (SIGTERM), `CleanupStaleFiles`.

**Step 3: Run tests, commit**

```bash
git commit -m "feat(adapter): daemon lifecycle management (detect, start, stop, cleanup)"
```

---

### Task 6: Wire Daemon Mode in main.go

**Files:**
- Create: `internal/daemon/run.go` (full daemon startup: config, health, LLM, Qdrant, Engine, MCP, listeners)
- Modify: `cmd/rag-code-mcp/main.go` (simplify to ~60 lines: daemon flag → daemon.Run or adapter mode)

**Step 1:** Extract heavy init from main.go lines 122-301 into `daemon.Run(version, commit, date, configPath, httpPort)`

**Step 2:** Rewrite main.go:
```go
if *daemonFlag {
    daemon.Run(...)
} else {
    runAdapter(...)  // EnsureDaemon + RunBridge
}
```

**Step 3:** Run `go test ./... -short` — all existing tests must PASS

**Step 4:** Manual integration test: build, start daemon, echo JSON-RPC via adapter

**Step 5: Commit**

```bash
git commit -m "feat: wire daemon+adapter architecture in main.go — replaces stdio+SSE"
```

---

### Task 7: Delete Old Proxy Code

**Files:**
- Delete: `internal/proxy/portutil.go`
- Delete: `internal/proxy/proxy.go`
- Delete: `internal/proxy/proxy_test.go`

**Step 1:** `grep -r "internal/proxy" --include="*.go" .` — verify no external imports

**Step 2:** `rm -rf internal/proxy/`

**Step 3:** `go test ./... -short` — PASS, no compilation errors

**Step 4: Commit**

```bash
git commit -m "chore: remove legacy proxy code (SSE parsing, port detection, kill-on-port)"
```

---

### Task 8: Update Skill ragcode-sse → ragcode-http

**Files:**
- Modify: `.agent/skills/ragcode-sse/SKILL.md`

Rewrite as simple JSON POST instructions. Remove all SSE, session, handshake references. Show curl one-liner + Python 5-liner.

```bash
git commit -m "docs: rewrite ragcode-sse skill as ragcode-http (JSON-only, no SSE)"
```

---

### Task 9: Update Documentation

**Files:**
- Modify: `ARCHITECTURE.md`
- Modify: `README.md`
- Modify: `QUICKSTART.md`

Update to reflect daemon + adapter architecture. Remove SSE references.

```bash
git commit -m "docs: update architecture and quickstart for daemon+adapter model"
```

---

### Task 10: Integration Tests

**Files:**
- Create: `tests/daemon_integration_test.go`

Tests:
- `TestDaemonStartAndToolsList` — full flow
- `TestMultipleAdaptersConcurrent` — 3 bridges in parallel
- `TestDaemonSurvivesAdapterEOF` — adapter dies, daemon stays
- `TestHealthEndpointReturnsVersion` — health check via socket

```bash
git commit -m "test: add daemon architecture integration tests"
```

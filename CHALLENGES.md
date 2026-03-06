# 🏆 RagCode MCP — Engineering Challenges & Solutions

> This document describes real technical challenges encountered during RagCode MCP development and the innovative solutions implemented. Each challenge represents a production scenario that required creative engineering thinking.

---

## 1. 🔄 Model Eviction in Ollama — "The Disappearing Embeddings Problem"

**Status:** ✅ Solved (v2.2.0 — branch `feature/ollama-native-client`)

### The Problem

In a real development environment, a programmer uses **multiple applications simultaneously** that communicate with Ollama:
- **The IDE** (Cursor, VS Code + Continue) requests chat/code models (e.g., `qwen2.5-coder:7b`, `deepseek-r1:8b`)
- **RagCode MCP** requests an embedding model (e.g., `qwen3-embedding:0.6b`)
- Other tools (chatbots, model experimentation)

Ollama manages GPU/RAM memory with an **eviction mechanism**: when loading a new model, it may unload the previous model from memory. This means:

1. RagCode starts indexing with `qwen3-embedding:0.6b` ✅
2. The user opens a chat in their IDE → Ollama loads `deepseek-r1:8b`
3. Ollama **unloads** `qwen3-embedding:0.6b` from memory to make room
4. RagCode sends the next embedding request → **cold start of 10-60s**
5. The 30s timeout expires → `context deadline exceeded` ❌
6. The circuit breaker trips, indexing fails

### Diagnosis

Using the new native Ollama client (`github.com/ollama/ollama/api`), we could query directly:
```go
ps, _ := client.ListRunning(ctx)  // → (no models currently loaded in memory)
```
Confirming that the embedding model had been completely evicted from memory.

### The Solution: Three Levels of Protection

#### Level 1: `keep_alive` — Prevention Through Retention
Every embedding request now includes `keep_alive: 30m`, instructing Ollama to keep the model in memory for **30 minutes** after the last request, even if other models are requested:

```go
resp, err := p.client.Embed(ctx, &api.EmbedRequest{
    Model:     p.embedName,
    Input:     text,
    KeepAlive: &p.keepAlive,  // 30 minutes
})
```

#### Level 2: `Warmup()` — Pre-loading at Startup
At server startup, the embedding model is proactively pre-loaded with a generous 2-minute timeout:

```go
// main.go — at startup, before any indexing
if err := ollamaProvider.Warmup(context.Background()); err != nil {
    logger.Warn("Warmup failed: %v", err)
}
```

The warmup makes a real embedding request (the word `"warmup"`) that forces Ollama to load the model, and caches the resulting dimension.

#### Level 3: `EnsureLoaded()` — Active Check Before Indexing
Before starting any indexing session, we check whether the model is **actually loaded** using the native `ListRunning()` API:

```go
func (p *OllamaLLMProvider) EnsureLoaded(ctx context.Context) error {
    if p.IsModelLoaded(ctx) {
        return nil  // Model is in memory, proceed
    }
    // Model was evicted — reload it
    log.Printf("[WARN] 🔄 Model '%s' NOT loaded — reloading...", p.embedName)
    return p.Warmup(ctx)
}
```

This check runs:
- **Before every indexing batch** (in `IndexWorkspace`)
- **At every circuit breaker trigger** (after 2 consecutive embed errors)

### Result

| Scenario | Before | After |
|----------|--------|-------|
| Cold start at indexing | ❌ Timeout → total failure | ✅ Warmup 2min → success |
| Another model loaded during indexing | ❌ Embed fails → circuit breaker → abort | ✅ EnsureLoaded detects, reloads → continues |
| Indexing after 30min pause | ❌ Model unloaded → errors | ✅ keep_alive=30m keeps model loaded |

### Key Innovation
Compared to the standard implementation (langchaingo), which treats Ollama as a generic API without awareness of the process's internal state, **RagCode uses the native Ollama client** to have complete visibility into:
- What models are **actually loaded** in memory (`ListRunning`)
- Service health (`Heartbeat` natively)
- Embedding dimensions directly from model metadata (`/api/show → model_info`)
- Explicit control over memory retention (`keep_alive`)

---

## 2. 🔒 Silent Deadlocks in Indexing Goroutines

**Status:** ✅ Solved (v2.1.x)

### The Problem
Indexing goroutines would block indefinitely when Ollama became unresponsive. `CreateEmbedding()` from langchaingo did not properly propagate `context.WithTimeout`, resulting in goroutine leaks and silent deadlocks — no logs, no errors, just 0% CPU and frozen indexing.

### The Solution
1. **Watchdog with stall detection**: A dedicated goroutine monitors indexing progress every 30s. If no progress is made, it logs a warning, and after 90s triggers a goroutine dump
2. **Circuit breaker**: After 2 consecutive embed failures, it checks Ollama health and attempts automatic restart
3. **Ollama auto-recovery**: If Ollama is unresponsive, it attempts `systemctl restart ollama`, then falls back to `ollama serve`

### Key Innovation
The system self-heals without user intervention — it detects deadlocks, restarts Ollama, and resumes indexing from where it left off.

---

## 3. 🎯 Embedding Dimension Discovery — "The Moving Target Problem"

**Status:** ✅ Solved (v2.2.0)

### The Problem
Each embedding model has a different vector dimension (384, 768, 1024, 2560, 4096...). Qdrant requires the **exact** dimension when creating a collection. The previous solution was a hardcoded table with ~30 models — fragile, incomplete, and requiring manual updates for every new model.

### The Solution: Dynamic Dimension Discovery
We completely eliminated the hardcoded table and replaced it with direct Ollama API queries:

```go
// Query /api/show for model_info
showResp, _ := p.client.Show(ctx, &api.ShowRequest{Model: p.embedName})

// Search for "*.embedding_length" in model metadata
for key, val := range showResp.ModelInfo {
    if strings.HasSuffix(key, "embedding_length") {
        return uint64(val.(float64))  // e.g., "qwen3.embedding_length" → 1024
    }
}
```

Works with **any model**, including custom or new models, with zero code updates.

### Backup: Probe Embedding
If `/api/show` doesn't return the dimension (very exotic models), a test embedding is generated and the vectors are counted:
```go
vec, _ := p.Embed(ctx, "probe")
dim = uint64(len(vec))  // Guaranteed correct
```

---

## 4. ⚡ Resource Starvation — "The Freezing PC Problem"

**Status:** ✅ Solved (v2.1.x)

### The Problem
When 2+ large projects were indexed simultaneously, the system consumed 100% CPU and all available memory, causing the PC to completely freeze. The IDE, browser, and even the mouse became unresponsive.

### The Solution
1. **Global Semaphore**: A global semaphore limits the total number of concurrent workers to `max(2, NumCPU/4)`, regardless of how many projects are being indexed
2. **GOMAXPROCS Capping**: At startup, we limit the Go scheduler to 75% of CPUs
3. **Throttle between embeddings**: 150ms pause between each embed request, preventing Ollama saturation
4. **Single worker per batch**: Ollama processes embeddings serially — multiple workers only increase latency

---

## 5. 🔧 Migration from LangchainGo to Native Ollama Client

**Status:** ✅ Completed (v2.2.0)

### The Problem
Communication with Ollama was done through `github.com/tmc/langchaingo` (v0.1.14), a generic LLM wrapper that:
- Did not expose native Ollama functions (heartbeat, list running, model management)
- Did not offer granular HTTP transport-level timeout control
- Added a layer of indirection that made debugging difficult
- Contributed to the deadlock bug — `CreateEmbedding` did not properly propagate context cancellation

### The Solution
Complete migration to the official **`github.com/ollama/ollama/api`** package:

| Function | What It Provides | Use in RagCode |
|----------|-----------------|----------------|
| `Client.Heartbeat(ctx)` | Native liveness check | Health check in indexing watchdog |
| `Client.Embed(ctx, req)` | Direct embeddings, no intermediary | Replaces langchaingo `CreateEmbedding` |
| `Client.ListRunning(ctx)` | Active processes/models in memory | Model eviction detection |
| `Client.Show(ctx, req)` | Complete model metadata | Dynamic dimension discovery |
| `NewClient(url, httpClient)` | Custom HTTP transport | Zero-effort timeout configuration |

### Concrete Benefits
1. **Native resilience** — `Heartbeat()` is an official ping, not an HTTP hack on `/api/tags`
2. **Correct context propagation** — the official client respects `context.WithTimeout` at HTTP level
3. **Eliminated langchaingo dependency** — a massive dependency (~50+ sub-dependencies) removed
4. **HTTP transport control** — custom `http.Client` with configurable timeouts, keep-alive, etc.
5. **Batch embedding** — `EmbedRequest` supports `Input` as any type (native batch), not just single text

---

## 6. 🧠 Ollama Concurrent Runners — "The OOM Problem on 8GB Systems"

**Status:** ✅ Solved (v2.2.0)

### The Problem
When the indexer triggers multiple concurrent embedding requests to Ollama, Ollama's default behavior is to spawn multiple `ollama runner` processes to handle the concurrency. Each runner uses around ~1.4GB of RAM. On systems with 8GB or less RAM, launching 4-5 runners results in catastrophic Out Of Memory (OOM) freezing the entire PC.

### The Solution: Dynamic Memory-Aware Concurrency
Instead of relying strictly on CPU counts for indexing worker semaphores, the Go indexer now natively checks `/proc/meminfo` on Linux to determine total system RAM at runtime.
1. If system memory is <= 8GB, the global indexing semaphore is strictly capped at `cap(globalIndexSemaphore) = 1`.
2. This strictly limits client-side requests, ensuring Ollama never receives concurrent requests for the same model, bypassing the need to configure `OLLAMA_NUM_PARALLEL=1` server-side.

---

*This document will be updated as we encounter and solve new technical challenges.*

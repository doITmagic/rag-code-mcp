# Indexing Throughput — Optimization Plan

**Goal:** Cut wall-clock indexing time on a CPU-only embedder, where RagCode currently spends more time sleeping and waiting on HTTP round-trips than computing.

**Architecture:** Three independent changes to the embed path in `pkg/indexer/service.go` and `pkg/llm`, plus a global indexing queue in `internal/service/engine`. Each is shippable on its own; they are listed in order of gain per line changed.

**Tech Stack:** Go, Ollama `/api/embed`, existing worker-pool in `IndexItems`

---

## Measurements that motivated this

Taken on 2026-09-16, 16-core CPU, 60 GB RAM, **no NVIDIA GPU** (`ollama ps` reports `size_vram: 0`, so `qwen3-embedding:0.6b` Q8 runs entirely on CPU).

| Observation | Value |
|---|---|
| Ollama embed latency (`/api/embed`, one symbol) | 81–300 ms |
| Fixed sleep after every successful embed | 150 ms |
| Embed workers | 1 |
| Symbols per HTTP request | 1 |
| `rag-code-mcp` RSS (3 processes) | 47 + 27 + 27 MB |
| `rag-code-mcp` CPU while indexing | ~0% |
| Ollama CPU / RSS | ~467% / 1505 MB |
| Throughput, single workspace | ~70 s per file |
| Throughput, two workspaces competing | ~2 min per file |

The server itself is not the bottleneck: it idles at 0% CPU and ~100 MB while Ollama saturates several cores. Every lever below targets how work is *handed to* Ollama, not the indexing logic.

---

## Task 1: Remove the fixed 150 ms sleep

**Files:** `pkg/indexer/service.go:550`

Today every successful embed is followed by:

```go
time.Sleep(150 * time.Millisecond)
```

Against an 81–300 ms call this is 50–65% of the loop spent idle. The comment claims it is "negligible vs total indexing time" — true for a GPU embedder, false here.

The sleep exists to keep Ollama from freezing under sustained load, which is a real failure mode, so do not simply delete it — make it reactive:

- no pause on success
- pause (and back off) only after an embed error, next to the existing `consecutiveFailures` counter, which already tracks exactly this condition

**Expected gain:** close to 2x on CPU. Largest gain per line changed in this document.

**Verify:** index the same workspace before and after, compare `elapsed` in `.ragcode/index_status.json`.

---

## Task 2: Batch embeds

**Files:** `pkg/llm/provider.go:22`, `pkg/llm/ollama.go:170`, `pkg/indexer/service.go:535`

`Embed(ctx, text)` sends one symbol per HTTP request. Ollama's `/api/embed` accepts an array of inputs, so a 32-symbol batch removes 31 round-trips and lets Ollama schedule the work as one unit.

- add `EmbedBatch(ctx context.Context, texts []string) ([][]float64, error)` to the `Embedder` interface
- implement it in `OllamaLLMProvider` over `api.EmbedRequest` with a slice input
- keep `Embed` as a one-element wrapper so existing call sites and the retry wrapper keep working
- in the worker loop, accumulate symbols up to a batch size (start at 16–32, make it configurable) and flush on batch-full or job-channel-drained

**Note:** this is the biggest structural win but also the only task here that changes an interface. Do it after Task 1 so the gain is measurable in isolation.

---

## Task 3: Make the worker count configurable

**Files:** `pkg/indexer/service.go:485`, `internal/config/`

`numWorkers := 1` is hard-coded, justified by a comment at `service.go:232` claiming "Embed is serial in Ollama anyway". Ollama already uses several cores per request, so raw parallelism will not multiply throughput — but the per-symbol work that is *not* embedding (payload construction, `float64`→`float32` conversion, Qdrant upsert) currently blocks the next embed.

Expose the count as config, default 2, and measure. On a CPU-only host expect a modest gain; more than 3 workers will simply contend for the same cores.

---

## Task 4: Serialize indexing across workspaces

**Files:** `internal/service/engine/engine.go` (near the existing warning at `engine.go:925`)

The engine already detects the problem and only warns:

> `⚠️ %d workspaces indexing simultaneously — Ollama requests will serialize implicitly`

Two concurrent workspaces halve each other's rate (measured: 70 s → 2 min per file) without improving total throughput, because they serialize inside Ollama regardless. A global queue — one workspace indexing at a time, the rest waiting — gives the same total time while keeping per-workspace progress legible instead of making every workspace look stalled.

---

## Out of scope

- **Switching embedding model.** A smaller model (e.g. `all-minilm`) would be far faster on CPU but changes vector dimensions and forces a full re-index of every workspace. `StableEmbeddingModel` is deliberately pinned in `internal/config/config.go`.
- **GPU.** No NVIDIA device on this host; nothing to configure.

---

## Related fixes already applied

Found while measuring, fixed on this branch rather than deferred:

- Index status writes were throttled by file count (every 10 files), which left `index_status.json` reading `processed: 0` for minutes on a slow embedder. Now throttled by time (2 s).
- `AppendSearchMetric` created `.ragcode/` on write. Since `.ragcode` is itself a workspace marker, a mis-resolved root planted a false marker — test runs were creating `.ragcode` directories inside the source tree. It now writes only into an existing `.ragcode` and refuses relative roots.
- `DefaultWorkspaceDetectionMarkers` and `WorkspaceConfig.DetectionMarkers` in `internal/config/config.go` were a second, dead copy of the marker list; the live one is `detector.DefaultOptions()`. Removed, along with the unread `detection_markers` key in `default.yaml`.

# Multi-Collection Indexing Overhaul — Design Document

**Date:** 2026-03-07  
**Status:** Approved  
**Scope:** `pkg/indexer/`, `internal/service/engine/`

---

## Problem

### Bug 1 — Progress inaccurat (`last_percent`)

`state.json` conține câmpul `last_percent` care este **per-limbaj**, nu global.
La fiecare limbaj nou, progresul se resetează de la 0 la 100, independent.

**Fluxul buggy:**
```
engine.IndexWorkspace:
  state.json ← last_percent=1         (start global)

  lang="md":
    indexer ← LoadState → last_percent=1
    procesează fișiere → last_percent=46, 87, 100 (salvat periodic)
    final → state.json: last_percent=100

  lang="go":
    indexer ← LoadState → last_percent=100  (preia valoarea de la md!)
    procesează fișiere → last_percent=3, 5, 8... (RESET!)
    search vede 5% → "indexare întreruptă" fals pozitiv
```

**Efecte observate:**
- Căutarea refuza indexarea la >80% md cu mesaj de "indexare în progres" fără procent real
- `last_percent` se reseta între limbaje confuzând logica de auto-resume
- `progressStore` in-memory era corect dar nu persista suficient de des pe disk

### Bug 2 — Goroutine complexity nedebuggabilă

`globalIndexSemaphore` (package-level var) creează o concurență greu de urmărit:
- N file workers per apel `IndexWorkspace`
- 1 periodic save goroutine per apel (5 limbaje → 5 goroutine secvențiale)
- 1 stall watchdog goroutine per apel
- Workers partajează semaphore-ul global → comportament non-determinist dacă 2 workspace-uri indexează simultan

Deoarece `IndexItems` are `numWorkers=1` (embed-ul e serial la Ollama), paralelismul file workers nu aduce câștig real de performanță, dar adaugă complexitate maximă.

---

## Soluție

### Principii de design

1. **O singură sursă de adevăr pentru progres** — `index_status.json` (nu `state.json`)
2. **3 goroutine fixe** — indiferent de câte limbaje există
3. **1 workspace la un moment dat** — simplu, determinist, ușor de depanat
4. **Verbosity prin log levels** — per-fișier la Debug, per-limbaj la Info, erori la Warn/Error

### Arhitectura nouă

```
StartIndexingAsync(ws):   ← 1 goroutine
  1. Pre-scan ALL languages → total_files_global (sumă)
  2. for lang in languages:          ← secvențial
       for file in lang_files:       ← secvențial (for loop simplu, fără pool)
         IndexFile(file)             ← AST → embed → Qdrant
         progress++
         lang_pct  = progress_in_lang / lang_files * 100
         global_pct = progress_total / total_files * 100
         ← debounced write (500ms)

save goroutine (1 singur):   ← debounced 500ms
  index_status.json ← global_pct + per-lang breakdown

watchdog goroutine (1 singur):   ← 60s interval
  dacă nu embed activity → Warn + restart Ollama dacă necesar
```

### Fișiere de stare

| Fișier | Rol | Câmpuri relevante |
|---|---|---|
| `state.json` | File tracking (modtime/size) | `files{}` — **fără `last_percent`** |
| `index_status.json` | Progress tracking | `global_percent`, `languages{}`, `state`, `current_language` |

**`index_status.json` format:**
```json
{
  "workspace_id": "abc123",
  "workspace_root": "/home/user/project",
  "state": "running",
  "global_percent": 47,
  "current_language": "go",
  "languages_queued": ["md", "go", "python", "php", "html"],
  "languages": {
    "md": { "done": 120, "total": 120, "percent": 100, "state": "completed" },
    "go": { "done": 234, "total": 500, "percent": 46,  "state": "running"   },
    "py": { "done": 0,   "total": 89,  "percent": 0,   "state": "pending"   }
  },
  "started_at": "2026-03-07T12:00:00Z",
  "updated_at": "2026-03-07T12:05:23Z",
  "error": ""
}
```

### Serialitate workspace-uri

- `indexingJobs sync.Map` rămâne pentru a detecta job activ
- Dacă se cere indexare pentru un workspace deja activ → respins imediat (comportament existent)
- Dacă se cere indexare pentru alt workspace când unul e activ → respins cu mesaj clar (`ErrIndexingBusy`)
- Nu există coadă (YAGNI) — utilizatorul poate re-triggera după terminare

### Standard de logging

**Format:** `[IDX] ws=<name> lang=<lang> [<n>/<total>] <file> (<lang_pct>%) global=<global_pct>%`

| Nivel | Conținut | Default vizibil |
|---|---|---|
| `Debug` | Per fișier processat | ❌ (MCP_LOG_LEVEL=debug) |
| `Info` | Start/Done limbaj, global complete | ✅ |
| `Warn` | Eroare fișier individual, stall detectat | ✅ |
| `Error` | Eșec limbaj, Ollama/Qdrant down | ✅ |

**Exemple Info (default):**
```
[IDX] ws=rag-code-mcp  ▶ starting 5 languages (total files: 1247)
[IDX] ws=rag-code-mcp lang=md     ✅ DONE 120 files in 0m45s (global=10%)
[IDX] ws=rag-code-mcp lang=go     ✅ DONE 500 files in 4m12s (global=50%)
[IDX] ws=rag-code-mcp  ✅ COMPLETE all 5 languages in 8m42s
```

**Exemple Debug (MCP_LOG_LEVEL=debug):**
```
[IDX] ws=rag-code-mcp lang=go [  1/500] main.go     ( 0%) global=10%
[IDX] ws=rag-code-mcp lang=go [234/500] engine.go   (46%) global=23%
```

---

## Ce NU se schimbă

- `globalIndexSemaphore` — **eliminat** (nu aduce beneficiu real cu embed serial)
- `IndexItems` numWorkers=1 — rămâne (Ollama e serial oricum)
- Circuit breaker Ollama — rămâne (util)
- `EnsureLoaded` la start limbaj — rămâne
- Periodic state.json save (file tracking) — rămâne

## Ce se schimbă

| Componentă | Schimbare |
|---|---|
| `pkg/indexer/service.go` | Elimină `globalIndexSemaphore` + file worker pool → for loop simplu |
| `pkg/indexer/service.go` | Elimină `state.SetLastPercent()` din file processing |
| `pkg/indexer/state.go` | Elimină câmpul `LastPercent` din `State` struct |
| `internal/service/engine/index_progress.go` | Adaugă `global_percent` calculat + flush debounced la `index_status.json` |
| `internal/service/engine/engine.go` | Pre-scan total files înainte de loop; search logic citește `index_status.json` |
| `internal/service/engine/engine.go` | Adaugă `ErrIndexingBusy` pentru al doilea workspace |
| Toate fișierele de indexare | `log.Printf` → `logger.Instance.*` cu format standardizat `[IDX]` |

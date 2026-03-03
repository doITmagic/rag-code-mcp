# Analiză: jcodemunch-mcp vs rag-code-mcp și Idei de Implementare

## Ce face `jcodemunch-mcp`?
Acest proiect acționează ca un server MCP bazat pe parsarea AST (via `tree-sitter`) ce construiește un index local al tuturor simbolurilor (funcții, metode, clase, tipuri, constante) din codebase. Scopul său principal este de a naviga și a aduce agenților AI strict fragmentul sau simbolul de care au nevoie, înlăturând necesitatea ca agentul să citească (raw read) zeci de mii de linii de cod repetitiv sau boilerplate.

Față de aplicația noastră (`rag-code-mcp`), ei pun un accent uriaș pe simplificarea navigației și uneltele atomice (ex. `get_symbol`, `search_symbols`) folosind ID-uri unice ale simbolurilor, și se mândresc cu o documentare foarte clară a **„economiei de tokeni”** (Token Savings) și **„costului evitat”** (Cost Avoided).

---

## 💡 Idei bune și Funcționalități Punctuale pe care le-am putea integra în `rag-code-mcp`

### 1. Funcția de Contorizare a "Token Savings" și "Cost Avoided" (Live Tracking)

**Cum face `jcodemunch-mcp`?**
- La fiecare preluare de simbol, calculează instantaneu octeții salvați (raw vs context returnat) și scutește `file_size` ÷ 4 `tokens_saved`.
- **Persistență Gobală**: Salvează `total_tokens_saved` cumulat între sesiuni automat într-un tracker permanent (`~/.code-index/_savings.json`).
- De asemenea, randează în USD câți bani ai economisit exact folosind tarife standard (ex. Claude Opus cost vs GPT-5 latest), trimițând structura asta agentului:
  ```json
  "_meta": {
      "tokens_saved": 2450,
      "total_tokens_saved": 184320,
      "cost_avoided": { "claude_opus": 0.24 },
      "total_cost_avoided": { "claude_opus": 6.40 }
  }
  ```

**Cum facem noi în `rag-code-mcp` momentan?**
- Avem deja un modul la `pkg/telemetry/savings.go` (`CalculateSavings`) pe care îl apelăm din `internal/service/tools/` la generarea contextuală a rezultatelor (`actualBytes` vs `os.Stat(path).Size()`).
- Sistemul nostru doar evaluează `tokens_saved`, `bytes_avoided` și o eficiență procentuală la nivel de cerere individuală pentru a fi servite înapoi către AI sub `context.telemetry`.
- ❌ **Ce lipsește**: Măsurarea este izolată (stateless). Nu avem implementat un Storage de Telemetrie Globală care să păstreze `total_tokens_saved` peste sesiuni și nu calculăm „bani salvați” pentru diverse modele cum fac ei cu `cost_avoided`.

**✅ Aplicabilitate practică (Ideea de a se integra la noi):**
Ar trebui să adăugăm scrierea atomică / asincronă a savings-ului calculat per-request într-un track file mic global (`home/.ragcode/savings.json`) sau log. De asemenea, trebuie să punem în `modelContextProtocol` răspunsul de Telemetry cumulat + Banii Economisiți pe care vizual AI-ul (sau user-ul) să îl vadă. E un USP imens („RagCode ți-a economisit $40 luna asta”).

### 2. Extragerea Extrem de Rapidă în Timpul Execuției: O(1) Fetch prin Offset Byte
- **Implementare în jcode:** Pe lângă liniile de start/end, indexul lor JSON stochează offset-ul exact în octeți (Byte Offset) al fiecărui simbol sau bucăți de funcții parsate de AST. Pentru recuperarea conținutului lor, backend-ul face simplu `seek()` în fișier la offset-ul stocat și citește de acolo. Acest lucru evită încărcarea în text integral a conținutului, evitând penalizări RAM / re-parsare.
- **Aplicabilitate practică pentru noi:** Dacă stocăm byte offsets în baza noastră de date (Graph Context/Vectors) pentru nodurile de cod, orice apel `read_file_context` viitor ar funcționa instantaneu și extrem predictibil.

### 3. Mecanism de ID Stabil pentru Simbolurilor (`Stable Symbol IDs`)
- **Implementare în jcode:** Oferă fiecărui nod parsabil un ID fix, ușor de citit/serializat: `{file_path}::{qualified_name}#{kind}` (ex: `src/main.py::UserService.login#method`). Funcțiile „overload” sunt diferențiate prin sufixe incrementale (ex. `~1`, `~2`).
- **Aplicabilitate practică pentru noi:** `rag-code-mcp` ar putea expune (într-una din acțiunile de Outline / Symbol Explore) o referință similară. Astfel, agentul ar putea fi rugat de sistem doar: *Adu-mi te rog definiția și callerele pentru `pkg/parser/php/laravel/adapter.go::Parser.Extract#method`*.

### 4. Search Algorithm Bazat pe Scor Ponderat (Scoring Punctual fără Embeddings complexe)
- **Implementare în jcode:** Nu utilizează vector database embeddings pentru `search_symbols`. Au un algoritm local extrem de bine definit care dă "scoruri" căutării pur text:
  - Exact match nume: +20 pct
  - Substring nume: +10 pct
  - Overlap de cuvânt: +5 pct/cuv
  - Părți din Signatură sau Docstring: +3 pct, etc.
- **Aplicabilitate practică pentru noi:** Acesta poate fi un „fallback de mare viteză” sau un "Mod Hibrid/Text" optimizat pentru `mcp_ragcode_rag_search_code` (care la noi folosește opțiunea `mode: "exact"`).

### 5. Sumarizator Activ de Simboluri (În Indexare cu AI mic/rapid)
- **Implementare în jcode:** Dacă o funcție sau clasă nu are documentație, trimite batch-uri către Claude Haiku / Gemini Flash (care cer costuri neglijabile) pe timpul indexării doar pentru a genera un scurt „One Line Summary”.
- **Aplicabilitate practică pentru noi:** Dacă RAG Code este legat de un Provider ieftin, am putea îmbunătăți enorm indexul vectorial sau căutarea semantică dând un context real simbolurilor care abia au 20 de litere de cod și denumiri criptice. (Ex: `Extract(...)` -> "Summarized as: Parses Laravel routes structure and creates AST tree...").

## 🤖 Propuneri Validate de Agenți (pe baza Rulărilor și RAG Evaluation)

În urma utilizării și evaluării detaliate a agenților (Antigravity/Claude), am identificat următoarele direcții cheie pentru a reduce "fricțiunea" și a maximiza performanța:

### 6. ✅ IMPLEMENTAT — `rag_search`: Dual Search + Adaptive Response

> **Status:** Implementat și testat cu succes în `internal/service/tools/smart_search.go`

**Problema inițială (2 propuneri separate, acum unificate):**
1. Agenții trebuiau să aleagă manual `mode: "exact"` vs `"discovery"` — decizie care consuma tokeni de raționament.
2. Chiar și cu 5 rezultate, JSON-ul returnat conținea mult cod sursă, poluând contextul agentului.

**Soluția implementată — `rag_search` (tool nou, coexistă cu `rag_search_code`):**

Abordarea inițială de "euristici lingvistice" (CamelCase, snake_case, etc.) a fost respinsă: **nu știi ce cod are utilizatorul**, deci nu poți ghici dacă un cuvânt e simbol sau concept. Soluția corectă:

1. **Dual Search Paralel** — rulează `SearchCode` (semantic) și `HybridSearchCode` (exact) simultan în goroutine-uri. Costul: max(exact, semantic) ≈ ~100ms, zero penalizare.
2. **Merge & Deduplicate** — combină rezultatele, elimină duplicatele (pe ID vectorial), marchează sursa fiecărui rezultat (`_source: "semantic" | "hybrid" | "both"`). Rezultatele găsite de **ambele** strategii sunt garantat cele mai relevante.
3. **Răspuns Adaptiv (fără parametri)** — decide automat formatul pe baza **rezultatelor**, nu pe baza query-ului:
   - **Compact** (doar metadate: fișier, linie, semnătură, scor) — când sunt >4 rezultate SAU scorul top < 0.85
   - **Full** (cod sursă complet) — când sunt puține rezultate cu scor mare

**Parametri tool:** doar `query` + opțional `file_path`. Zero decizii pentru agent.

#### Benchmark Real (testat pe proiectul rag-code-mcp):

| Metrică | `rag_search_code` (vechi) | `rag_search` (nou) |
|---------|--------------------------|---------------------|
| Parametri de decizie | `query` + `mode` + `file_path` | doar `query` (+ opțional `file_path`) |
| Strategia de căutare | 1 singură (aleasă manual) | ambele, în paralel |
| Output (10 rezultate) | ~17.500 tokeni (cod complet) | ~500 tokeni (compact: semnături) |
| Eficiență tokeni | ~89% | **100%** |
| Info suplimentar | - | `_source` arată ce strategie a găsit fiecare rezultat |

#### Workflow Validat (2 pași vs 1 pas mare):

```
Pas 1: rag_search("workspace detection") → 10 rezultate COMPACT (~500 tokeni)
       AI identifică: DetectContext pe L164 din engine.go

Pas 2: rag_read_file_context(engine.go, L164-219) → cod complet (~2.5KB)
       AI primește exact funcția + relații AST

Total: ~3KB context consumat
Alternativa veche: ~17KB dintr-un singur apel rag_search_code
Economie: ~6x mai puțini tokeni
```

### 7. Indexing Status & Health Metrics in Results — ⚠️ PARȚIAL IMPLEMENTAT

**Problemă:** Agentul primește un rezultat greșit (index vechi, funcție ștearsă) și nu știe, luându-l ca adevăr absolut.

**Ce avem deja implementat:**

| Componentă | Fișier | Status |
|------------|--------|--------|
| `IndexProgress` struct (state, elapsed, per-language) | `engine/index_progress.go` | ✅ |
| `IndexingProgressSummary` (view compact pt răspunsuri) | `tools/response.go` | ✅ |
| `BuildIndexingProgress()` (construiește summary) | `tools/response.go` | ✅ |
| `GetIndexProgress()` (citește jobul curent) | `engine/engine.go` | ✅ |
| `MismatchRisk` warning (branch mismatch) | `engine/engine.go` → `WorkspaceContext` | ✅ |
| `ReindexRequired` flag (git HEAD schimbat) | `engine/engine.go` → `WorkspaceContext` | ✅ |
| `CompareAndUpdate()` (detectează schimbări git) | `branchstate/state.go` | ✅ |
| `ErrIndexingStarted` / `ErrIndexingInProgress` | `engine/engine.go` | ✅ |

**Ce lipsește (de completat):**
1. **`_index_age`** — un field simplu în răspunsul de căutare care spune "indexul a fost actualizat acum X minute". Momentan avem `Elapsed` doar când indexarea e activă, nu și un timestamp al ultimei indexări completate.
2. **Stale chunk detection** — dacă în timpul unui search, un fișier adus din vectori returnează `os.IsNotExist`, MCP-ul ar trebui să adauge un warning automat: `"Warning: Some indexed files no longer exist. Consider re-indexing."`. Asta ar face feedback-ul proactiv, nu reactiv.
3. **Expunerea consistentă** — `rag_search` (noul tool) deja include `indexing_progress` în context, dar nu toate tool-urile o fac uniform.

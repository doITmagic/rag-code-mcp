# Discuții și Idei Arhitecturale

## 2026-02-18 — LLM local: unde adaugă valoare reală?

### Context
Proiectul folosește 2 modele Ollama configurate în `server.json`:
- `OLLAMA_EMBED` (ex: `mxbai-embed-large`) — folosit activ pentru embedding la indexare și căutare
- `OLLAMA_MODEL` (ex: `phi3:medium`) — prezent în infrastructură dar **nefolosit** în niciun tool de producție

### Idei pentru utilizarea LLM-ului de chat

#### 1. Query Expansion *(prioritate înaltă)*
AI-ul trimite `"auth middleware"`. LLM-ul local expandează la variante sinonime:
`["authentication middleware", "JWT validation", "request interceptor", "bearer token check"]`
→ se fac N căutări în paralel, rezultatele se merge și de-duplică → acoperire mult mai bună.

**Valoare:** AI-ul extern știe ce vrea, dar nu știe cum e numit în codul specific al proiectului.
**Cost:** 1 apel LLM per query, +100-300ms latență.
**Implementare:** pas opțional în `Engine.SearchCode` înainte de căutarea în Qdrant.

#### 2. Re-ranking semantic *(prioritate medie)*
Qdrant returnează top 20 după vector similarity. LLM-ul local citește fiecare rezultat + query și dă un scor de relevanță reală.
→ se returnează top 5 re-ranked în loc de top 5 brut.

**Valoare:** embedding-urile sunt bune dar nu perfecte — LLM înțelege contextul mai bine.
**Cost:** N apeluri LLM per query (unul per rezultat), latență semnificativă.
**Implementare:** pas opțional în `Engine.SearchCode` după căutarea în Qdrant.

#### 3. Query Intent Detection *(prioritate medie)*
LLM-ul clasifică query-ul înainte de căutare și alege tool-ul potrivit automat:
- `"find function X"` → `rag_get_function_details`
- `"find all usages of X"` → `rag_find_implementations`
- `"how does X work"` → `rag_search_code`

**Valoare:** reduce numărul de tool-uri pe care AI-ul extern trebuie să le cunoască.

#### 4. Chunk Summarization la indexare *(prioritate scăzută)*
La `rag_index_workspace`, LLM-ul generează un summary de 1-2 propoziții per funcție indexată.
Summary-ul se stochează în payload Qdrant și se folosește la embedding în loc de codul brut.

**Valoare:** căutarea semantică devine mai precisă.
**Cost:** N apeluri LLM la indexare — indexarea devine mult mai lentă.

---

## 2026-02-18 — Inspirație: SQLite-only RAG (zero dependențe externe)

### Referință
Un tool concurent implementează RAG complet fără dependențe externe:

| Layer | Implementare |
|-------|-------------|
| Vector DB | Embeddings ca BLOB în SQLite, cosine similarity search |
| Keyword Search | FTS5 virtual tables cu BM25 scoring |
| Hybrid Merge | Custom weighted merge function |
| Embeddings | Trait cu OpenAI, custom URL, sau noop |
| Chunking | Line-based markdown chunker |
| Caching | SQLite `embedding_cache` cu LRU eviction |
| Safe Reindex | Rebuild FTS5 + re-embed atomic |

### Comparație cu abordarea noastră

| | SQLite-only | Noi (Qdrant + Ollama) |
|---|---|---|
| **Setup** | Zero — un singur fișier | Qdrant + Ollama obligatorii |
| **Vector search** | Cosine pe BLOB | HNSW index nativ în Qdrant |
| **Keyword search** | FTS5 + BM25 real | Lipsă — hybrid_search e tot semantic |
| **Hybrid merge** | Custom weighted cu BM25 real | 60/40 dar fără BM25 real |
| **Embedding cache** | SQLite LRU | Lipsă — re-embed la fiecare query |
| **Scalabilitate** | Limitată (SQLite) | Bună (Qdrant HNSW) |

### Ce putem lua de acolo

#### A. BM25 / keyword search real *(prioritate înaltă)*
`hybrid_search.go` face acum 60% semantic + 40% "lexical" dar lexical-ul e tot semantic.
Un BM25 real pe codul indexat ar fi mult mai precis pentru căutări exacte (nume funcție, variabilă).
**Implementare:** FTS5 în SQLite local sau Qdrant sparse vectors (suportat din v1.7).

#### B. Embedding cache *(prioritate înaltă, ușor de implementat)*
Acum la fiecare query: `Embed(ctx, queryText)` → apel Ollama, chiar dacă același query a mai venit.
Un cache simplu `map[string][]float32` cu LRU ar elimina apelurile duplicate.
**Implementare:** wrapper peste `llm.Provider` în `search.Service`.

#### C. SQLite ca backend alternativ la Qdrant *(prioritate scăzută)*
Ar elimina dependența de Qdrant și ar simplifica enorm setup-ul pentru proiecte mici.
**Implementare:** nouă implementare a interfeței `storage.VectorStore` cu SQLite.
**Când:** după ce `storage.VectorStore` interface este stabilizată.

### Prioritizare implementare
1. **Embedding cache** — ușor, impact imediat, zero arhitectură nouă
2. **BM25 real** — ar face `hybrid_search` cu adevărat hybrid
3. **Query Expansion cu LLM** — valoare mare, latență acceptabilă
4. **SQLite backend** — opțional, pentru utilizatori fără Qdrant

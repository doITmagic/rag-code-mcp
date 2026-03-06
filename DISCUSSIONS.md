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

---

## 2026-02-23 — RagCode Lite: zero dependențe externe

### Problema curentă
Utilizatorul nou are nevoie de: **Docker + Qdrant + Ollama + model pull (~670MB)** înainte de primul query.
Bariera de intrare e prea mare pentru adopție. Scopul versiunii Lite: **download binary → funcționează**.

### Opțiuni evaluate

#### 1. Vector DB embedded — `coder/hnsw` *(recomandat)*

| | Qdrant (actual) | coder/hnsw |
|---|---|---|
| Dependință externă | Docker obligatoriu | ✗ — embedded în binary |
| CGO | ✗ | ✗ — pure Go |
| Static binary | ✓ | ✓ — `build-binaries.sh` rămâne simplu |
| ANN index | HNSW nativ | HNSW pure Go |
| Payload filtering | ✓ puternic | manual post-search |
| Persistență | server | fișier local |
| Efort implementare `VectorStore` | — | mic (~200 linii) |

**Implementare:** `pkg/storage/hnsw.go` cu flush JSON pe disc la `~/.local/share/ragcode/vectors/`.
Config: `storage.vector_db.backend: hnsw`

#### 2. Embeddings fără Ollama — opțiuni

**A. Jina AI API** *(cloud, free tier)*
- 1M tokens/lună gratuit, fără card
- Rate limit free tier: 100 RPM & 100,000 TPM
- Zero instalare: API key → funcționează instant pe orice OS
- Model recomandat: `jina-embeddings-v2-base-code` (antrenat specific pe cod, 768 dims)
- Model nou SOTA: `jina-embeddings-v5-text` (677M, 32K context, Matryoshka dims)
- **Dezavantaj:** dependință internet, service extern poate cădea
- Config: `llm.provider: jina`, `llm.jina_api_key: jina_xxx`

**B. BM25 / bleve** *(offline, zero dependențe)*
- `blevesearch/bleve` — full-text search embedded, zero CGO, bbolt backend
- Indexează `Symbol.Name` (3x), `Symbol.Signature` (2x), `Symbol.Content` (1x)
- Calitate vs neural: ~60-70% pentru queries care conțin termeni din cod
- Cade pentru semantic queries (`"authenticate user"` → `LoginHandler`)
- **Avantaj:** funcționează 100% offline, zero setup
- Config: `llm.provider: none`, `storage.vector_db.backend: bleve`

**C. ONNX Runtime local** *(offline, CGO)*
- `yalue/onnxruntime_go` + `all-MiniLM-L6-v2.onnx` (~90MB, 384 dims)
- Model bundled sau descărcat o dată la prima rulare
- Calitate apropiată de Ollama, fără server
- **Dezavantaj:** CGO complică cross-compile

#### 3. Hybrid search fără LLM — BM25 + HNSW cu RRF

Când Ollama/Jina este disponibil:
```
BM25 (bleve) + HNSW (vector) → RRF merge → top-K
```
Când nu există LLM:
```
BM25 (bleve) only → top-K
```
Aceasta e arhitectura Cursor.sh / GitHub Copilot. RRF (Reciprocal Rank Fusion) este ~5 linii de cod.

#### 4. Tree-sitter pentru parsere *(îmbunătățire precizie AST)*
- Go are deja `go/ast` nativ → **nu aduce beneficii pentru Go**
- PHP, Python, JS au parsere mai naive în prezent → tree-sitter aduce precizie AST
- `go-tree-sitter` (smacker) = CGO → complică build, nu ideal pentru Lite
- **Verdict:** low priority pentru Lite, reconsiderat pentru v2

---

## 2026-02-23 — Tree-sitter: merită schimbat tot parserul?

### Situația actuală a parserelor

| Limbaj | Implementare | Calitate | Linii |
|---|---|---|---|
| **Go** | `go/ast` nativ stdlib | ★★★★★ precis, typed | 944 |
| **Python** | regex + indent-counting (`findBlockEnd`) | ★★★ fragil pe edge cases | 1190 |
| **PHP/Laravel** | custom AST walker + Laravel sub-package complet | ★★★★ bun | ~500 |
| **JavaScript/TS** | **ZERO implementare** — doar README | — | 0 |
| **WordPress** | **ZERO implementare** | — | 0 |

### De ce tree-sitter contează pentru JS în special

JS/TS este **imposibil de parsat corect cu regex**:
- JSX (`<Component prop={expr} />`) — ambiguitate totală cu operatori `<` `>`
- Template literals cu expresii `` `hello ${user.name}` ``
- Destructuring în parametri `function({id, name}: Props)`
- Decorators TypeScript `@Injectable()`, `@Component()`
- Arrow functions cuibare `const f = (x) => (y) => x + y`

README-ul din `pkg/parser/javascript/` menționează explicit tree-sitter ca plan de implementare. **Varianta regex ar necesita >2000 linii și ar fi în continuare incorectă.**

### tree-sitter în Go — opțiuni

**A. `smacker/go-tree-sitter`** (cel mai matur)
```go
// CGO obligatoriu
// Grammar-uri pre-compilate pentru JS, TS, TSX, PHP, Python, Ruby, etc.
// Folosit de Neovim, Helix, GitHub Semantic
import sitter "github.com/smacker/go-tree-sitter"
import "github.com/smacker/go-tree-sitter/javascript"
import "github.com/smacker/go-tree-sitter/typescript/tsx"
```

**B. `tree-sitter/go-tree-sitter`** (official, mai nou dar mai puțin matur în Go)

**Dezavantaj critic:** CGO → complică `build-binaries.sh` cross-compile.

### Soluție: build tags pentru separare Lite / Full

```go
// pkg/parser/javascript/analyzer_treesitter.go
//go:build !lite

// pkg/parser/javascript/analyzer_regex.go  
//go:build lite
```

Astfel:
- `go build -tags lite` → binary static, zero CGO, fallback regex
- `go build` (standard) → tree-sitter complet, CGO

### WordPress — arhitectură recomandată

WordPress urmează pattern-ul Laravel: **PHP parser ca bază + layer WP-specific**.

```
pkg/parser/php/
  wordpress/
    analyzer.go      ← wrapper peste php.PackageInfo
    hooks.go         ← add_action / add_filter detection  
    post_types.go    ← register_post_type / register_taxonomy
    shortcodes.go    ← add_shortcode
    blocks.go        ← register_block_type (Gutenberg)
    types.go
```

Patterns WordPress-specific de detectat:
```php
add_action('init', 'my_callback')          → Hook{Type: action, Name: "init"}
add_filter('the_content', [$this, 'fn'])   → Hook{Type: filter, Name: "the_content"}
register_post_type('book', $args)          → PostType{Name: "book"}
add_shortcode('gallery', 'fn')             → Shortcode{Tag: "gallery"}
// Plugin Name: My Plugin                  → PluginHeader metadata
class MyWidget extends WP_Widget           → Widget symbol
```

### Decizie per-parser

| Parser | Acțiune recomandată | Motivare |
|---|---|---|
| **Go** | Păstrăm `go/ast` | Perfect, nicio îmbunătățire posibilă |
| **Python** | tree-sitter (build full) / regex (build lite) | 1190 linii fragile, merită rescris |
| **PHP** | Păstrăm custom + adăugăm WordPress sub-package | Deja funcțional, Laravel pattern dovedit |
| **JavaScript/TS** | tree-sitter obligatoriu pentru corectitudine | JSX/decorators = imposibil cu regex |
| **WordPress** | PHP base + hooks/posttype layer (fără tree-sitter) | Patterns simple, regex suficient |

### Prioritizare implementare parsere

1. **JavaScript/TS cu tree-sitter** — zero implementare acum, prioritate înaltă pentru adopție
2. **WordPress sub-package** — pattern Laravel, ~4 fișiere noi, fără CGO
3. **Python refactor cu tree-sitter** — îmbunătățire calitate, nu urgent (funcționează)
4. **Build tags lite/full** — după ce tree-sitter e integrat

### Strategia recomandată pentru Lite

```
Lite = coder/hnsw + bleve (BM25) + Jina API opțional
```

Moduri de funcționare (auto-detect):
1. **Full** — Qdrant + Ollama (actual, prod)
2. **Cloud** — hnsw + Jina API (zero instalare locală, necesită internet)
3. **Offline** — hnsw + bleve BM25 (zero dependențe, funcționează air-gapped)

Config minimal pentru Lite:
```yaml
storage:
  vector_db:
    backend: hnsw
    hnsw_path: ~/.local/share/ragcode/vectors/
llm:
  provider: jina          # sau "none" pentru offline BM25
  jina_api_key: jina_xxx  # gratuit pe jina.ai
  jina_embed_model: jina-embeddings-v2-base-code
```

### Prioritizare implementare Lite

1. **`pkg/storage/hnsw.go`** — elimină Docker/Qdrant, cel mai mare impact DX
2. **`pkg/storage/bleve.go`** — elimină Ollama, fallback BM25 offline
3. **`pkg/llm/jina.go`** — provider cloud gratuit, ~80 linii, zero CGO
4. **RRF hybrid merge** — combină bleve + hnsw când ambele disponibile
5. **Tree-sitter parsere PHP/Python/JS** — v2, după stabilizarea Lite

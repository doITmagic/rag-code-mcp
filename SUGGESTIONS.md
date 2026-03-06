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

### 7. ✅ IMPLEMENTAT — Indexing Status & Health Metrics in Results

> **Status:** Implementat complet în `internal/service/tools/`

**Problema:** Agentul primea un rezultat greșit (index vechi, funcție ștearsă) și nu știa, luându-l ca adevăr absolut.

**Ce era deja implementat:** `IndexProgress`, `BuildIndexingProgress`, `MismatchRisk`, `ReindexRequired`, erori de indexare

**Ce am adăugat:**

| Componentă | Fișier | Status |
|------------|--------|--------|
| `index_age` field în `IndexingProgressSummary` | `tools/response.go` | ✅ |
| `formatAge()` helper (`"just now"`, `"3 minutes ago"`, etc.) | `tools/response.go` | ✅ |
| **Stale chunk detection** în `rag_search_code` | `tools/search_local_index.go` | ✅ |
| **Stale chunk detection** în `rag_search` (smart) | `tools/smart_search.go` | ✅ |
| `IndexingProgress` expus uniform în `rag_read_file_context` | `tools/read_file_context.go` | ✅ |
| `IndexingProgress` expus uniform în `rag_list_skills` | `tools/skills.go` | ✅ |
| `IndexingProgress` expus uniform în `rag_install_skill` | `tools/skills.go` | ✅ |
| `IndexingProgress` expus uniform în `rag_evaluate` | `tools/evaluate_ragcode.go` | ✅ |
| Teste dedicate pentru toate 3 funcționalități | `tools/tests/health_metrics_test.go` | ✅ |

**Comportament stale detection:**
```json
{
  "warning": "⚠️ 2 indexed file(s) no longer exist on disk (stale index). Consider re-indexing. Missing: /src/old_file.go, /src/deleted.go"
}
```

**Comportament index_age (când indexul e completed):**
```json
{
  "context": {
    "indexing_progress": {
      "state": "completed",
      "elapsed": "1m23s",
      "index_age": "3 minutes ago"
    }
  }
}
```

### 8. 🔄 Migrare de la `langchaingo` la clientul nativ Ollama (`github.com/ollama/ollama/api`)

> **Status:** Propus — refactoring dedicat

**Problema:** Comunicarea actuală cu Ollama se face prin `github.com/tmc/langchaingo` (v0.1.14), un wrapper generic LLM care:
- Nu expune funcții native Ollama (heartbeat, list running, model management)
- Nu oferă control granular asupra timeout-urilor HTTP la nivel de transport
- Adaugă un nivel de indirectare care face debugging-ul mai dificil
- A contribuit la bug-ul de deadlock — `CreateEmbedding` nu propaga corect context cancellation

**Soluția propusă:** Pachetul oficial **`github.com/ollama/ollama/api`** oferă exact ce ne trebuie:

| Funcție | Ce oferă | Utilizare în RagCode |
|---------|---------|---------------------|
| `Client.Heartbeat(ctx)` | Liveness check nativ | Health check în watchdog-ul de indexare |
| `Client.Embed(ctx, req)` | Embeddings direct, fără intermediar | Înlocuiește langchaingo `CreateEmbedding` |
| `Client.List(ctx)` | Lista completă a modelelor instalate | Validare la startup + tool `rag_evaluate` |
| `Client.ListRunning(ctx)` | Procesele/modelele active în memorie | Diagnosticare when Ollama e slow |
| `ClientFromEnvironment()` | Auto-config din `OLLAMA_HOST` env | Configurare zero-effort |

**Beneficii concrete:**
1. **Reziliență nativă** — `Heartbeat()` e un ping oficial, nu un hack HTTP `/api/tags`
2. **Context propagation corect** — clientul oficial respectă `context.WithTimeout` la nivel HTTP
3. **Eliminare dependență langchaingo** — o dependență masivă (~50+ sub-dependencies) eliminată
4. **Control transport HTTP** — putem configura `http.Client` cu timeouts custom, keep-alive, etc.
5. **Embedding bulk** — `EmbedRequest` suportă `Input []string` (batch nativ), nu doar text singular

**Scope of work:**
- Înlocuire `pkg/llm/ollama.go` → implementare cu `ollama/api.Client`
- Actualizare `internal/healthcheck/` → `PingOllama()` folosind `Client.Heartbeat()`
- Eliminare `langchaingo` din `go.mod`
- Testare completă cu modelele existente

**Riscuri:** Migrare mare, necesită testare exhaustivă. Trebuie făcut ca un refactoring separat, nu alături de alte features.

### 9. 🔄 Smart Search Consolidation — Un singur tool de căutare, fără „decision fatigue"

> **Status:** Propus — vezi TASKS.md Task 7

**Problema:** MCP-ul expune mai multe tool-uri de căutare cu capabilități suprapuse (`rag_search` + `rag_search_code`). Agenții LLM pierd tokeni de raționament decidând pe care să-l folosească. În plus, `rag_search_code` expune parametri manuali (`mode: "discovery" | "exact"`, `include_docs`) care forțează agentul să ia decizii pe care `rag_search` le ia deja automat.

**Soluția propusă:**

1. **Consolidare în `rag_search`** — devine singurul tool de căutare (text/semantic). Celelalte tool-uri (`rag_find_usages`, `rag_call_hierarchy`, `rag_list_package_exports`) rămân — sunt ortogonale (graph / structural, nu text).

2. **Adăugare 2 parametri opționali:**
   - `include_full_content: bool` (default `false`) — când `true`, forțează modul Full (cod sursă integral), ignorând logica adaptivă compact/highConfidence. Util când agentul știe exact că vrea codul, nu doar metadata.
   - `include_docs: bool` (default `false`) — când `true`, caută și în chunk-urile de documentație markdown (necesită Suggestion #10).

3. **Ștergere `rag_search_code`** (`search_local_index.go`) — tot ce făcea (discovery, exact, include_docs, Graph Context Expansion) e acoperit de `rag_search`.

**Impactul în cod:**
- `SmartSearchInput`: +2 câmpuri bool
- `Execute()`: +3 linii (override `useCompact` când `include_full_content`)
- `Execute()`: +1 goroutină (docs search când `include_docs`)
- `search_local_index.go`: marcat deprecated → șters

**Beneficii:**
- Agentul vede **6 tool-uri** în loc de 7, fiecare cu scop distinct
- Zero „parametric overload" — max 5 parametri, toți intuitivi
- Input schema rămâne simplă: `query` (obligatoriu) + 4 opționale

### 10. 🔄 Indexare Documentație Markdown cu `langchaingo/textsplitter`

> **Status:** Propus — vezi TASKS.md Task 8

**Problema:** RAGCode indexează doar cod sursă (Go, Python, PHP). Fișierele `.md` (README, guides, API docs, CHANGELOG) sunt complet ignorate. Când agentul caută „cum se face deployment" sau „ce face IndexWorkspace", nu găsește documentația care descrie exact asta.

**Versiunea veche** (`inspirations/rag-code-mcp`) avea un tool `rag_search_docs` separat și un chunking naiv (split pe heading + maxChars=2000, fără overlap). Funcționa, dar:
- Tăia tabele și code blocks la jumătate
- Pierdea contextul heading-urilor părinte (un chunk cu "### OAuth" nu știa că era sub "## Auth > # API")
- Zero overlap între chunk-uri = pierdere de context la granițe

**Soluția propusă — `langchaingo/textsplitter`:**

Pachetul `github.com/tmc/langchaingo` oferă un `MarkdownHeaderTextSplitter` matur care rezolvă toate problemele:

| Feature | Chunking naiv (vechi) | `langchaingo/textsplitter` |
|---------|----------------------|---------------------------|
| Heading hierarchy | ❌ Pierde contextul părinte | ✅ Prepune heading-urile (ex: "# API > ## Auth > ### OAuth") |
| Overlap între chunk-uri | ❌ Zero | ✅ Configurabil (ex: 200 chars) |
| Tabele | ❌ Sparte pe linii | ✅ Păstrate ca un singur chunk |
| Code blocks | ❌ Tăiate arbitrar | ✅ `WithCodeBlocks` — întregi |
| Liste | ❌ Sparte la jumătate | ✅ Păstrate structura logică |
| Token counting | ❌ Numără caractere | ✅ Poate număra tokeni reali |

**Arhitectura de integrare:**
- **Chunking:** `MarkdownHeaderTextSplitter` cu `chunkSize: 2000`, `chunkOverlap: 200`, `WithHeadingHierarchy(true)`, `WithCodeBlocks(true)`
- **Embedding:** Același model Ollama (`qwen3-embedding`) ca pentru cod
- **Storage:** Aceeași colecție Qdrant, diferențiat prin `chunk_type: "markdown"` în payload
- **Potrivire cod ↔ doc:** 100% prin similaritate semantică (embedding cosine similarity) — zero regex, zero extragere explicită de simboluri, complet language-agnostic
- **Căutare:** Când `include_docs: true` pe `rag_search`, se adaugă o a 3-a goroutină de căutare filtrată pe `chunk_type == "markdown"`, merge-uită cu rezultatele de cod

**Nota importantă privind `langchaingo`:** Suggestion #8 propune eliminarea `langchaingo` în favoarea clientului nativ Ollama. Cele două nu sunt în conflict — putem folosi `langchaingo/textsplitter` (sub-pachetul de text splitting) fără a folosi `langchaingo` ca LLM client. Alternativ, putem extrage doar logica de chunking într-un pachet propriu (`pkg/indexer/markdown.go`) inspirat din `textsplitter`, eliminând complet dependința.

**Extensii viitoare (P2):**
- `.txt` → split pe paragrafe cu `RecursiveCharacterSplitter`
- `.json` / `.yaml` → flatten keys ca text documentație
- `.rst` / `.adoc` → convertor la markdown + chunking standard

### 11. 🔄 WordPress / WooCommerce / Oxygen Builder Parser — Sub-package după modelul Laravel

> **Status:** Propus — cercetare completă finalizată
> **Cercetare:** vezi artifact `wordpress_parser_research.md`

**Problema:** RagCode parsează excelent PHP generic și Laravel, dar **nu înțelege WordPress**. Un plugin WordPress are pattern-uri specifice (hooks, custom post types, shortcodes, Gutenberg blocks) care nu sunt extrase de parserul PHP de bază. La fel, Oxygen Builder și WooCommerce au convenții proprii pe care un AI trebuie să le cunoască pentru a naviga eficient.

**Fundament existent (nu trebuie construit de la zero):**
- `VKCOM/php-parser` — deja integrat, parsează PHP 5/7/8 complet în AST
- `pkg/parser/php/laravel/` — sub-package funcțional cu 4 sub-analyzeri, dovedește pattern-ul
- `DISCUSSIONS.md` (L218-258) — plan arhitectural deja documentat

**Surse de inspirație cercetate:**

| Proiect | Limbaj | Ce preluăm |
|---------|--------|------------|
| [`malikad778/wp-hook-check`](https://github.com/malikad778/wp-hook-check) | PHP | Logica completă hooks detection + orphan analysis (AST traversal pe `add_action`, `add_filter`, `do_action`, `apply_filters`) |
| [`VKCOM/noverify`](https://github.com/VKCOM/noverify) | **Go** | Pattern traversare AST Go, namespace resolver, caching metadata per fișier |
| [`WordPress/phpdoc-parser`](https://github.com/WordPress/phpdoc-parser) | PHP | Catalog complet de patterns WP API (nu se portează, doar se studiază CE detectează) |
| Pattern Laravel din proiect | Go | Arhitectura sub-package, coordinator Analyzer, types separate |

> **Notă despre `WordPress/phpdoc-parser`:** Nu trebuie portat în Go! Folosește `nikic/php-parser` (echivalentul PHP al `VKCOM/php-parser`). Rolul lui e doar de **referință** — studiezi CE detectează (hooks, PHPDoc patterns, hook name generation din AST nodes) și implementezi aceeași logică cu `VKCOM/php-parser` care e deja în proiect.

**Arhitectura propusă:**

```
pkg/parser/php/
  wordpress/
    analyzer.go          ← wrapper peste php.PackageInfo, coordonează sub-analyzeri (ca laravel/analyzer.go)
    hooks.go             ← add_action / add_filter / do_action / apply_filters / remove_action / has_filter
    post_types.go        ← register_post_type / register_taxonomy / register_meta
    shortcodes.go        ← add_shortcode
    blocks.go            ← register_block_type / register_block_pattern (Gutenberg)
    widgets.go           ← clasă extends WP_Widget
    admin.go             ← add_menu_page / add_submenu_page / register_setting
    plugin_header.go     ← Plugin Name / Theme Name / Version / Author din comment header
    types.go             ← WPHook, PostType, Taxonomy, Shortcode, Block, Widget, PluginHeader, etc.
    oxygen/
      analyzer.go        ← OxyEl class detection, ct_builder_json parsing
      types.go           ← OxygenElement, OxygenTemplate, CodeBlock
    woocommerce/
      analyzer.go        ← WC-specific hooks (prefix "woocommerce_"), product queries
      types.go           ← WC_Hook, WC_API types
```

**Pattern-uri WordPress de detectat (prioritizate):**

**A. Hooks — prioritate înaltă (cel mai important layer WP)**
```php
add_action('init', 'my_callback');                    → Hook{Type: action, Name: "init", Callback: "my_callback"}
add_action('init', [$this, 'method'], 10, 2);        → Hook{Type: action, Priority: 10, ArgCount: 2}
add_filter('the_content', 'my_filter');               → Hook{Type: filter, Name: "the_content"}
do_action('my_custom_hook', $arg1, $arg2);            → Hook{Type: action_trigger, Name: "my_custom_hook"}
apply_filters('my_filter', $value, $extra);           → Hook{Type: filter_trigger, Name: "my_filter"}
remove_action('wp_head', 'wp_generator');             → Hook{Type: action_removal}
has_filter('the_content');                            → Hook{Type: filter_check}
```

**B. Custom Post Types & Taxonomii — prioritate înaltă**
```php
register_post_type('book', $args);                    → PostType{Name: "book"}
register_taxonomy('genre', 'book', $args);            → Taxonomy{Name: "genre", PostType: "book"}
register_meta('post', 'my_meta', $args);              → Meta{Type: "post", Key: "my_meta"}
```

**C. Shortcodes & Gutenberg Blocks — prioritate medie**
```php
add_shortcode('gallery', 'render_gallery');            → Shortcode{Tag: "gallery", Callback: "render_gallery"}
register_block_type('my-plugin/block', $args);        → Block{Name: "my-plugin/block"}
register_block_pattern('my-pattern', $args);          → BlockPattern{Name: "my-pattern"}
```

**D. Widgets & Admin Pages — prioritate medie**
```php
class MyWidget extends WP_Widget { }                  → Widget{Name: "MyWidget"}
add_menu_page('Title', 'Menu', 'cap', 'slug', 'fn'); → AdminPage{Slug: "slug"}
add_submenu_page('parent', 'Title', ...);             → AdminSubpage{Parent: "parent"}
register_setting('group', 'option');                  → Setting{Group: "group", Option: "option"}
```

**E. Plugin Header — prioritate medie**
```php
/**
 * Plugin Name: My Plugin
 * Version: 1.0.0
 * Author: Razvan
 * Text Domain: my-plugin
 */
```
→ `PluginHeader{Name: "My Plugin", Version: "1.0.0", Author: "Razvan", TextDomain: "my-plugin"}`

**F. Oxygen Builder — extensie dedicată**
```php
// Custom Element detection — clasă extends OxyEl
class MyElement extends OxyEl {
    function init() {}      // inițializare
    function name() {}      // display name în builder
    function slug() {}      // identificator unic
    function icon() {}      // icon SVG
    function controls() {}  // controale editor (left pane)
    function render() {}    // output HTML
}
// → OxygenElement{Name: "MyElement", Slug: din slug(), Methods: [...]}

// Oxygen stochează layout în JSON (nu shortcodes):
//   Meta key: ct_builder_json → tree de sections/columns/divs/elements
//   Custom post types: ct_template (templates), oxy_user_library (componente reusable)
//   Code Blocks: element cu tip "code-block" — conține PHP inline, executat la the_post
```

**G. WooCommerce — extensie dedicată**
```php
add_action('woocommerce_before_cart', 'my_fn');       → WC_Hook{Area: "cart", Hook: "before_cart"}
wc_get_product($id);                                 → WC_API{Function: "wc_get_product"}
// Toate hooks cu prefix "woocommerce_" → detectabile automat
// WooCommerce definește și propriile post types: product, shop_order, shop_coupon
```

**Detectare automată WordPress vs Laravel vs plain PHP:**
```go
func detectPHPFramework(rootPath string) string {
    // WordPress indicators
    if fileExists("wp-config.php") || dirExists("wp-content/") ||
       headerContains("Plugin Name:") || headerContains("Theme Name:") {
        return "wordpress"
    }
    // Laravel indicators
    if fileExists("artisan") || composerHas("laravel/framework") {
        return "laravel"
    }
    return "php" // generic
}
```

**Implementare concretă — pași:**

1. **Creare `pkg/parser/php/wordpress/types.go`** — definire tipuri: `WPHook`, `PostType`, `Taxonomy`, `Shortcode`, `Block`, `Widget`, `AdminPage`, `PluginHeader`, `WordPressInfo` (agregator)
2. **Creare `pkg/parser/php/wordpress/hooks.go`** — AST visitor care caută `ExprFunctionCall` cu name `add_action`/`add_filter`/`do_action`/`apply_filters`/`remove_action`/`has_filter`, extrage argumentele (hook name, callback, priority, accepted args)
3. **Creare `pkg/parser/php/wordpress/post_types.go`** — detectare `register_post_type`, `register_taxonomy`, `register_meta`
4. **Creare `pkg/parser/php/wordpress/shortcodes.go`** — detectare `add_shortcode`
5. **Creare `pkg/parser/php/wordpress/blocks.go`** — detectare `register_block_type`, `register_block_pattern`
6. **Creare `pkg/parser/php/wordpress/widgets.go`** — detectare clasă extends `WP_Widget`
7. **Creare `pkg/parser/php/wordpress/admin.go`** — detectare `add_menu_page`, `add_submenu_page`, `register_setting`
8. **Creare `pkg/parser/php/wordpress/plugin_header.go`** — parsing comentariu header PHP (regex pe primele linii)
9. **Creare `pkg/parser/php/wordpress/analyzer.go`** — coordinator (exact ca `laravel/analyzer.go`), orchestrează sub-analyzeri
10. **Creare sub-extensii `oxygen/` și `woocommerce/`** — detectare patterns specifice (OxyEl, woocommerce_ hooks)
11. **Integrare în `pkg/parser/php/analyzer.go`** — adapter care detectează framework (WP vs Laravel) și apelează sub-analyzerul potrivit
12. **Teste** — unit tests per sub-analyzer + integration test pe cod WP real

**Efort estimat:** ~2-3 zile (bazat pe experiența Laravel care a fost similar ca structură)

**Beneficii:**
- AI-ul va putea naviga pluginuri WordPress la nivel semantic: „arată-mi toate hooks-urile din plugin" sau „ce custom post types definește?"
- Oxygen Builder: înțelegere componente custom, Code Blocks, template hierarchy
- WooCommerce: navigare hooks specifice (cart, checkout, product, order)
- Pattern-ul Laravel e dovedit → zero risc arhitectural


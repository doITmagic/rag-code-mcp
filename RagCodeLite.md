# RagCode Lite: Concept Arhitectural (SQLite)

## Obiectiv 🎯
Dezvoltarea unui mod de operare „Lite” pentru **RagCode MCP**, conceput special pentru proiecte mici și medii. Scopul este **eliminarea completă a dependenței de Qdrant (Docker) și a modelelor LLM de embedding**, oferind utilizatorilor un setup instant, cu zero configurări (0-config), rulând direct dintr-un singur executabil compilat cross-platform.

## De ce SQLite și nu Baze Documentare sau Vectoriale Embedded? 🧐
- **Nu CGO / C++**: Integrarea motoarelor vectoriale embedded sau a librăriilor LLM locale (precum `llama.cpp` sau `sqlite-vec`) strică portabilitatea și ușurința cross-compilării pur Go.
- **Baze Documentare (ex. Bleve, CloverDB)**: Oferă căutare lexicală excelentă (TF-IDF/BM25), **dar** sunt slabe la a gestiona eficient *relațiile* dintre documente.
- **SQLite (Alegerea Câștigătoare)**:
  1. Suportă nativ Full-Text Search avansat prin extensia **FTS5** (cu rankare algoritmică tip BM25, simulând excelent „similitudinea” codului).
  2. Este **regele Relațiilor (JOIN-uri)**: Extrem de rapid în maparea graf-ului sintetic al codului sursă.

---

## Arhitectura "Code Graph" (Rețea Semnatică Fără LLM) 🕸️

Ideea principală este de a compensa lipsa „înțelegerii vectoriale semantice” printr-o **extragere strictă, hard-coded a relațiilor (AST)** structurale din cod cu ajutorul Tree-sitter, direct din parserele multi-language (Go, PHP, Python, etc.).

### 1. Modelul de Date (SQLite Schema)
Spre deosebire de un vector-store care stochează un payload "plat", RagCode Lite va reprezenta proiectul ca un graf Relațional:

- **Tabel `nodes` / `symbols`**:
  - `id` (hash unic)
  - `name` (numele clasei, funcției)
  - `type` (class, function, interface, variable)
  - `file_path`, `package`
  - `code_snippet` (conținutul brut)
  - *Se cuplează cu un index virtual FTS5 pe câmpul de code/name pentru căutare hibridă lexicală instantanee.*

- **Tabel `edges` / `relations`**:
  - `source_id` (Cine folosește/apelează)
  - `target_name` / `target_id` (Ce folosește/apelează)
  - `relation_type` (Enum)

### 2. Tipuri de Relații Extrase (Multi-Language)
- `IMPLEMENTS`: Ex: `class MyController implements ControllerInterface`
- `HAS_METHOD`: Ex: `class User` deține `func login()`
- `USES_TYPE`: Ex: O funcție primește ca parametru un obiect instanțiat (`func process(u User)` unde `User` e importat din alt fișier).
- `CALLS`: O funcție apelează o altă funcție.

### 3. Faza de Căutare ("Smart Context Resolution")

Aici stă adevărata forță a modului Lite, depășind limitările unui Vector Store clasic RAG.

**Problema actuală (cu Vectori/RAG clasic):**  
Când un AI cere `process()`, baza de date aduce doar funcția `process`. AI-ul realizează că funcția are un parametru `u User`, dar nu știe structura lui `User`. Va trebui să consume timp și tokeni pentru a face un al doilea apel `mcp_search_code("User")` ca să îi descopere detaliile (sperând într-o potrivire de embeddings).

**Soluția RagCode Lite (SQLite Code Graph):**
1. MCP-ul primește un query pentru o resursă (ex: `process`).
2. Găsește `node`-ul în funcție de nume/conținut prin FTS5 SQLite.
3. Înainte de a trimite răspunsul către AI, RagCode execută un **Query Relational Recursiv (JOIN)**: *„Adu-mi tot codul nodului `process` ȘI (WHERE) toate definițiile nodurilor target legate de acest nod prin relația `USES_TYPE` sau `CALLS`”*.
4. **Rezultat**: AI-ul primește instantaneu definiția funcției `process` **împreună** cu structura/codul pentru `User` exact din fișierul original de unde a fost importat. Totul se rezolvă într-o singură mișcare ultra-exactă.

## Următorii Pași de Dezvoltare 🚀
1. **Parsere (Tree-sitter)**: 
   - Modificarea funcțiilor Analyze din `pkg/parser/*` pentru a captura nu doar `Symbol`, ci și o listă nativă de `Relation` care rezolvă AST-ul de importuri și tipuri de parametrii.
2. **Storage SQLite Implementare**:
   - Finalizarea fișierului `pkg/storage/sqlite.go` curent, transformându-l într-un manager de tabel dublu (`symbols` + `relations`) cu extensie FTS5 pentru căscări eficiente.
3. **Căutarea Grafului**:
   - Implementarea rezoluției contextuale la nivelul de Search, unde un `SearchResult` este populat cu "Dependințe" adiționale, oferind AI-ului context absolut perfect fără "ghicit" matematic pe vectori.

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

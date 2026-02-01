# Analiză: Indexare Automată și Mesaj de Eroare

**Data:** 2025-11-21  
**Problema:** Mesaj "❌ Workspace '/home' is not indexed yet" la accesarea tool-urilor

---

## 🔍 Problema Identificată

Când un tool MCP (ex: `search_code`, `get_function_details`) este apelat, utilizatorul primește mesajul:

```
❌ Workspace '/home' is not indexed yet.

To enable this operation, please call the 'index_workspace' tool first with:
{
  "file_path": "/home"
}

Details:
- Workspace: /home
- Collection: coderag-2cc974af6afc (not created yet)
```

### Cauza Principală

**Workspace-ul detectat este `/home` în loc de directorul real al proiectului!**

Aceasta se întâmplă când:
1. **File path-ul furnizat nu conține markeri de workspace** (`.git`, `go.mod`, `composer.json`, etc.)
2. **Detector-ul urcă în arborele de directoare** până la `/home` (sau chiar `/`)
3. **Nu găsește niciun marker**, deci folosește **fallback-ul** (linia 115-124 din `detector.go`)

---

## 📋 Fluxul Normal de Indexare Automată

### Cum AR TREBUI să funcționeze:

```
1. Tool apelat: search_code({ file_path: "/home/user/project/src/main.go" })
   ↓
2. DetectWorkspace() → caută markeri urcând în arbore
   ↓
3. Găsește /home/user/project/.git → workspace root = /home/user/project
   ↓
4. GetMemoryForWorkspaceLanguage(workspace, "go")
   ↓
5. Verifică dacă colecția "coderag-abc123-go" există în Qdrant
   ↓
6. Dacă NU există:
   a. Creează colecția
   b. Dacă config.Workspace.AutoIndex == true:
      → Pornește indexarea în background (goroutine)
   c. Returnează memoria (goală, dar indexarea rulează)
   ↓
7. Tool-ul poate căuta imediat (rezultate apar pe măsură ce indexarea progresează)
```

### Configurația Actuală

În `config.yaml` (linia 26):
```yaml
workspace:
  enabled: true
  auto_index: true  # ✅ Activat corect
  max_workspaces: 10
```

---

## 🐛 Cauze Posibile

### 1. **Path-ul furnizat nu conține markeri de workspace** ❌

**Exemplu problematic:**
```json
{
  "file_path": "/home/razvan/test.go"
}
```

**Ce se întâmplă:**
- Detector-ul urcă: `/home/razvan` → `/home` → `/`
- Nu găsește `.git`, `go.mod`, etc.
- Folosește fallback: workspace = `/home` (directorul părinte al fișierului)
- Colecția devine: `coderag-2cc974af6afc` (hash pentru `/home`)

**Soluție:**
- Asigură-te că path-ul este într-un proiect valid cu markeri
- Exemplu corect: `/home/razvan/go/src/github.com/doITmagic/coderag-mcp/internal/tools/search.go`

### 2. **Markerii de workspace lipsesc din proiect** ⚠️

Dacă directorul proiectului nu are `.git`, `go.mod`, `composer.json`, etc., detector-ul nu poate identifica workspace-ul.

**Verificare:**
```bash
# Verifică dacă proiectul are markeri
ls -la /path/to/project | grep -E '\.git|go\.mod|composer\.json|package\.json'
```

**Soluție:**
```bash
# Pentru un proiect Go
cd /path/to/project
git init  # sau go mod init

# Pentru un proiect PHP
composer init
```

### 3. **Indexarea automată nu pornește din cauza unei erori** 🔴

Chiar dacă `auto_index: true`, indexarea poate eșua din cauza:

#### a) **Ollama nu rulează sau modelele lipsesc**

**Verificare:**
```bash
# Verifică dacă Ollama rulează
curl http://localhost:11434/api/tags

# Verifică modelele instalate
ollama list | grep nomic-embed-text
```

**Soluție:**
```bash
# Pornește Ollama
ollama serve &

# Descarcă modelul de embeddings
ollama pull nomic-embed-text
```

#### b) **Qdrant nu rulează sau nu este accesibil**

**Verificare:**
```bash
# Verifică dacă Qdrant rulează
curl http://localhost:6333/readyz

# Verifică colecțiile existente
curl http://localhost:6333/collections | jq .
```

**Soluție:**
```bash
# Pornește Qdrant cu Docker
docker run -d --name qdrant \
  -p 6333:6333 -p 6334:6334 \
  -v ~/.local/share/qdrant:/qdrant/storage \
  qdrant/qdrant

# Sau folosește scriptul de instalare
~/.local/share/coderag/start.sh
```

#### c) **Eroare la crearea colecției (dimensiune embedding incorectă)**

Dacă `m.llm.Embed(ctx, "test")` eșuează (linia 304 din `manager.go`), colecția nu se creează.

**Verificare în log-uri:**
```bash
# Caută erori în log-urile MCP
tail -f ~/.local/state/coderag/mcp.log | grep -i "failed to get embedding"
```

### 4. **Logica de verificare a colecției este prea strictă** ⚠️

În `search_local_index.go` (liniile 142-161), tool-ul verifică dacă colecția există **DUPĂ** ce `GetMemoryForWorkspaceLanguage()` ar fi trebuit să o creeze.

**Problema:**
- `GetMemoryForWorkspaceLanguage()` creează colecția și pornește indexarea în background
- Dar tool-ul verifică imediat dacă colecția există
- Dacă verificarea eșuează (race condition), returnează mesajul de eroare

**Cod problematic:**
```go
// Linia 103: Obține memoria (creează colecția dacă nu există)
workspaceMem, err := t.workspaceManager.GetMemoryForWorkspaceLanguage(ctx, workspaceInfo, language)
if err != nil {
    // Returnează eroare dacă crearea eșuează
    return "❌ Workspace is not indexed yet...", nil
}

// Linia 142-161: Verifică DIN NOU dacă colecția există (redundant!)
if checker, ok := workspaceMem.(CollectionChecker); ok {
    exists, checkErr := checker.CollectionExists(ctx, collectionName)
    if checkErr != nil || !exists {
        // Returnează eroare chiar dacă colecția tocmai a fost creată
        return "❌ Workspace is not indexed yet...", nil
    }
}
```

---

## ✅ Soluții Recomandate

### Soluție 1: **Îmbunătățește mesajul de eroare pentru fallback workspace**

Când workspace-ul detectat este `/home`, `/`, sau alt director suspect, afișează un mesaj mai clar:

```go
// În detector.go, linia 115-124
if fallbackDir == "/" || fallbackDir == os.Getenv("HOME") {
    return nil, fmt.Errorf(
        "could not detect workspace for file '%s'.\n\n" +
        "Please ensure the file is inside a project directory with workspace markers like:\n" +
        "- .git (Git repository)\n" +
        "- go.mod (Go project)\n" +
        "- composer.json (PHP project)\n" +
        "- package.json (Node.js project)",
        absPath,
    )
}
```

### Soluție 2: **Elimină verificarea redundantă a colecției**

În `search_local_index.go`, elimină verificarea de la liniile 142-161, deoarece `GetMemoryForWorkspaceLanguage()` deja garantează că colecția există sau returnează eroare.

```go
// ÎNAINTE (liniile 103-161):
workspaceMem, err := t.workspaceManager.GetMemoryForWorkspaceLanguage(ctx, workspaceInfo, language)
if err != nil {
    return "❌ Workspace is not indexed yet...", nil
}

// Verificare redundantă (ȘTERGE ACEST BLOC)
if checker, ok := workspaceMem.(CollectionChecker); ok {
    exists, checkErr := checker.CollectionExists(ctx, collectionName)
    if checkErr != nil || !exists {
        return "❌ Workspace is not indexed yet...", nil
    }
}

// DUPĂ (simplificat):
workspaceMem, err := t.workspaceManager.GetMemoryForWorkspaceLanguage(ctx, workspaceInfo, language)
if err != nil {
    return fmt.Sprintf("❌ Failed to initialize workspace: %v", err), nil
}

// Verifică dacă indexarea este în curs
if t.workspaceManager.IsIndexing(indexKey) {
    return "⏳ Workspace is being indexed in the background. Try again in a few moments.", nil
}
```

### Soluție 3: **Adaugă logging pentru debugging**

În `manager.go`, adaugă log-uri pentru a urmări fluxul de indexare:

```go
// Linia 288
log.Printf("📦 Workspace '%s' language '%s' not indexed yet, creating collection...", info.Root, language)
log.Printf("   Detected from path: %s", /* path original */)
log.Printf("   Workspace ID: %s", info.ID)
log.Printf("   Collection name: %s", collectionName)
```

### Soluție 4: **Validează workspace-ul înainte de indexare**

În `manager.go`, adaugă validare pentru workspace-uri suspecte:

```go
// La începutul GetMemoryForWorkspaceLanguage
if info.Root == "/" || info.Root == os.Getenv("HOME") {
    return nil, fmt.Errorf(
        "invalid workspace root '%s'. " +
        "Please provide a file path inside a valid project directory with workspace markers.",
        info.Root,
    )
}
```

---

## 🧪 Testare

### Test 1: Verifică detecția workspace-ului

```bash
# Rulează MCP server în mod debug
MCP_LOG_LEVEL=debug ~/.local/share/coderag/bin/coderag-mcp

# În alt terminal, apelează tool-ul
echo '{
  "jsonrpc": "2.0",
  "id": 1,
  "method": "tools/call",
  "params": {
    "name": "search_code",
    "arguments": {
      "query": "test",
      "file_path": "/home/razvan/go/src/github.com/doITmagic/coderag-mcp/internal/tools/search.go"
    }
  }
}' | ~/.local/share/coderag/bin/coderag-mcp
```

### Test 2: Verifică indexarea automată

```bash
# Șterge colecția existentă
curl -X DELETE http://localhost:6333/collections/coderag-1cb9c48c45f0-go

# Apelează search_code (ar trebui să declanșeze indexarea automată)
# Verifică log-urile pentru:
# - "📦 Workspace ... not indexed yet, creating collection..."
# - "🚀 Starting background indexing for workspace: ..."
# - "✅ Workspace language indexed successfully in ..."
```

---

## 📊 Rezumat

| Problemă | Cauză | Soluție |
|----------|-------|---------|
| Workspace detectat = `/home` | Path fără markeri | Validare workspace + mesaj mai clar |
| Indexarea nu pornește | Ollama/Qdrant offline | Verificare servicii în `start.sh` |
| Mesaj "not indexed" chiar dacă colecția există | Verificare redundantă | Elimină verificarea după `GetMemoryForWorkspaceLanguage()` |
| Race condition la verificarea colecției | Verificare prea rapidă | Așteaptă indexarea sau verifică `IsIndexing()` |

---

## 🔧 Implementare Recomandată

**Prioritate 1 (Critical):**
1. Validează workspace-ul în `GetMemoryForWorkspaceLanguage()` (Soluția 4)
2. Îmbunătățește mesajul de eroare în `detector.go` (Soluția 1)

**Prioritate 2 (High):**
3. Elimină verificarea redundantă în `search_local_index.go` (Soluția 2)
4. Adaugă logging pentru debugging (Soluția 3)

**Prioritate 3 (Medium):**
5. Testează fluxul complet cu scripturile de test existente
6. Documentează comportamentul în README.md

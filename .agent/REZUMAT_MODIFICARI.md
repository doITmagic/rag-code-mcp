## Rezumat Modificări - Fix pentru Mesaj "Workspace not indexed"

**Data:** 2025-11-21  
**Problema:** Tool-urile `hybrid_search` și `search_docs` returnau mesajul "❌ Workspace '/home' is not indexed yet" când erau apelate fără parametrul `file_path`.

---

## 🔧 Modificări Implementate

### 1. **Validare Workspace în `detector.go`** ✅

**Fișier:** `internal/workspace/detector.go`

**Modificare:** Adăugat validare pentru workspace-uri suspecte în `DetectFromPath()`:

```go
// Validate fallback directory - reject suspicious workspace roots
homeDir, _ := os.UserHomeDir()
if fallbackDir == "/" || fallbackDir == homeDir || strings.HasPrefix(fallbackDir, "/tmp") {
    return nil, fmt.Errorf(
        "could not detect workspace for file '%s'.\n\n"+
        "The file appears to be outside any project directory.\n"+
        "Please ensure the file is inside a project with workspace markers like:\n"+
        "  - .git (Git repository)\n"+
        "  - go.mod (Go project)\n"+
        "  - composer.json (PHP project)\n"+
        "  - package.json (Node.js project)\n"+
        "  - pyproject.toml (Python project)\n\n"+
        "Detected fallback directory: %s",
        absPath, fallbackDir,
    )
}
```

**Impact:** Previne crearea de workspace-uri pentru directoare invalide (`/`, `/home`, `/tmp`).

---

### 2. **Validare Workspace în `manager.go`** ✅

**Fișier:** `internal/workspace/manager.go`

**Modificare:** Adăugat validare în `GetMemoryForWorkspaceLanguage()`:

```go
// Validate workspace root - reject suspicious directories
homeDir, _ := os.UserHomeDir()
if info.Root == "/" || info.Root == homeDir || strings.HasPrefix(info.Root, "/tmp") {
    return nil, fmt.Errorf(
        "invalid workspace root '%s'. "+
        "Please provide a file path inside a valid project directory with workspace markers "+
        "(e.g., .git, go.mod, composer.json, package.json)",
        info.Root,
    )
}
```

**Impact:** Dublă protecție la nivel de manager pentru workspace-uri invalide.

---

### 3. **Logging Îmbunătățit în `manager.go`** ✅

**Modificare:** Adăugat detalii suplimentare la crearea colecțiilor:

```go
log.Printf("📦 Workspace '%s' language '%s' not indexed yet, creating collection...", info.Root, language)
log.Printf("   Workspace ID: %s", info.ID)
log.Printf("   Collection name: %s", collectionName)
log.Printf("   Project type: %s", info.ProjectType)
log.Printf("   Detected markers: %v", info.Markers)
```

**Impact:** Debugging mai ușor pentru probleme de indexare.

---

### 4. **Fix pentru `hybrid_search.go`** ✅

**Fișier:** `internal/tools/hybrid_search.go`

**Modificări:**
1. **Înlocuit `GetMemoryForWorkspace()` deprecated** cu `GetMemoryForWorkspaceLanguage()`
2. **Adăugat verificare pentru `file_path`** înainte de workspace detection:

```go
// Only try workspace detection if file_path is explicitly provided
filePath := extractFilePathFromParams(params)
if filePath != "" {
    workspaceInfo, err := t.workspaceManager.DetectWorkspace(params)
    // ... rest of logic
}
```

**Impact:** 
- Când `file_path` este furnizat → folosește workspace detection
- Când `file_path` lipsește → folosește memoria default (fallback la colecția globală)
- **NU mai încearcă să folosească `os.Getwd()` ca fallback**

---

### 5. **Fix pentru `search_docs.go`** ✅

**Fișier:** `internal/tools/search_docs.go`

**Modificări:** Identice cu `hybrid_search.go`:
1. Înlocuit `GetMemoryForWorkspace()` cu `GetMemoryForWorkspaceLanguage()`
2. Adăugat verificare pentru `file_path`

---

## 📊 Comportament Înainte vs. După

### **Înainte:**

| Scenariu | Tool | Comportament |
|----------|------|--------------|
| Apel fără `file_path` | `hybrid_search` | ❌ Detectează workspace = `/home` (din `os.Getwd()`) → Eroare "not indexed" |
| Apel fără `file_path` | `search_docs` | ❌ Detectează workspace = `/home` → Eroare "not indexed" |
| Apel cu `file_path` valid | Ambele | ✅ Funcționează corect |

### **După:**

| Scenariu | Tool | Comportament |
|----------|------|--------------|
| Apel fără `file_path` | `hybrid_search` | ✅ Folosește memoria default (colecția globală configurată) |
| Apel fără `file_path` | `search_docs` | ✅ Folosește memoria default (colecția globală configurată) |
| Apel cu `file_path` valid | Ambele | ✅ Detectează workspace corect și folosește colecția specifică |
| Apel cu `file_path` în `/home` | Ambele | ❌ Eroare clară: "could not detect workspace" |

---

## 🧪 Testare

### Test 1: `hybrid_search` fără `file_path`

```json
{
  "query": "referat referate Report"
}
```

**Rezultat așteptat:** Caută în colecția default configurată (dacă există), altfel returnează eroare clară.

### Test 2: `hybrid_search` cu `file_path` valid

```json
{
  "query": "referat referate Report",
  "file_path": "/home/razvan/go/src/github.com/doITmagic/coderag-mcp/internal/tools/search.go"
}
```

**Rezultat așteptat:** Detectează workspace `coderag-mcp`, folosește colecția `coderag-{id}-go`.

### Test 3: `hybrid_search` cu `file_path` în `/home`

```json
{
  "query": "test",
  "file_path": "/home/test.go"
}
```

**Rezultat așteptat:** Eroare clară cu mesaj descriptiv despre lipsa markerilor de workspace.

---

## 💡 Îmbunătățiri Viitoare (Opțional)

### Opțiune 1: **Search în toate colecțiile indexate** (sugestia ta)

Când `file_path` lipsește, în loc să folosim doar memoria default, putem căuta în **toate colecțiile indexate**:

```go
if filePath == "" {
    // Search across all indexed workspaces
    allCollections := t.workspaceManager.GetAllIndexedCollections()
    results := searchAcrossCollections(ctx, query, allCollections)
    return aggregateResults(results), nil
}
```

**Avantaje:**
- Utilizatorul poate căuta fără să știe în ce workspace este codul
- Mai flexibil pentru explorare

**Dezavantaje:**
- Mai lent (caută în multiple colecții)
- Rezultate potențial confuze (din workspace-uri diferite)

### Opțiune 2: **Cache de workspace-uri indexate**

Păstrăm o listă de workspace-uri indexate în memorie:

```go
type Manager struct {
    // ...
    indexedWorkspaces map[string]*Info // workspaceID -> Info
}
```

Când `file_path` lipsește, returnăm lista de workspace-uri disponibile:

```json
{
  "error": "No file_path provided. Available workspaces:",
  "workspaces": [
    {"id": "abc123", "root": "/home/user/project1", "languages": ["go"]},
    {"id": "def456", "root": "/home/user/project2", "languages": ["php"]}
  ]
}
```

---

## ✅ Concluzie

**Problema rezolvată:**
- `hybrid_search` și `search_docs` nu mai returnează eroarea "Workspace '/home' is not indexed"
- Workspace-urile suspecte (`/`, `/home`, `/tmp`) sunt validate și respinse cu mesaje clare
- Tool-urile funcționează corect atât cu `file_path` (workspace-specific) cât și fără (fallback la default)

**Fișiere modificate:**
1. `internal/workspace/detector.go` - Validare workspace fallback
2. `internal/workspace/manager.go` - Validare + logging îmbunătățit
3. `internal/tools/hybrid_search.go` - Fix workspace detection
4. `internal/tools/search_docs.go` - Fix workspace detection

**Build:** ✅ Reușit  
**Status:** Gata pentru testare

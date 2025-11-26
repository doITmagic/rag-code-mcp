# Python Code Analyzer

Analizor de cod Python pentru extragerea simbolurilor, structurii și relațiilor din fișiere Python. Indexează codul pentru căutare semantică în Qdrant.

## Status: ✅ FULLY IMPLEMENTED

---

## 🎯 Ce Face Acest Analizor?

Analizorul Python parsează fișierele `.py` și extrage:
1. **Simboluri** - clase, metode, funcții, variabile, constante
2. **Relații** - moșteniri, dependențe, apeluri de metode
3. **Metadate** - decoratori, type hints, docstrings

Informațiile sunt convertite în `CodeChunk`-uri care sunt apoi indexate în Qdrant pentru căutare semantică.

---

## 📊 Fluxul de Date

```
┌─────────────────┐     ┌──────────────────┐     ┌─────────────────┐
│  Fișiere .py    │────▶│  Python Analyzer │────▶│   CodeChunks    │
│  (cod sursă)    │     │  (regex parsing) │     │   (structurat)  │
└─────────────────┘     └──────────────────┘     └────────┬────────┘
                                                          │
                                                          ▼
                                                 ┌─────────────────┐
                                                 │     Qdrant      │
                                                 │  (vector store) │
                                                 └─────────────────┘
```

---

## 🔍 Ce Indexăm

### 1. Clase (`type: "class"`)

```python
@dataclass
class User(BaseModel, LoggingMixin, metaclass=ABCMeta):
    """Reprezintă un utilizator în sistem."""
    name: str
    email: str
```

**Informații extrase:**
| Câmp | Valoare | Descriere |
|------|---------|-----------|
| `name` | `"User"` | Numele clasei |
| `bases` | `["BaseModel", "LoggingMixin"]` | Clasele părinte (moștenire) |
| `decorators` | `["dataclass"]` | Decoratorii aplicați |
| `is_abstract` | `true` | Dacă e clasă abstractă (ABC) |
| `is_dataclass` | `true` | Dacă e decorată cu @dataclass |
| `is_enum` | `false` | Dacă moștenește din Enum |
| `is_protocol` | `false` | Dacă e Protocol (typing) |
| `is_mixin` | `true` | Dacă e/folosește mixin |
| `metaclass` | `"ABCMeta"` | Metaclasa specificată |
| `dependencies` | `["BaseModel", "LoggingMixin"]` | Toate dependențele clasei |
| `docstring` | `"Reprezintă un utilizator..."` | Documentația clasei |

### 2. Metode (`type: "method"`)

```python
class UserService:
    async def get_user(self, user_id: int) -> User:
        """Returnează un utilizator după ID."""
        self.validate_id(user_id)
        user = await self.repository.find(user_id)
        return user
```

**Informații extrase:**
| Câmp | Valoare | Descriere |
|------|---------|-----------|
| `name` | `"get_user"` | Numele metodei |
| `signature` | `"async def get_user(self, user_id: int) -> User"` | Semnătura completă |
| `class_name` | `"UserService"` | Clasa părinte |
| `parameters` | `[{name: "user_id", type: "int"}]` | Parametrii cu tipuri |
| `return_type` | `"User"` | Tipul returnat |
| `is_async` | `true` | Dacă e metodă async |
| `is_static` | `false` | Dacă e @staticmethod |
| `is_classmethod` | `false` | Dacă e @classmethod |
| `calls` | `[{name: "validate_id", receiver: "self"}, ...]` | Metodele apelate |
| `type_deps` | `["User"]` | Tipurile folosite (dependențe) |
| `docstring` | `"Returnează un utilizator..."` | Documentația metodei |

### 3. Funcții (`type: "function"`)

```python
@lru_cache(maxsize=100)
async def fetch_data(url: str) -> dict:
    """Descarcă date de la URL."""
    yield from process(url)
```

**Informații extrase:**
| Câmp | Valoare | Descriere |
|------|---------|-----------|
| `name` | `"fetch_data"` | Numele funcției |
| `signature` | `"async def fetch_data(url: str) -> dict"` | Semnătura |
| `is_async` | `true` | Dacă e async |
| `is_generator` | `true` | Dacă folosește yield |
| `decorators` | `["lru_cache"]` | Decoratorii aplicați |

### 4. Proprietăți (`type: "property"`)

```python
class User:
    @property
    def full_name(self) -> str:
        return f"{self.first_name} {self.last_name}"
    
    @full_name.setter
    def full_name(self, value: str):
        self.first_name, self.last_name = value.split()
```

**Informații extrase:**
| Câmp | Valoare | Descriere |
|------|---------|-----------|
| `name` | `"full_name"` | Numele proprietății |
| `type` | `"str"` | Tipul returnat |
| `has_getter` | `true` | Are getter (@property) |
| `has_setter` | `true` | Are setter (@x.setter) |
| `has_deleter` | `false` | Are deleter (@x.deleter) |

### 5. Constante (`type: "const"`)

```python
MAX_CONNECTIONS: int = 100
API_BASE_URL = "https://api.example.com"
```

**Informații extrase:**
- Detectate prin convenția UPPER_CASE
- Tipul și valoarea sunt extrase

### 6. Variabile (`type: "var"`)

```python
logger = logging.getLogger(__name__)
default_config: Config = Config()
```

---

## 🔗 Detectarea Relațiilor

### Dependency Graph

Analizorul construiește un graf de dependențe între clase:

```python
class OrderService:
    repository: OrderRepository  # → dependency
    
    def create_order(self, user: User) -> Order:  # → dependencies: User, Order
        notification = NotificationService()  # → dependency (din calls)
        return Order(...)
```

**Dependențe detectate:**
- `OrderRepository` - din type hint pe variabilă
- `User` - din parametru
- `Order` - din return type
- `NotificationService` - din apeluri de metode

### Method Call Analysis

```python
def process(self, data):
    self.validate(data)           # → self.validate
    result = Helper.compute(data) # → Helper.compute (static call)
    super().process(data)         # → super().process
    save_to_db(result)            # → save_to_db (function call)
```

**Apeluri detectate:**
```json
{
  "calls": [
    {"name": "validate", "receiver": "self", "line": 2},
    {"name": "compute", "receiver": "Helper", "class_name": "Helper", "line": 3},
    {"name": "process", "receiver": "super()", "line": 4},
    {"name": "save_to_db", "line": 5}
  ]
}
```

---

## 🏗️ Structura Fișierelor

```
python/
├── types.go           # Tipuri: ModuleInfo, ClassInfo, MethodInfo, MethodCall, etc.
├── analyzer.go        # Implementare PathAnalyzer (1500+ linii)
├── api_analyzer.go    # Legacy APIAnalyzer (build-tagged out)
├── analyzer_test.go   # 26 teste comprehensive
└── README.md          # Această documentație
```

---

## 💻 Utilizare

### Analiză Standard

```go
import "github.com/doITmagic/rag-code-mcp/internal/ragcode/analyzers/python"

// Creare analizor (exclude test files by default)
analyzer := python.NewCodeAnalyzer()

// Analiză directoare/fișiere
chunks, err := analyzer.AnalyzePaths([]string{"./myproject"})

for _, chunk := range chunks {
    fmt.Printf("[%s] %s.%s\n", chunk.Type, chunk.Package, chunk.Name)
    fmt.Printf("  Dependencies: %v\n", chunk.Metadata["dependencies"])
}
```

### Cu Opțiuni

```go
// Include și fișierele de test
analyzer := python.NewCodeAnalyzerWithOptions(true)
```

---

## 🔌 Integrare

### Language Manager

Analizorul Python este selectat automat pentru:
- `python`, `py` - proiecte Python generice
- `django` - proiecte Django
- `flask` - proiecte Flask
- `fastapi` - proiecte FastAPI

### Detectare Workspace

Proiectele Python sunt detectate prin:
| Fișier | Descriere |
|--------|-----------|
| `pyproject.toml` | PEP 518 - Python modern |
| `setup.py` | Setuptools legacy |
| `requirements.txt` | Dependențe pip |
| `Pipfile` | Pipenv |

---

## 📋 Tipuri de CodeChunk

| Type | Descriere | Exemplu |
|------|-----------|---------|
| `class` | Definiție clasă | `class User(BaseModel):` |
| `method` | Metodă de clasă | `def get_user(self):` |
| `function` | Funcție module-level | `def helper():` |
| `property` | Proprietate @property | `@property def name(self):` |
| `const` | Constantă UPPER_CASE | `MAX_SIZE = 100` |
| `var` | Variabilă module-level | `logger = getLogger()` |

---

## 🏷️ Metadate Complete

### Class Metadata
```json
{
  "bases": ["BaseModel", "Mixin"],
  "decorators": ["dataclass"],
  "is_abstract": false,
  "is_dataclass": true,
  "is_enum": false,
  "is_protocol": false,
  "is_mixin": false,
  "metaclass": "",
  "dependencies": ["BaseModel", "Mixin", "User", "Order"]
}
```

### Method Metadata
```json
{
  "class_name": "UserService",
  "is_static": false,
  "is_classmethod": false,
  "is_async": true,
  "is_abstract": false,
  "decorators": ["cache"],
  "calls": [
    {"name": "validate", "receiver": "self", "line": 10},
    {"name": "save", "receiver": "self.repository", "line": 12}
  ],
  "type_deps": ["User", "Order"]
}
```

### Function Metadata
```json
{
  "is_async": true,
  "is_generator": false,
  "decorators": ["lru_cache"]
}
```

---

## 🧪 Testare

```bash
# Rulează toate testele (26 teste)
go test ./internal/ragcode/analyzers/python/

# Cu output verbose
go test -v ./internal/ragcode/analyzers/python/

# Test specific
go test -v -run TestMethodCallExtraction ./internal/ragcode/analyzers/python/

# Cu coverage
go test -cover ./internal/ragcode/analyzers/python/
```

---

## 🚫 Căi Excluse

Analizorul sare automat:
- `__pycache__/` - cache Python
- `.venv/`, `venv/`, `env/` - virtual environments
- `.git/` - Git
- `.tox/`, `.pytest_cache/`, `.mypy_cache/` - cache-uri
- `dist/`, `build/` - distribuții
- `test_*.py`, `*_test.py` - fișiere test (implicit)

---

## ⚠️ Limitări

| Limitare | Descriere |
|----------|-----------|
| **Regex-based** | Nu folosește AST Python complet - poate rata cazuri edge |
| **No Type Resolution** | Type hints sunt extrase ca stringuri, nu rezolvate |
| **Single-file** | Fiecare fișier e analizat independent |
| **No Runtime Info** | Nu execută codul, doar analiză statică |

---

## 🔮 Îmbunătățiri Viitoare

- [ ] Django: modele, views, URLs, forms
- [ ] Flask/FastAPI: route detection, dependency injection
- [ ] Type resolution: rezolvare type hints cross-file
- [ ] Import graph: graf complet de importuri
- [ ] Nested classes: clase definite în alte clase
- [ ] Comprehensions: list/dict/set comprehensions

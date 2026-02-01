# Python Code Analyzer

Code analyzer for extracting symbols, structure, and relationships from Python files. Indexes code for semantic search in Qdrant.

## Status: ✅ FULLY IMPLEMENTED

---

## 🎯 What This Analyzer Does

The Python analyzer parses `.py` files and extracts:
1. **Symbols** - classes, methods, functions, variables, constants
2. **Relationships** - inheritance, dependencies, method calls
3. **Metadata** - decorators, type hints, docstrings

Information is converted to `CodeChunk`s which are then indexed in Qdrant for semantic search.

---

## 📊 Data Flow

```
┌─────────────────┐     ┌──────────────────┐     ┌─────────────────┐
│   .py Files     │────▶│  Python Analyzer │────▶│   CodeChunks    │
│  (source code)  │     │  (regex parsing) │     │  (structured)   │
└─────────────────┘     └──────────────────┘     └────────┬────────┘
                                                          │
                                                          ▼
                                                 ┌─────────────────┐
                                                 │     Qdrant      │
                                                 │  (vector store) │
                                                 └─────────────────┘
```

---

## 🔍 What We Index

### 1. Classes (`type: "class"`)

```python
@dataclass
class User(BaseModel, LoggingMixin, metaclass=ABCMeta):
    """Represents a user in the system."""
    name: str
    email: str
```

**Extracted information:**
| Field | Value | Description |
|-------|-------|-------------|
| `name` | `"User"` | Class name |
| `bases` | `["BaseModel", "LoggingMixin"]` | Parent classes (inheritance) |
| `decorators` | `["dataclass"]` | Applied decorators |
| `is_abstract` | `true` | If it's an abstract class (ABC) |
| `is_dataclass` | `true` | If decorated with @dataclass |
| `is_enum` | `false` | If inherits from Enum |
| `is_protocol` | `false` | If it's a Protocol (typing) |
| `is_mixin` | `true` | If it is/uses a mixin |
| `metaclass` | `"ABCMeta"` | Specified metaclass |
| `dependencies` | `["BaseModel", "LoggingMixin"]` | All class dependencies |
| `docstring` | `"Represents a user..."` | Class documentation |

### 2. Methods (`type: "method"`)

```python
class UserService:
    async def get_user(self, user_id: int) -> User:
        """Returns a user by ID."""
        self.validate_id(user_id)
        user = await self.repository.find(user_id)
        return user
```

**Extracted information:**
| Field | Value | Description |
|-------|-------|-------------|
| `name` | `"get_user"` | Method name |
| `signature` | `"async def get_user(self, user_id: int) -> User"` | Complete signature |
| `class_name` | `"UserService"` | Parent class |
| `parameters` | `[{name: "user_id", type: "int"}]` | Parameters with types |
| `return_type` | `"User"` | Return type |
| `is_async` | `true` | If it's an async method |
| `is_static` | `false` | If it's @staticmethod |
| `is_classmethod` | `false` | If it's @classmethod |
| `calls` | `[{name: "validate_id", receiver: "self"}, ...]` | Called methods |
| `type_deps` | `["User"]` | Used types (dependencies) |
| `docstring` | `"Returns a user..."` | Method documentation |

### 3. Functions (`type: "function"`)

```python
@lru_cache(maxsize=100)
async def fetch_data(url: str) -> dict:
    """Downloads data from URL."""
    yield from process(url)
```

**Extracted information:**
| Field | Value | Description |
|-------|-------|-------------|
| `name` | `"fetch_data"` | Function name |
| `signature` | `"async def fetch_data(url: str) -> dict"` | Signature |
| `is_async` | `true` | If it's async |
| `is_generator` | `true` | If it uses yield |
| `decorators` | `["lru_cache"]` | Applied decorators |

### 4. Properties (`type: "property"`)

```python
class User:
    @property
    def full_name(self) -> str:
        return f"{self.first_name} {self.last_name}"
    
    @full_name.setter
    def full_name(self, value: str):
        self.first_name, self.last_name = value.split()
```

**Extracted information:**
| Field | Value | Description |
|-------|-------|-------------|
| `name` | `"full_name"` | Property name |
| `type` | `"str"` | Return type |
| `has_getter` | `true` | Has getter (@property) |
| `has_setter` | `true` | Has setter (@x.setter) |
| `has_deleter` | `false` | Has deleter (@x.deleter) |

### 5. Constants (`type: "const"`)

```python
MAX_CONNECTIONS: int = 100
API_BASE_URL = "https://api.example.com"
```

**Extracted information:**
- Detected by UPPER_CASE convention
- Type and value are extracted

### 6. Variables (`type: "var"`)

```python
logger = logging.getLogger(__name__)
default_config: Config = Config()
```

---

## 🔗 Relationship Detection

### Dependency Graph

The analyzer builds a dependency graph between classes:

```python
class OrderService:
    repository: OrderRepository  # → dependency
    
    def create_order(self, user: User) -> Order:  # → dependencies: User, Order
        notification = NotificationService()  # → dependency (from calls)
        return Order(...)
```

**Detected dependencies:**
- `OrderRepository` - from type hint on variable
- `User` - from parameter
- `Order` - from return type
- `NotificationService` - from method calls

### Method Call Analysis

```python
def process(self, data):
    self.validate(data)           # → self.validate
    result = Helper.compute(data) # → Helper.compute (static call)
    super().process(data)         # → super().process
    save_to_db(result)            # → save_to_db (function call)
```

**Detected calls:**
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

## 🏗️ File Structure

```
python/
├── types.go           # Types: ModuleInfo, ClassInfo, MethodInfo, MethodCall, etc.
├── analyzer.go        # PathAnalyzer implementation (1500+ lines)
├── api_analyzer.go    # Legacy APIAnalyzer (build-tagged out)
├── analyzer_test.go   # 26 comprehensive tests
└── README.md          # This documentation
```

---

## 💻 Usage

### Standard Analysis

```go
import "github.com/doITmagic/rag-code-mcp/internal/ragcode/analyzers/python"

// Create analyzer (excludes test files by default)
analyzer := python.NewCodeAnalyzer()

// Analyze directories/files
chunks, err := analyzer.AnalyzePaths([]string{"./myproject"})

for _, chunk := range chunks {
    fmt.Printf("[%s] %s.%s\n", chunk.Type, chunk.Package, chunk.Name)
    fmt.Printf("  Dependencies: %v\n", chunk.Metadata["dependencies"])
}
```

### With Options

```go
// Include test files
analyzer := python.NewCodeAnalyzerWithOptions(true)
```

---

## 🔌 Integration

### Language Manager

The Python analyzer is automatically selected for:
- `python`, `py` - generic Python projects
- `django` - Django projects
- `flask` - Flask projects
- `fastapi` - FastAPI projects

### Workspace Detection

Python projects are detected by:
| File | Description |
|------|-------------|
| `pyproject.toml` | PEP 518 - modern Python |
| `setup.py` | Setuptools legacy |
| `requirements.txt` | pip dependencies |
| `Pipfile` | Pipenv |

---

## 📋 CodeChunk Types

| Type | Description | Example |
|------|-------------|---------|
| `class` | Class definition | `class User(BaseModel):` |
| `method` | Class method | `def get_user(self):` |
| `function` | Module-level function | `def helper():` |
| `property` | @property | `@property def name(self):` |
| `const` | UPPER_CASE constant | `MAX_SIZE = 100` |
| `var` | Module-level variable | `logger = getLogger()` |

---

## 🏷️ Complete Metadata

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

## 🧪 Testing

```bash
# Run all tests (26 tests)
go test ./internal/ragcode/analyzers/python/

# With verbose output
go test -v ./internal/ragcode/analyzers/python/

# Specific test
go test -v -run TestMethodCallExtraction ./internal/ragcode/analyzers/python/

# With coverage
go test -cover ./internal/ragcode/analyzers/python/
```

---

## 🚫 Excluded Paths

The analyzer automatically skips:
- `__pycache__/` - Python cache
- `.venv/`, `venv/`, `env/` - virtual environments
- `.git/` - Git
- `.tox/`, `.pytest_cache/`, `.mypy_cache/` - caches
- `dist/`, `build/` - distributions
- `test_*.py`, `*_test.py` - test files (by default)

---

## ⚠️ Limitations

| Limitation | Description |
|------------|-------------|
| **Regex-based** | Doesn't use full Python AST - may miss edge cases |
| **No Type Resolution** | Type hints are extracted as strings, not resolved |
| **Single-file** | Each file is analyzed independently |
| **No Runtime Info** | Doesn't execute code, only static analysis |

---

## 🔮 Future Improvements

- [ ] Django: models, views, URLs, forms
- [ ] Flask/FastAPI: route detection, dependency injection
- [ ] Type resolution: cross-file type hint resolution
- [ ] Import graph: complete import graph
- [ ] Nested classes: classes defined inside other classes
- [ ] Comprehensions: list/dict/set comprehensions

# Python Code Analyzer

Python code analyzer for extracting symbols and structure from Python files.

## Status: ✅ FULLY IMPLEMENTED

## Features

### Symbol Extraction
- **Classes**: Full class parsing with inheritance, decorators, docstrings
- **Methods**: Instance, static, class methods with decorators
- **Functions**: Module-level functions with async/generator detection
- **Properties**: @property decorator support with getter/setter/deleter
- **Variables**: Module and class-level variables with type annotations
- **Constants**: UPPER_CASE naming convention detection
- **Imports**: Both `import X` and `from X import Y` styles

### Python-Specific Features
- **Type Hints**: Full support for Python 3.5+ type annotations
- **Decorators**: Detection of @staticmethod, @classmethod, @property, @abstractmethod, @dataclass
- **Async/Await**: Async function and method detection
- **Generators**: Yield-based generator detection
- **Docstrings**: Module, class, and function docstring extraction
- **Abstract Classes**: ABC inheritance and @abstractmethod detection
- **Dataclasses**: @dataclass decorator detection

## Structure

```
python/
├── types.go           # Python-specific types (ModuleInfo, ClassInfo, etc.)
├── analyzer.go        # PathAnalyzer implementation
├── api_analyzer.go    # Legacy APIAnalyzer (build-tagged out)
├── analyzer_test.go   # Comprehensive test suite
└── README.md          # This file
```

## Usage

```go
import "github.com/doITmagic/rag-code-mcp/internal/ragcode/analyzers/python"

// Create analyzer
analyzer := python.NewCodeAnalyzer()

// Analyze paths (files or directories)
chunks, err := analyzer.AnalyzePaths([]string{"./myproject"})
if err != nil {
    log.Fatal(err)
}

// Process chunks
for _, chunk := range chunks {
    fmt.Printf("%s: %s (%s)\n", chunk.Type, chunk.Name, chunk.Language)
}
```

## Integration

The Python analyzer is automatically selected by `language_manager.go` for:
- `python` project type
- `py` project type
- `django` project type
- `flask` project type
- `fastapi` project type

### Workspace Detection

Python projects are detected by these markers:
- `pyproject.toml` (PEP 518 - modern Python)
- `setup.py` (legacy setuptools)
- `requirements.txt` (pip dependencies)
- `Pipfile` (pipenv)

## CodeChunk Types

| Type | Description |
|------|-------------|
| `class` | Python class definition |
| `method` | Class method (instance, static, or class method) |
| `function` | Module-level function |
| `property` | @property decorated method |
| `const` | UPPER_CASE module-level constant |
| `var` | Module-level variable |

## Metadata Fields

### Class Chunks
```go
Metadata: map[string]any{
    "bases":        []string{"BaseClass"},
    "decorators":   []string{"dataclass"},
    "is_abstract":  true,
    "is_dataclass": true,
}
```

### Method Chunks
```go
Metadata: map[string]any{
    "class_name":     "MyClass",
    "is_static":      false,
    "is_classmethod": false,
    "is_async":       true,
    "decorators":     []string{"abstractmethod"},
}
```

### Function Chunks
```go
Metadata: map[string]any{
    "is_async":     true,
    "is_generator": false,
    "decorators":   []string{"lru_cache"},
}
```

## Testing

```bash
# Run Python analyzer tests
go test ./internal/ragcode/analyzers/python/

# Run with verbose output
go test -v ./internal/ragcode/analyzers/python/

# Run specific test
go test -v -run TestExtractClasses ./internal/ragcode/analyzers/python/
```

## Excluded Paths

The analyzer automatically skips:
- `__pycache__/` directories
- `.venv/`, `venv/`, `env/`, `.env/` virtual environments
- `.git/` directories
- `.tox/`, `.pytest_cache/`, `.mypy_cache/` cache directories
- `dist/`, `build/` distribution directories
- `test_*.py`, `*_test.py` test files (by default)

## Limitations

- **No AST Parser**: Uses regex-based parsing instead of a full Python AST parser
  - Handles most common Python patterns correctly
  - May miss edge cases with complex nested structures
- **No Type Resolution**: Type hints are extracted as strings, not resolved
- **No Cross-File Analysis**: Each file is analyzed independently

## Future Enhancements

- [ ] Django framework support (models, views, URLs)
- [ ] Flask/FastAPI route detection
- [ ] Type hint resolution
- [ ] Cross-file import resolution
- [ ] Test file analysis (optional flag)

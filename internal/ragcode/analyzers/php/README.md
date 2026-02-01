# PHP Code Analyzer

Analizor de cod PHP pentru extragerea simbolurilor, structurii și relațiilor din fișiere PHP. Include suport complet pentru framework-ul Laravel. Indexează codul pentru căutare semantică în Qdrant.

## Status: ✅ PRODUCTION READY

---

## 🎯 Ce Face Acest Analizor?

Analizorul PHP parsează fișierele `.php` și extrage:
1. **Simboluri** - clase, metode, funcții, interfețe, traits, constante
2. **Relații** - moșteniri, implementări, relații Eloquent
3. **Metadate** - PHPDoc, vizibilitate, tipuri, Laravel-specific
4. **Framework** - Eloquent models, Controllers, Routes (Laravel)

---

## 📊 Fluxul de Date

```
┌─────────────────┐     ┌──────────────────┐     ┌─────────────────┐
│  Fișiere .php   │────▶│   PHP Analyzer   │────▶│   CodeChunks    │
│  (cod sursă)    │     │  (VKCOM parser)  │     │   (structurat)  │
└─────────────────┘     └──────────────────┘     └────────┬────────┘
                                                          │
                        ┌──────────────────┐              │
                        │ Laravel Analyzer │◀─────────────┤
                        │ (Eloquent, etc.) │              │
                        └──────────────────┘              ▼
                                                 ┌─────────────────┐
                                                 │     Qdrant      │
                                                 │  (vector store) │
                                                 └─────────────────┘
```

---

## 🔍 Ce Indexăm

### 1. Clase (`type: "class"`)

```php
<?php
namespace App\Models;

/**
 * Reprezintă un utilizator în sistem.
 */
class User extends Model implements Authenticatable
{
    use SoftDeletes, Notifiable;
    
    protected $fillable = ['name', 'email'];
    protected $casts = ['email_verified_at' => 'datetime'];
}
```

**Informații extrase:**
| Câmp | Valoare | Descriere |
|------|---------|-----------|
| `name` | `"User"` | Numele clasei |
| `namespace` | `"App\\Models"` | Namespace-ul |
| `full_name` | `"App\\Models\\User"` | Numele complet |
| `extends` | `"Model"` | Clasa părinte |
| `implements` | `["Authenticatable"]` | Interfețele implementate |
| `traits` | `["SoftDeletes", "Notifiable"]` | Traits folosite |
| `is_abstract` | `false` | Dacă e abstractă |
| `is_final` | `false` | Dacă e final |
| `docstring` | `"Reprezintă un utilizator..."` | PHPDoc |

### 2. Metode (`type: "method"`)

```php
/**
 * Returnează comenzile utilizatorului.
 * 
 * @param int $limit Numărul maxim de comenzi
 * @return Collection<Order>
 */
public function getOrders(int $limit = 10): Collection
{
    return $this->orders()->limit($limit)->get();
}
```

**Informații extrase:**
| Câmp | Valoare | Descriere |
|------|---------|-----------|
| `name` | `"getOrders"` | Numele metodei |
| `visibility` | `"public"` | Vizibilitate |
| `is_static` | `false` | Dacă e statică |
| `is_abstract` | `false` | Dacă e abstractă |
| `parameters` | `[{name: "limit", type: "int", default: "10"}]` | Parametri |
| `return_type` | `"Collection"` | Tipul returnat |
| `phpdoc.params` | `[{name: "limit", type: "int", desc: "..."}]` | PHPDoc params |
| `phpdoc.return` | `{type: "Collection<Order>", desc: ""}` | PHPDoc return |

### 3. Interfețe (`type: "interface"`)

```php
interface PaymentGateway extends Gateway
{
    public function charge(float $amount): bool;
    public function refund(string $transactionId): bool;
}
```

### 4. Traits (`type: "trait"`)

```php
trait Auditable
{
    public function getCreatedBy(): ?User { ... }
    public function logActivity(string $action): void { ... }
}
```

### 5. Funcții Globale (`type: "function"`)

```php
/**
 * Helper pentru formatare preț.
 */
function format_price(float $amount, string $currency = 'RON'): string
{
    return number_format($amount, 2) . ' ' . $currency;
}
```

---

## 🔗 Laravel Framework Support

### Eloquent Models

```php
class Order extends Model
{
    protected $fillable = ['user_id', 'total', 'status'];
    protected $casts = ['total' => 'decimal:2'];
    
    public function user(): BelongsTo
    {
        return $this->belongsTo(User::class);
    }
    
    public function items(): HasMany
    {
        return $this->hasMany(OrderItem::class);
    }
    
    public function scopeCompleted($query)
    {
        return $query->where('status', 'completed');
    }
    
    public function getTotalFormattedAttribute(): string
    {
        return number_format($this->total, 2) . ' RON';
    }
}
```

**Metadate Laravel extrase:**
```json
{
  "is_eloquent_model": true,
  "fillable": ["user_id", "total", "status"],
  "casts": {"total": "decimal:2"},
  "relations": [
    {"name": "user", "type": "belongsTo", "related": "User"},
    {"name": "items", "type": "hasMany", "related": "OrderItem"}
  ],
  "scopes": ["completed"],
  "accessors": ["total_formatted"]
}
```

### Controllers

```php
class OrderController extends Controller
{
    public function index(): View { ... }
    public function store(OrderRequest $request): RedirectResponse { ... }
    public function show(Order $order): View { ... }
}
```

**Metadate Controller:**
```json
{
  "is_controller": true,
  "is_resource_controller": true,
  "actions": ["index", "store", "show"],
  "http_methods": {
    "index": "GET",
    "store": "POST",
    "show": "GET"
  }
}
```

### Routes

```php
Route::get('/orders', [OrderController::class, 'index'])->name('orders.index');
Route::resource('users', UserController::class);
```

---

## 🏗️ Structura Fișierelor

```
php/
├── types.go              # Tipuri PHP: ClassInfo, MethodInfo, etc.
├── analyzer.go           # PathAnalyzer implementation (21KB)
├── api_analyzer.go       # APIAnalyzer pentru documentație
├── phpdoc.go             # Parser PHPDoc
├── analyzer_test.go      # 10 teste CodeAnalyzer
├── api_analyzer_test.go  # 4 teste APIAnalyzer
├── parser_test.go        # 5 teste parser
├── README.md             # Această documentație
└── laravel/              # Modul Laravel
    ├── types.go          # Tipuri Laravel-specific
    ├── analyzer.go       # Coordonator Laravel
    ├── adapter.go        # Adapter PathAnalyzer
    ├── eloquent.go       # Analizor Eloquent Models
    ├── controller.go     # Analizor Controllers
    ├── routes.go         # Analizor Routes
    └── README.md         # Documentație Laravel
```

---

## 💻 Utilizare

```go
import "github.com/doITmagic/rag-code-mcp/internal/ragcode/analyzers/php/laravel"

// Pentru proiecte Laravel (recomandat)
analyzer := laravel.NewAdapter()

// Analiză directoare/fișiere
chunks, err := analyzer.AnalyzePaths([]string{"./app"})

for _, chunk := range chunks {
    fmt.Printf("[%s] %s\n", chunk.Type, chunk.Name)
    if relations, ok := chunk.Metadata["relations"]; ok {
        fmt.Printf("  Relations: %v\n", relations)
    }
}
```

---

## 🔌 Integrare

### Language Manager

Analizorul PHP/Laravel este selectat automat pentru:
- `php` - proiecte PHP generice
- `laravel` - proiecte Laravel
- `php-laravel` - alternativă Laravel

### Detectare Workspace

| Fișier/Director | Tip Proiect |
|-----------------|-------------|
| `artisan` | Laravel |
| `composer.json` | PHP |
| `app/Models/` | Laravel |
| `routes/web.php` | Laravel |

---

## 📋 Tipuri de CodeChunk

| Type | Descriere | Exemplu |
|------|-----------|---------|
| `class` | Clasă PHP | `class User extends Model` |
| `method` | Metodă de clasă | `public function save()` |
| `function` | Funcție globală | `function helper()` |
| `interface` | Interfață | `interface Payable` |
| `trait` | Trait | `trait Auditable` |
| `const` | Constantă de clasă | `const STATUS_ACTIVE = 1` |
| `property` | Proprietate | `protected $fillable` |

---

## 🏷️ Metadate Complete

### Class Metadata
```json
{
  "namespace": "App\\Models",
  "extends": "Model",
  "implements": ["Authenticatable"],
  "traits": ["SoftDeletes"],
  "is_abstract": false,
  "is_final": false,
  "is_eloquent_model": true,
  "fillable": ["name", "email"],
  "relations": [...]
}
```

### Method Metadata
```json
{
  "class_name": "UserController",
  "visibility": "public",
  "is_static": false,
  "is_abstract": false,
  "is_final": false,
  "parameters": [...],
  "return_type": "View",
  "phpdoc": {
    "description": "...",
    "params": [...],
    "return": {...}
  }
}
```

---

## 🧪 Testare

```bash
# Toate testele PHP (19 teste)
go test ./internal/ragcode/analyzers/php/...

# Doar Laravel (21 teste)
go test ./internal/ragcode/analyzers/php/laravel/...

# Cu coverage
go test -cover ./internal/ragcode/analyzers/php/...
```

**Rezultate:**
- ✅ 19/19 teste PHP PASS
- ✅ 21/21 teste Laravel PASS
- ✅ Coverage: 83.6%

---

## 📦 Dependențe

- **VKCOM/php-parser** v0.8.2 - Parser PHP cu suport PHP 8.0-8.2

---

## ⚠️ Limitări

| Limitare | Descriere |
|----------|-----------|
| **No Runtime** | Analiză statică, nu execută codul |
| **Single-file** | Fiecare fișier e analizat independent |
| **No Autoload** | Nu rezolvă autoload-ul Composer |

---

## 🔮 Îmbunătățiri Viitoare

- [ ] Route groups cu middleware
- [ ] Migration analyzer
- [ ] Symfony framework support
- [ ] WordPress support
- [ ] Cross-file type resolution

---

## Status Anterior (pentru referință)

### Laravel Analyzer - FULLY IMPLEMENTED

**Test Results:**
- ✅ **21/21 Laravel tests PASS**
- ✅ **Real project tested**: Barou Laravel app (38 models, 116 relations, 813 chunks)
- ✅ **E2E integration**: Full indexing pipeline working
- ✅ **Language manager**: Auto-detection and routing complete

### Implemented Features

#### PAS 8: Laravel-Specific Features ✅
1. **Eloquent Models** (COMPLETE):
   - ✅ Model detection (`extends Model`)
   - ✅ Property extraction: `$fillable`, `$guarded`, `$casts`, `$table`, `$primaryKey`
   - ✅ **All Relations**: `hasOne`, `hasMany`, `belongsTo`, `belongsToMany`, `hasManyThrough`, `morphTo`, `morphMany`, `morphToMany`, `morphedByMany`
   - ✅ Foreign key & local key extraction
   - ✅ Fully-qualified name resolution with imports
   - ✅ **Scopes**: `scopeMethodName()` detection
   - ✅ **Accessors/Mutators**: `getXxxAttribute()`, `setXxxAttribute()`
   - ✅ SoftDeletes trait detection

2. **Controllers** (COMPLETE):
   - ✅ Controller detection (`extends Controller`)
   - ✅ Resource controller identification (7 RESTful actions)
   - ✅ API controller detection
   - ✅ HTTP method inference from action names
   - ✅ Parameter extraction

3. **Routes** (COMPLETE):
   - ✅ Route file parsing (`routes/web.php`, `routes/api.php`)
   - ✅ `Route::get()`, `Route::post()`, etc.
   - ✅ `Route::match()` support
   - ✅ `Route::resource()` expansion
   - ✅ Controller@action binding
   - ✅ Array syntax `[Controller::class, 'action']`

#### PAS 9: End-to-End Testing ✅
- ✅ Complete Laravel project E2E tests
- ✅ Relationship resolution tests
- ✅ Scopes and accessors tests
- ✅ **Real Barou project**: 38 models, 116 relations analyzed successfully

#### PAS 10: Integration ✅
- ✅ Laravel Adapter implementing `PathAnalyzer` interface
- ✅ Language manager integration
- ✅ Auto-detection for `php`, `laravel`, `php-laravel` project types
- ✅ Chunk enrichment with Laravel metadata
- ✅ Full indexing pipeline tested

## Next Steps

**READY FOR PRODUCTION** - Laravel analyzer is complete and tested with real projects.

Optional enhancements for future:
1. Route groups with middleware
2. Migration analysis
3. Symfony framework support

### Implemented Files (8 files)

- ✅ `types.go` (6.1KB) - PHP-specific internal types with API support
- ✅ `analyzer.go` (21KB) - PathAnalyzer implementation with PHPDoc extraction
- ✅ `api_analyzer.go` (7.4KB) - APIAnalyzer implementation
- ✅ `phpdoc.go` (5.3KB) - PHPDoc parser helper
- ✅ `analyzer_test.go` (9.9KB, 10 tests) - CodeAnalyzer tests
- ✅ `api_analyzer_test.go` (7.4KB, 4 tests) - APIAnalyzer tests
- ✅ `parser_test.go` (4.4KB, 5 tests) - Parser API validation tests
- ✅ **Integration**: language_manager.go, workspace detector

### Test Results

```text
19/19 TESTS PASS ✅
Coverage: 83.6% of statements
Integration: COMPLETE ✅
```

**Test Categories:**
- **CodeAnalyzer** (10 tests):
  - Basic class extraction
  - Global functions (2 tests)
  - Multiple classes (2 tests)
  - Properties (1 test)
  - Constants (1 test)
  - Interfaces (1 test)
  - Traits (1 test)
  - Complete class (1 test)
- **APIAnalyzer** (4 tests):
  - Full API path analysis with PHPDoc
  - Global functions with documentation
  - Interface documentation
  - Trait documentation
- **Parser API** (5 tests):
  - Basic parsing, class/function/method/namespace extraction
- **Integration** (6 tests):
  - Language manager recognizes PHP/Laravel
  - Workspace detector identifies composer.json/artisan
  - Collection naming: ragcode-{workspaceID}-php

## Dependencies

- **VKCOM/php-parser** v0.8.2 (fork with PHP 8.0-8.2 support)

## Features

### ✅ Implemented (PAS 1-7)

1. **Namespace Support**
   - Multi-namespace per-file
   - Fully qualified names

2. **Class Analysis**
   - Class declarations with extends/implements
   - Method extraction (visibility, static, abstract, final)
   - Property extraction (visibility, static, readonly, typed)
   - Class constants (visibility, value extraction)
   - Parameter and return type support
   - **PHPDoc extraction** (description, @param, @return)

3. **Interface Support**
   - Interface declarations
   - Method signatures
   - Multiple interface extends
   - **PHPDoc documentation**

4. **Trait Support**
   - Trait declarations
   - Methods and properties
   - **PHPDoc documentation**

5. **Function Analysis**
   - Global functions
   - Namespaced functions
   - Parameters and return types
   - **PHPDoc documentation**

6. **PHPDoc Parsing** (PAS 6)
   - Description extraction
   - @param tags (type, name, description)
   - @return tags (type, description)
   - @throws, @var, @deprecated, @see, @example tags
   - Type hint merging with PHPDoc

7. **API Documentation** (PAS 6)
   - APIAnalyzer implementation
   - Conversion to APIChunk format
   - Full documentation export
   - Multi-file API analysis

8. **Multi-file Support**
   - `AnalyzePaths()` for batch processing
   - `AnalyzeAPIPaths()` for API documentation

9. **Error Handling**
   - Parser error recovery
   - Graceful degradation

10. **Integration** (PAS 7)
    - language_manager.go integration ✅
    - Multi-language collection support ✅
    - Workspace detection (composer.json, artisan) ✅
    - Project type normalization (php, laravel) ✅

### ✅ Completed (PAS 8: Laravel-Specific Features)

11. **Laravel Framework Support**
    - **Eloquent Models Detection**:
      - Extends `Illuminate\Database\Eloquent\Model`
      - `$fillable` array extraction
      - `$guarded` array extraction
      - `$casts` array extraction
      - `$table` property
      - Relations: `hasOne()`, `hasMany()`, `belongsTo()`, `belongsToMany()`
      - Scopes: `scopeMethodName()` detection
    - **Controller Detection**:
      - Extends `Illuminate\Routing\Controller`
      - Resource controllers
      - API controllers
      - Request validation
    - **Directory Structure Awareness**:
      - `app/Models/` - Eloquent models
      - `app/Http/Controllers/` - Controllers
      - `routes/` - Route definitions
      - `database/migrations/` - Migrations

### ❌ TODO (PAS 8-9)

All PAS 8–9 milestones for the Laravel analyzer are now **complete** (see the **Status** section above).

Remaining future enhancements (post-PAS 10):
- Route groups with middleware
- Migration analyzer
- Symfony framework support

## Code Metrics

- **Total Lines**: ~1,800
- **Core Implementation**: 
  - `analyzer.go` (21KB, 814 lines)
  - `api_analyzer.go` (7.4KB, 293 lines)
  - `phpdoc.go` (5.3KB, 217 lines)
- **Helper Functions**: 25+
- **Test Coverage**: 83.6%
- **Tests**: 19 (5 parser + 10 analyzer + 4 API)
- **Integration Tests**: 6 (language manager + workspace detector)

## Laravel Support

The analyzer automatically detects Laravel projects through:
- `artisan` file (Laravel CLI tool)
- `composer.json` (PHP dependency manager)
- `app/Models/` directory structure

Project types recognized:
- `laravel` - Laravel framework project
- `php-laravel` - Alternative Laravel naming
- `php` - Generic PHP project

All Laravel projects use the PHP analyzer with future Laravel-specific enhancements.

## Architecture

Follows the same pattern as `golang` analyzer with modular framework support:

```
php/
├── types.go              - Internal type definitions
├── analyzer.go           - PathAnalyzer implementation
├── api_analyzer.go       - APIAnalyzer implementation
├── phpdoc.go             - PHPDoc parser (PHP-specific helper)
├── analyzer_test.go      - CodeAnalyzer tests
├── api_analyzer_test.go  - APIAnalyzer tests
├── parser_test.go        - Parser validation tests
└── laravel/              - Laravel framework module (separate package)
    ├── types.go          - Laravel-specific types (Eloquent, Controllers, Routes)
    ├── analyzer.go       - Laravel framework analyzer coordinator
    ├── eloquent.go       - Eloquent Model analyzer
    ├── controller.go     - Controller analyzer
    └── README.md         - Laravel module documentation
```

### Framework Modules

Framework-specific analyzers are separated into their own packages:
- **laravel/** - Laravel framework support (Eloquent, Controllers, Routes)
- **symfony/** - Symfony framework support (planned)
- **wordpress/** - WordPress support (planned)

This modular design allows:
- Clean separation of concerns
- Independent testing of framework features
- Easy addition of new frameworks
- Reusable base PHP analyzer

## Next Steps

1. **PAS 8**: Laravel-specific features (Eloquent models, Controllers)
2. **PAS 9**: End-to-end testing with Laravel projects
3. **PAS 10**: Production testing with Symfony projects

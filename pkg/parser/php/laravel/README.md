# Laravel & Blade Framework Analyzer (`pkg/parser/php/laravel`)

Framework-aware analyzer and `php.FrameworkEnricher` for **Laravel** applications and **Blade** templates.

## 🏗️ Architecture

```
laravel/
├── adapter.go           - Registers Laravel as a php.FrameworkEnricher
├── analyzer.go          - Main Laravel analyzer coordinator
├── types.go             - Laravel-specific symbol & metadata types
├── eloquent.go          - Eloquent Model & relationship analyzer
├── controller.go        - HTTP & API Controller analyzer
├── routes.go            - Route parser (Route::get/post/resource/apiResource/group)
├── migrations.go        - Database migration & schema analyzer
├── middleware.go        - HTTP Middleware analyzer
├── service_provider.go  - ServiceProvider (register/boot/bindings) analyzer
└── blade.go             - Blade template (.blade.php) analyzer
```

## ✨ Implemented Features

1. **Eloquent Models (`eloquent.go`)**
   - Detects classes extending `Illuminate\Database\Eloquent\Model` or `Illuminate\Foundation\Auth\User` (resolved via `use` imports).
   - Extracts `$table`, `$primaryKey`, `$fillable`, `$guarded`, `$casts`, `$hidden`, `$appends`, `$with`.
   - Extracts Eloquent relationships (`hasOne`, `hasMany`, `belongsTo`, `belongsToMany`, `hasManyThrough`, `hasOneThrough`, `morphTo`, `morphMany`, `morphOne`, `morphToMany`, `morphedByMany`) with FQN target resolution (including group `use` imports).
   - Extracts local query scopes (`scopeXxx`), accessors, and mutators.

2. **Controllers (`controller.go`)**
   - Detects controllers in `App\Http\Controllers` or extending `Illuminate\Routing\Controller`.
   - Distinguishes web vs. API controllers (`/Api/` path normalized across Windows/Linux).
   - Extracts actions, HTTP method inference, request validation, and controller middleware.

3. **Routes (`routes.go`)**
   - Parses `routes/*.php` (`web.php`, `api.php`, etc.).
   - Extracts `Route::get`, `post`, `put`, `patch`, `delete`, `options`, `any`, `match`.
   - Expands `Route::resource` and `Route::apiResource` into their standard REST actions.
   - Supports both `Route::group([...], fn)` and fluent chains (`Route::middleware(...)->prefix(...)->name(...)->group(fn)`).

4. **Database Migrations (`migrations.go`)**
   - Extracts `Schema::create`, `Schema::table`, `Schema::drop`, `Schema::dropIfExists`, column definitions, and foreign keys.

5. **Middleware & Service Providers (`middleware.go`, `service_provider.go`)**
   - Extracts `handle()` middleware logic and `ServiceProvider` container bindings (`bind`, `singleton`, `scoped`) and boot registrations.

6. **Blade Templates (`blade.go`)**
   - Parses `.blade.php` views for `@extends`, `@section`, `@yield`, `@include`, `@component`, `@props`, and Livewire/Alpine references.

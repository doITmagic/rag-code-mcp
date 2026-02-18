---
name: php-laravel
description: PHP and Laravel specific patterns, Service Providers, and Eloquent practices
---

# 🐘 Skill: PHP & Laravel Patterns

Guidelines for working with PHP projects, specifically those using the Laravel framework.

---

## 🔗 External References (Source of Truth)
- [Laravel Official Documentation](https://laravel.com/docs)
- [PHP-FIG PEPs (Standards)](https://www.php-fig.org/psr/)
- [Spatie's Guidelines](https://guidelines.spatie.be/)

---

## 🏰 Laravel Architecture

### 1. Service Providers
- All core logic should be registered in Service Providers.
- Check `app/Providers` for dependency injection setup.

### 2. Eloquent Models
- Models belong in `app/Models`.
- Use Scopes for reusable query logic.
- Always check for Relationships (hasMany, belongsTo).

### 3. Controllers & Requests
- Keep controllers thin. Move validation to `FormRequest` classes.
- Move complex logic to `Services` or `Actions`.

### 4. Testing
- **Pest OR PHPUnit**: Use Pest for a modern API or PHPUnit for classic suites.
- **Feature Tests**: Test behavior from the user's perspective (Routes + DB).
- **Unit Tests**: For pure logic outside the Laravel container.
- **RefreshDatabase**: Use traits to keep tests isolated.

---

## 🔧 Tool Usage:
- `mcp_ragcode_hybrid_search`: Great for finding specific Eloquent model names or Route definitions.
- `mcp_ragcode_list_package_exports`: Use for listing methods in a Laravel Class.

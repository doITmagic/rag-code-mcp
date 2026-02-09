---
name: go-best-practices
description: Go development patterns, project structure, and idiomatic practices
---

# 🐹 Skill: Go Best Practices

This skill guides the AI in writing high-quality, idiomatic Go code and understanding Go project structures.

---

## 🔗 External References (Source of Truth)
- [Google Go Style Guide](https://google.github.io/styleguide/go/)
- [Effective Go](https://go.dev/doc/effective_go)
- [Uber Go Style Guide](https://github.com/uber-go/guide/blob/master/style.md)

---

## 🏗️ Project Structure
Most Go projects in this ecosystem follow the Standard Go Project Layout:
- `/cmd`: Main applications/binaries.
- `/internal`: Private packages (not importable by other projects).
- `/pkg`: Public packages (rarely used here, prefer `internal`).
- `/api`: API definitions.

## ✅ Implementation Patterns

### 1. Error Handling
- **Check immediately**: Never ignore errors.
- **Wrap errors**: Use `fmt.Errorf("context: %w", err)` to provide trace details.
- **Sentinel Errors**: Define custom errors in the package for specific conditions.

### 2. Interfaces
- **Small is better**: Prefer `io.Reader` over large monolithic interfaces.
- **Consumer defines**: Define interfaces where they are USED, not where they are implemented.

### 3. Concurrency
- **Use Channels**: For communication/orchestration.
- **Mutex**: For simple state protection.
- **Context**: Always propagate `context.Context` for cancellation and timeouts.

### 4. Testing
- **Table-Driven Tests**: Use anonymous structs and `t.Run` for multiple test cases.
- **Package name**: Use `package_test` for external testing (test only the public API).
- **Mocking**: Use interfaces to mock dependencies. Avoid complex mocking frameworks if simple stubs work.
- **Helpers**: Use `t.Helper()` for repetitive assertions.

---

## 🔧 Tools for Go Project Analysis:
- `mcp_ragcode_find_type_definition`: Essential for understanding structs and interfaces.
- `mcp_ragcode_list_package_exports`: Quick overview of a package's public API.

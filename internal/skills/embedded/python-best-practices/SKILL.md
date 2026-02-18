---
name: python-best-practices
description: Python patterns, type hinting, and project standards
---

# 🐍 Skill: Python Best Practices

This skill provides directions for writing robust and maintainable Python code.

---

## 🔗 External References (Source of Truth)
- [PEP 8 - Style Guide for Python](https://peps.python.org/pep-0008/)
- [Google Python Style Guide](https://google.github.io/styleguide/pyguide.html)
- [Real Python Best Practices](https://realpython.com/python-best-practices/)

---

## 💎 Clean Code Patterns

### 1. Type Hinting
- Always use `typing` module for function signatures.
- Prefer `list[str]` (Python 3.9+) or `List[str]`.

### 2. Async usage
- Use `asyncio` for I/O bound tasks.
- Ensure `await` is used correctly with async functions.

### 3. Docstrings
- Use Google or NumPy style docstrings for classes and public methods.

### 4. Testing
- **pytest**: Use `pytest` for its powerful fixture system.
- **Hypothesis**: Use for property-based testing if needed.
- **Mocking**: Use `unittest.mock` to isolate components.
- **Coverage**: Aim for high coverage in business logic.

## 📦 Dependency Management
- Look for `pyproject.toml`, `requirements.txt`, or `setup.py`.

---

## 🔧 Search Tips for Python:
- Use `mcp_ragcode_search_code` with queries like "Python class definition" or "decorator implementation".

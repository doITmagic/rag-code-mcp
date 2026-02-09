---
name: ragcode-priority
description: Prioritize ragcode MCP tools for all code searches in this project
---

# 🎯 Skill: Ragcode MCP Priority Usage

This skill defines **MANDATORY** rules for searching code in the **rag-code-mcp** project.

---

## ✅ MANDATORY RULES

### 1. Search Hierarchy

When searching code, ALWAYS follow this order:

| Priority | Tool | When to Use |
|----------|------|-------------|
| 🥇 **First** | `mcp_ragcode_search_code` | Exploration, understanding concepts, unfamiliar code |
| 🥈 **Second** | `mcp_ragcode_hybrid_search` | Exact match: function names, errors, string literals |
| 🥉 **Third** | `mcp_ragcode_get_function_details` | Complete code of a known function |
| 4️⃣ | `mcp_ragcode_find_type_definition` | Complete struct/interface definition |
| 5️⃣ | `mcp_ragcode_find_implementations` | Where a function is called/implemented |
| 6️⃣ | `mcp_ragcode_list_package_exports` | What a package exports |

### 2. "Search First" Rule

**ALWAYS** start with a ragcode search before assuming code structure!

```
❌ WRONG: "I know it's in internal/ragcode, I'll open the file directly"
✅ CORRECT: search_code → find exact location → open file
```

### 3. Standard Workflow

```
Step 1: mcp_ragcode_search_code (understand context)
           ↓
Step 2: mcp_ragcode_get_function_details OR find_type_definition (details)
           ↓
Step 3: view_file (only for specific lines if needed)
```

---

## ⛔ FORBIDDEN

### Do NOT use these tools for code search:

| Tool | Why Forbidden | Exception |
|------|---------------|-----------|
| `grep_search` | No semantic context | ONLY for JavaScript/TypeScript (not indexed yet) |
| `find_by_name` | For files, not content | OK for finding files, not code |
| `view_file` randomly | Waste of time without context | ONLY after ragcode search |

### Forbidden behaviors:

1. ❌ **DO NOT assume** code structure without searching
2. ❌ **DO NOT make multiple grep searches** when one ragcode search suffices
3. ❌ **DO NOT navigate manually** through directories to find code
4. ❌ **DO NOT open random files** hoping to find what you need

---

## 🌐 Language Support

### ✅ Full support (use ONLY ragcode):
- **Go** - complete analyzer, all tools work
- **Python** - complete analyzer
- **PHP/Laravel** - complete analyzer with Laravel support
- **HTML** - basic analyzer

### ⚠️ Limited support (fallback allowed):
- **JavaScript/TypeScript** - indexing in development
  - Try `search_code` FIRST
  - If not found, use `grep_search` as backup

---

## 🔧 Recommended Parameters

### search_code
```
query: <semantic description of what you're looking for>
file_path: /home/razvan/go/src/github.com/doITmagic/rag-code-mcp  # optional, for context
limit: 5  # sufficient for most searches
```

### hybrid_search
```
query: <exact term to search>
limit: 5
```

### get_function_details
```
function_name: <exact function name>
package: <optional package for disambiguation>
```

---

## 📖 Why ragcode?

| Aspect | grep_search | ragcode |
|--------|-------------|---------|
| Understands context | ❌ | ✅ |
| Semantic search | ❌ | ✅ |
| Finds similar functions | ❌ | ✅ |
| Returns complete code | ❌ | ✅ |
| Multi-language support | Limited | ✅ |

**Concrete example:**
- Search "user authentication" with grep: finds only literal string "authentication"
- Search with ragcode: finds login functions, token validation, auth middleware

---

## ⚡ Quick Reference

```
🔍 Exploration         → search_code
🎯 Exact match         → hybrid_search  
📝 Function code       → get_function_details
🏗️ Type definition     → find_type_definition
🔗 Usages              → find_implementations
📦 Package contents    → list_package_exports
📄 Specific context    → get_code_context (with file + lines)
```

---

## 📚 Examples

See [examples/search_patterns.md](examples/search_patterns.md) for project-specific examples.

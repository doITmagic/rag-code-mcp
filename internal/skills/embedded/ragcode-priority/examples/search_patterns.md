# 📚 Ragcode Search Patterns for rag-code-mcp

This file contains practical examples for searching the rag-code-mcp codebase.

---

## 🔍 Finding Analyzers

### Find all language analyzers
```
mcp_ragcode_search_code 
  query: "language analyzer implementation CodeAnalyzer"
  limit: 10
```

### Find specific language analyzer
```
# Go analyzer
mcp_ragcode_search_code query: "Go AST parsing analyzer"

# Python analyzer
mcp_ragcode_search_code query: "Python code analyzer module parsing"

# PHP/Laravel analyzer
mcp_ragcode_search_code query: "PHP Laravel adapter analyzer"
```

### Get analyzer implementation details
```
mcp_ragcode_get_function_details 
  function_name: "NewCodeAnalyzer"
  package: "golang"
```

---

## 🏗️ Understanding Types

### Find Symbol structure
```
mcp_ragcode_find_type_definition type_name: "Symbol"
```

### Find PathAnalyzer interface
```
mcp_ragcode_find_type_definition type_name: "PathAnalyzer"
```

### Find all types in a package
```
mcp_ragcode_list_package_exports 
  package: "codetypes"
  symbol_type: "type"
```

---

## 🔗 Finding Usages

### Where is IndexWorkspace called?
```
mcp_ragcode_find_implementations symbol_name: "IndexWorkspace"
```

### Who implements PathAnalyzer?
```
mcp_ragcode_find_implementations symbol_name: "PathAnalyzer"
```

### Where is Symbol used?
```
mcp_ragcode_find_implementations symbol_name: "Symbol"
```

---

## 🎯 Exact Matches

### Find specific function by name
```
mcp_ragcode_hybrid_search query: "func IndexWorkspace"
```

### Find error handling
```
mcp_ragcode_hybrid_search query: "return nil, fmt.Errorf"
```

### Find specific constant or variable
```
mcp_ragcode_hybrid_search query: "LanguageGo"
```

---

## 📦 Exploring Packages

### What does the ragcode package export?
```
mcp_ragcode_list_package_exports package: "ragcode"
```

### What functions are in workspace package?
```
mcp_ragcode_list_package_exports 
  package: "workspace"
  symbol_type: "function"
```

### What types are in codetypes package?
```
mcp_ragcode_list_package_exports 
  package: "codetypes"
  symbol_type: "type"
```

---

## 🔄 Common Workflows

### Workflow 1: Understanding a feature
```
# Step 1: Semantic search for the concept
mcp_ragcode_search_code query: "language detection workspace"

# Step 2: Get the main function details
mcp_ragcode_get_function_details function_name: "DetectLanguages"

# Step 3: Find where it's used
mcp_ragcode_find_implementations symbol_name: "DetectLanguages"
```

### Workflow 2: Debugging an issue
```
# Step 1: Search for related code
mcp_ragcode_search_code query: "indexing error handling workspace"

# Step 2: Find the exact error message
mcp_ragcode_hybrid_search query: "workspace not indexed"

# Step 3: Get context around the error
mcp_ragcode_get_code_context 
  file_path: "<file from step 2>"
  start_line: <line - 5>
  end_line: <line + 10>
```

### Workflow 3: Adding a new feature
```
# Step 1: Find similar implementations
mcp_ragcode_search_code query: "analyzer implementation for language"

# Step 2: Understand the interface
mcp_ragcode_find_type_definition type_name: "PathAnalyzer"

# Step 3: See existing implementations
mcp_ragcode_find_implementations symbol_name: "PathAnalyzer"
```

---

## ⚠️ JavaScript/TypeScript Fallback

When searching JS/TS code (not yet fully indexed):

```
# Try ragcode first
mcp_ragcode_search_code query: "javascript parser"

# If no results, fallback to grep
grep_search 
  SearchPath: "/home/razvan/go/src/github.com/doITmagic/rag-code-mcp"
  Query: "javascript"
  Includes: ["*.go"]
```

---

## 📝 Tips

1. **Be semantic**: Use descriptive queries like "authentication middleware" not just "auth"
2. **Start broad**: Begin with `search_code`, narrow down with specific tools
3. **Use packages**: When function names are ambiguous, specify the package
4. **Check implementations**: Always use `find_implementations` to understand impact before refactoring

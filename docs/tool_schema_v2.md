# Tool Output & Indexing Schema (v2)

This document describes the **canonical v2** data model used by the code MCP server.
The goal is that ANY other developer can quickly understand:

- what is indexed (semantically, into the vector DB),
- what is returned to the AI (structured JSON),
- which tool produces which type of result.

---

## 1. Indexing / semantic search – `CodeChunk`

Indexing into the vector DB is done **exclusively** with the `CodeChunk` structure
(see `internal/codetypes/types.go`).

```go
// CodeChunk is the canonical v2 format for indexing/search. It represents a
// semantically meaningful piece of code (usually a function, method, type or
// interface declaration) that is stored in vector search.
type CodeChunk struct {
    // Symbol metadata
    Type     string // function | method | type | interface | file
    Name     string // Symbol name (or file base name for Type=file)
    Package  string // Package/module name
    Language string // go | php | python | typescript etc

    // Source location
    FilePath  string
    URI       string
    StartLine int
    EndLine   int

    // Selection range (for precise navigation to symbol name)
    SelectionStartLine int
    SelectionEndLine   int

    // Content
    Signature string
    Docstring string
    Code      string

    // Extra metadata
    Metadata map[string]any
}
```

**Principles:**

- `CodeChunk` is the **only** format written to the vector DB for code.
- `Signature` + `Docstring` + (sometimes) `Code` is the text that is embedded.
- `Metadata` can contain language-specific information, for example:
  - Go: `type_info` (serialized as JSON from `golang.TypeInfo`),
  - PHP/Laravel: model info (`eloquent_model`), populated by the Laravel analyzer
    for Eloquent models (table, fillable, relationships, scopes, attributes, etc.).
- Tools never read raw `Metadata` directly. They only access it through a layer
  that builds the *descriptors* defined below.

---

## 2. Output for the AI – descriptor schema

All tools that support `output_format: "json"` must serialize **one of the
structures** defined in `internal/codetypes/symbol_schema.go`.

### 2.1. `SymbolLocation`

```go
type SymbolLocation struct {
    FilePath  string `json:"file_path,omitempty"`
    URI       string `json:"uri,omitempty"`
    StartLine int    `json:"start_line,omitempty"`
    EndLine   int    `json:"end_line,omitempty"`
}
```

Used everywhere as `location` for precise navigation.

### 2.2. `ClassDescriptor` – type/class/model

```go
type ClassDescriptor struct {
    Language string `json:"language"`
    Kind     string `json:"kind"` // class | interface | trait | struct | type | model
    Name     string `json:"name"`

    Namespace string `json:"namespace,omitempty"`
    Package   string `json:"package,omitempty"`
    FullName  string `json:"full_name,omitempty"`

    Signature   string         `json:"signature,omitempty"`
    Description string         `json:"description,omitempty"`
    Location    SymbolLocation `json:"location,omitempty"`

    Fields    []FieldDescriptor    `json:"fields,omitempty"`
    Methods   []FunctionDescriptor `json:"methods,omitempty"`
    Relations []RelationDescriptor `json:"relations,omitempty"`

    // Data-model specific (ORM / Eloquent)
    Table      string            `json:"table,omitempty"`
    Fillable   []string          `json:"fillable,omitempty"`
    Hidden     []string          `json:"hidden,omitempty"`
    Visible    []string          `json:"visible,omitempty"`
    Appends    []string          `json:"appends,omitempty"`
    Casts      map[string]string `json:"casts,omitempty"`
    Scopes     []string          `json:"scopes,omitempty"`
    Attributes []string          `json:"attributes,omitempty"`

    Tags     []string       `json:"tags,omitempty"`
    Metadata map[string]any `json:"metadata,omitempty"`
}
```

**Used by:**

- `rag_find_type_definition` with `output_format: "json"`.
  - PHP: classes and Laravel models (User, Lawyer, etc.).
  - Go: types (struct/interface), enriched with `Fields` and `Methods` from
    `TypeInfo` when available.

### 2.3. `FunctionDescriptor` – function/method

```go
type FunctionDescriptor struct {
    Language    string         `json:"language"`
    Kind        string         `json:"kind"` // function | method | scope | accessor | mutator | constructor
    Name        string         `json:"name"`
    Namespace   string         `json:"namespace,omitempty"`
    Receiver    string         `json:"receiver,omitempty"`
    Signature   string         `json:"signature,omitempty"`
    Description string         `json:"description,omitempty"`
    Location    SymbolLocation `json:"location,omitempty"`

    Parameters []ParamDescriptor  `json:"parameters,omitempty"`
    Returns    []ReturnDescriptor `json:"returns,omitempty"`

    Visibility string `json:"visibility,omitempty"`
    IsStatic   bool   `json:"is_static,omitempty"`
    IsAbstract bool   `json:"is_abstract,omitempty"`
    IsFinal    bool   `json:"is_final,omitempty"`

    Code     string         `json:"code,omitempty"`
    Tags     []string       `json:"tags,omitempty"`
    Metadata map[string]any `json:"metadata,omitempty"`
}
```

**Used by:**

- `rag_get_function_details` with `output_format: "json"`.
  - PHP: functions and methods, including:
    - `visibility`, `is_static`, `is_abstract`, `is_final`,
    - `parameters` (with types from PHPDoc / type-hints),
    - `returns` (including Eloquent types, e.g. `BelongsToMany<App\\Role>`),
    - Laravel-specific classification (`kind: "scope"`, `"accessor"`,
      `"mutator"` for Eloquent special methods).
  - Go: functions/methods in v2 minimal form (signature, description, code,
    location), extensible later with parameters/returns parsed from the AST.

### 2.4. `SymbolDescriptor` – summary for listings/search

```go
type SymbolDescriptor struct {
    Language  string `json:"language"`
    Kind      string `json:"kind"` // class | interface | trait | function | method | constant | enum | file
    Name      string `json:"name"`
    Namespace string `json:"namespace,omitempty"`
    Package   string `json:"package,omitempty"`

    Signature   string         `json:"signature,omitempty"`
    Description string         `json:"description,omitempty"`
    Location    SymbolLocation `json:"location,omitempty"`

    Tags     []string       `json:"tags,omitempty"`
    Metadata map[string]any `json:"metadata,omitempty"`
}
```

**Used by:**

- `rag_list_package_exports` with `output_format: "json"` (Go + PHP).
- Search-oriented tools (planned) to return compact hits.

---

## 3. Mapping: tool → input → output

### 3.1. `rag_find_type_definition`

- **Standard input:**
  - `type_name` (required),
  - `package` / `namespace` (optional but recommended),
  - `output_format`: `"markdown"` (default) or `"json"`.
- **Output:**
  - `markdown` – human-friendly view, optimized for reading in a terminal.
  - `json` – a `ClassDescriptor` instance.

### 3.2. `rag_get_function_details`

- **Standard input:**
  - `function_name` (required),
  - `package` / `namespace`,
  - `class_name` for PHP methods (implicitly derived from the chunk when
    possible),
  - `output_format`.
- **Output:**
  - `markdown` – human-friendly view.
  - `json` – a `FunctionDescriptor` instance.

### 3.3. `rag_list_package_exports`

- **Standard input:**
  - `package` / `namespace` (required),
  - `symbol_type` (filter; optional),
  - `output_format`.
- **Output:**
  - `markdown` – structured list grouped by kind (function/type/class/etc.).
  - `json` – `[]SymbolDescriptor`.

### 3.4. `rag_evaluate`

- **Standard input:**
  - `file_path` (optional; used for workspace detection).
- **Functionality:**
  - This tool generates a special prompt for the AI agent, asking it to evaluate its performance, benefits, and pain points during the current session.
  - It provides technical context (workspace, languages, system status) to help the AI give a precise answer.
- **Goal:**
  - Continuous improvement of the RagCode MCP by gathering qualitative feedback directly from the agents using it.

### 3.5. `rag_search_code`

- **Standard input:**
  - `query` (required): Semantic search query.
  - `file_path` (required): Current file path for workspace detection.
  - `limit` (optional): Maximum results (default 10).
- **Output:**
  - `[]SymbolDescriptor` with code snippets and metadata.

### 3.6. `rag_hybrid_search`

- **Standard input:**
  - `query` (required): Keyword + semantic query.
  - `file_path` (required): Current file path.
- **Output:**
  - `[]SymbolDescriptor` with exact/semantic matches.

### 3.7. `rag_find_implementations`

- **Standard input:**
  - `symbol_name` (required): The function/method/interface to find usages of.
  - `file_path` (required): Current file path.
- **Output:**
  - `[]SymbolDescriptor` representing callers and implementations.

### 3.8. `rag_search_docs`

- **Standard input:**
  - `query` (required): Search query for Markdown documentation.
  - `file_path` (required): Current file path.
- **Output:**
  - Snippets from `.md` files in the workspace.

### 3.9. `rag_get_code_context`

- **Standard input:**
  - `file_path` (required): Target file path.
  - `start_line` (required).
  - `end_line` (required).
  - `context_lines` (optional): Default 5.
- **Output:**
  - Exact lines from the file with surrounding context.

### 3.10. `rag_index_workspace`

- **Standard input:**
  - `file_path` (required): Root detection starting point.
  - `recreate` (optional): Force full re-index.
- **Functionality:**
  - Manually triggers/restarts the indexing process for the detected workspace.

> **Developer Note:** If you are an AI assistant using this tool, please take a moment to provide honest, detailed feedback. Your experience helps us refine semantic search, tool naming, and the overall developer experience.

#### Example AI Response:
```markdown
# 📝 RagCode MCP Evaluation - Current Session

### 1. 🚀 BENEFITS & PERFORMANCE
- **Speed:** Sub-second searches vs. minutes of manual file browsing.
- **Precise Context:** `get_function_details` provides the full function with signature, not just a snippet.
- **Semantic Discovery:** Found relevant code by searching for "report mandate" without knowing the exact file or function names.
- **No Manual Grepping:** Eliminated the need for complex `grep` commands.

### 2. ⚡ PAIN POINTS & DIFFICULTIES
- **Mandatory Parameters:** Initially forgetting `file_path` led to errors (auto-detection has improved this).
- **Context Limits:** When complex flows span multiple files, manual reading is still sometimes required.
- **Search Refinement:** Occasionally had to switch from `rag_search_code` to `rag_hybrid_search` when semantic matches were too broad.

### 3. 🛠️ RECOMMENDATIONS FOR IMPROVEMENT
- **Cross-file Navigation:** A tool to follow function calls across files (call graph).
- **Bulk Read:** `get_multiple_functions` for related logic scattered across a package.
- **More Context in Search:** An option to include ±10 lines around the match.

### 4. 📢 RECOMMENDATION
100% Recommended for exploring unfamiliar codebases and rapid function discovery. It's a productivity multiplier—tasks that took 10 minutes now take 1.
```

---

## 4. Semantic vs structural – how they work together

- **Semantic search** (recall):
  - operates on `CodeChunk` + embeddings,
  - tools like `rag_hybrid_search` / `rag_search_code` should return:
    - `[]SymbolDescriptor` + small snippets of code.

- **Structural / analytic** (reasoning):
  - operates on the already-selected chunk,
  - for a specific symbol, the recommended tools are:
    - `rag_find_type_definition(json)` → `ClassDescriptor`,
    - `rag_get_function_details(json)` → `FunctionDescriptor`,
    - `rag_list_package_exports(json)` → `[]SymbolDescriptor`.

This way, the AI uses very few tokens on raw code text and instead has a
clear, standardized map of symbols via the schema above.

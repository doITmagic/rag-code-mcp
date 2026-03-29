# Go Template Parser — Structural Analysis

> Added in PR #47 · Branch: `feat/go-template-parser` → `dev`

## Overview

Adds full structural analysis support for **Go HTML templates** (`.html`, `.tmpl`, `.gohtml`) to the RagCode indexer. The new parser extracts template definitions, includes, block relationships, control-flow blocks, variables, and custom functions — and emits them as typed symbols with `RelDependency` edges for cross-file linking.

## New Files

| File | Purpose |
|------|---------|
| `pkg/parser/html/gotemplate/analyzer.go` | Line-by-line scanner: extracts defines, blocks, templates, range/with/if/else-if, variables, custom funcs |
| `pkg/parser/html/gotemplate/adapter.go` | Converts `GoTemplate` structs → `pkgParser.Symbol` + `RelDependency` relations |
| `pkg/parser/html/gotemplate/analyzer_test.go` | Unit tests covering layout, page, partial, multi-file, else-if, block relations |

## Modified Files

| File | Change |
|------|--------|
| `pkg/parser/html/analyzer.go` | Integrates GoTemplate detection into the existing HTML analyzer (single directory walk, logger-based error handling) |
| `pkg/parser/go/analyzer.go` | Template file dependency extraction also handles `*ast.Ident` calls (dot-imports, wrappers) |
| `internal/uninstall/uninstall.go` | V2 registry detection tightened: `Version == "v2"` instead of `!= ""` |

## What Gets Indexed

For each `.html` / `.tmpl` / `.gohtml` file containing `{{` syntax:

- **`{{define "name"}}`** → symbol of kind `template`, with start/end lines
- **`{{block "name" .}}`** → symbol of kind `block`, parent relation to enclosing define
- **`{{template "name" .}}`** → `RelDependency` edge from current template to included one
- **`{{range .Items}}`** / **`{{with .Obj}}`** → recorded in template metadata
- **`{{if ...}}` / `{{else if ...}}`** → correct stack handling (no extra `{{ end }}` consumed)
- **`.Variables`** → all dot-variables extracted from inside any `{{ ... }}` action, including pipelines like `{{ .Body | truncate 200 }}` and lowercase vars like `.user`, `.items`
- **Custom functions** → non-keyword identifiers followed by arguments

## Architecture

```
html/analyzer.go
  └─ single WalkDir (GoTemplate detection)
  └─ ca.AnalyzePaths (HTML DOM analysis)
       └─ gotemplate/analyzer.go  ← analyzeFile()
            └─ gotemplate/adapter.go ← ConvertToSymbols()
```

## Review Fixes Applied (PR #47)

| # | Issue | Fix |
|---|-------|-----|
| 1 | Double I/O in directory walk | Single `WalkDir` for GoTemplate; DOM via `AnalyzePaths` |
| 2 | `WalkDir` errors silently ignored | Logged via `logger.Instance.Debug` |
| 3 | `fmt.Fprintf(os.Stderr)` in library code | Replaced with project logger |
| 4 | `*ast.Ident` template deps missed | Added Ident case in `extractCallsFromAST` |
| 5 | V2 registry: loose version check | `== "v2"` exact match |
| 6 | Variables in pipelines missed | `reAction` + `reActionVar` pattern replacing narrow `reVariable` |
| 7 | `reActionVar` missed lowercase vars | Regex broadened to `[A-Za-z_]` |
| 8 | `htmlPaths` collected but unused | Variable removed entirely |
| 9 | `scanner.Err()` unchecked after scan | Not yet addressed (future work) |

## Tests

```bash
go test ./pkg/parser/html/...
go test ./pkg/parser/go/...
go test ./internal/uninstall/...
```

All pass ✅

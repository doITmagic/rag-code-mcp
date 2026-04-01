# Go Template Parser — Structural Analysis

> Added in PR #47 · Branch: `feat/go-template-parser` → `dev`

## Overview

Adds full structural analysis support for **Go HTML templates** (`.html`, `.tmpl`, `.gohtml`) to the RagCode indexer. The new parser extracts template definitions, includes, block relationships, control-flow blocks, variables, and custom functions — tracking them either as emitted `parser.Symbol` entries, relational edges like `RelInheritance`, or structured `metadata` attached to parent symbols.

## New Files

| File | Purpose |
|------|---------|
| `pkg/parser/html/gotemplate/analyzer.go` | Line-by-line scanner: extracts defines, blocks, templates, range/with/if/else-if, variables, custom funcs |
| `pkg/parser/html/gotemplate/adapter.go` | Converts `GoTemplate` structs → `parser.Symbol` + `RelDependency` relations |
| `pkg/parser/html/gotemplate/analyzer_test.go` | Unit tests covering layout, page, partial, multi-file, else-if, block relations |

## Modified Files

| File | Change |
|------|--------|
| `pkg/parser/html/analyzer.go` | Integrates GoTemplate detection into the existing HTML analyzer (single directory walk, logger-based error handling) |
| `pkg/parser/go/analyzer.go` | Template file dependency extraction also handles `*ast.Ident` calls (dot-imports, wrappers) |
| `internal/uninstall/uninstall.go` | V2 registry detection tightened: `Version == "v2"` instead of `!= ""` |

## What Gets Indexed

For each `.html` / `.tmpl` / `.gohtml` file containing `{{` syntax:

- **`{{define "name"}}`** → `parser.Type` symbol with `metadata.template_type = go_template_define` and start/end lines (plus a separate file-level `go_template` symbol)
- **`{{block "name" .}}`** → represented via `RelInheritance` relations + `blocks` metadata on the file-level template symbol (no standalone `block` symbol)
- **`{{template "name" .}}`** → `RelDependency` edge from current template to included one
- **`{{range .Items}}`** → recorded in template metadata; **`{{with .Obj}}`** → only its source ranges are tracked (no dedicated symbol metadata yet)
- **`{{if ...}}` / `{{else if ...}}`** → correct stack handling (no extra `{{ end }}` consumed)
- **`.Variables`** → all dot-variables extracted from inside any `{{ ... }}` action, including pipelines like `{{ .Body | truncate 200 }}` and lowercase vars like `.user`, `.items`
- **Custom functions** → non-keyword identifiers followed by arguments

## Architecture

```
html/analyzer.go
  ├─ WalkDir (GoTemplate detection)
  │    └─ gotemplate/analyzer.go (analyzeFile)
  │         └─ gotemplate/adapter.go (ConvertToSymbols)
  └─ ca.AnalyzePaths (HTML DOM analysis)
```

## Review Fixes Applied (PR #47)

| # | Issue | Fix |
|---|-------|-----|
| 1 | Double I/O in directory walk | Reduced for GoTemplate; DOM analysis still performs separate walk |
| 2 | `WalkDir` errors silently ignored | Logged via `logger.Instance.Debug` |
| 3 | `fmt.Fprintf(os.Stderr)` in library code | Replaced with project logger |
| 4 | `*ast.Ident` template deps missed | Added Ident case in `extractCallsFromAST` |
| 5 | V2 registry: loose version check | `== "v2"` exact match |
| 6 | Variables in pipelines missed | `reAction` + `reActionVar` pattern replacing narrow `reVariable` |
| 7 | `reActionVar` missed lowercase vars | Regex broadened to `[A-Za-z_]` |
| 8 | `htmlPaths` collected but unused | Variable removed entirely |
| 9 | scanner.Err() unchecked after scan | Fixed: added check in analyzeFile |

## Tests

```bash
go test ./pkg/parser/html/...
go test ./pkg/parser/go/...
go test ./internal/uninstall/...
```

All pass ✅

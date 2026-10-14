# HTML & Go Template Parser (`pkg/parser/html`)

The `html` package provides semantic analysis for **HTML** documents (`.html`, `.htm`) and **Go Templates** (`.tmpl`, `.gotmpl`, `.gohtml`, `.tpl`).

## 🎯 Extracted Symbols

1. **HTML Documents (`analyzer.go`)**:
   - Extracts `<title>`, meta descriptions, and semantic sections based on heading hierarchy (`<h1>`–`<h6>`) and landmark tags (`<header>`, `<nav>`, `<main>`, `<section>`, `<article>`, `<aside>`, `<footer>`).
   - Strips inline `<script>`, `<style>`, and `<noscript>` content before building prose chunks so code is not mixed into document text.
   - Extracts interactive/identifiable DOM elements (`id`, `class`, `<form>`, `<dialog>`, `<template>`), custom web components, and external asset links.
2. **Go Templates (`gotemplate/analyzer.go`)**:
   - Parses `html/template` and `text/template` directives left-to-right across lines:
     - `{{define "name"}} ... {{end}}`
     - `{{block "name" pipeline}} ... {{end}}`
     - `{{template "name" pipeline}}` (partials/includes)
     - `{{range $i, $v := .Items}} ... {{end}}` and `{{with ...}} ... {{end}}`
     - Custom template comments `{{/* ... */}}` attached as docstrings to `define`/`block` symbols.

---

## 🏗️ Package Structure

- `html_analyzer.go`: Implements `parser.Analyzer` and routes `.html`/`.htm` and Go template files.
- `analyzer.go`: GoQuery DOM analyzer for HTML structure, headings, forms, and web components.
- `gotemplate/analyzer.go`: Dedicated Go template block, partial, and pipeline analyzer.
- `types.go`: Data structures for HTML sections, elements, and template blocks.

# Documentation & Structured Config Parser (`pkg/parser/docs`)

The `docs` package chunks documentation and structured data files into semantic `section` symbols suitable for hybrid and vector search (`include_docs: true` in `rag_search`).

## 📂 Supported Formats

- **Markdown (`.md`, `.mdx`, `.rst`, `.txt`)**:
  - Splits documents by heading hierarchy (`#` through `######`), preserving parent heading breadcrumbs (`Parent > Child`) in `Signature`.
  - Captures paragraphs, bullet/ordered lists, fenced code blocks (` ``` `), indented code blocks, Markdown tables, and inline HTML blocks.
  - Sub-chunks oversized sections into overlapping ~2000-character parts on paragraph boundaries.
- **JSON, YAML, TOML, XML (`.json`, `.yaml`, `.yml`, `.toml`, `.xml`)**:
  - Uses Tree-sitter (`treesitter.go`) with sibling coalescing (grouping small keys/entries into ~1200-byte chunks) and UTF-8 rune-safe truncation.
  - Automatically falls back to top-level key/line-window chunking (`fallbackLineChunks`) for complex YAML files (such as `docker-compose.yml` or GitHub Actions workflows) so no content is lost.

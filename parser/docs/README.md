[RagCode v3](../../README.md) › [parser](../README.md) › docs

# Documentation and Data Analyzer

The `docs` package makes prose and configuration searchable next to code. It
splits Markdown and reStructuredText into sections, JSON / YAML / TOML into
keys, and any other text into fixed line windows. Every output symbol has
kind `chunk`.

No tree-sitter here: Markdown, reST and TOML use line scanners (the package
notes that the tree-sitter Markdown grammar cost about 1 s per MB on flat
CHANGELOG-style files), JSON uses `encoding/json`'s token decoder and YAML
uses `gopkg.in/yaml.v3`.

## Where it sits

- **Registered** from `init()`; the binary pulls it in through a blank import
  in [`internal/app`](../../internal/app/README.md).
- **Called** by [`index`](../../index/README.md) and
  [`tools`](../../tools/README.md) through `parser.Analyze`.
- **Calls** `RelModule` and `Finalize` from [parser](../README.md). No file
  system access.

| Property | Value |
|----------|-------|
| Language | `docs` |
| Extensions | `.md`, `.mdx`, `.markdown`, `.json`, `.yaml`, `.yml`, `.toml`, `.txt`, `.rst` |

| Extension | Handler | Fallback |
|-----------|---------|----------|
| `.md`, `.mdx`, `.markdown` | `markdown` (ATX headings) | line windows when there is no heading |
| `.rst` | `rst` (underlined titles) | line windows when there is no title |
| `.json` | `json` | line windows for invalid JSON or a non-object top level |
| `.yaml`, `.yml` | `yaml` | line windows when the first document fails to decode or yields no keys |
| `.toml` | `toml` | line windows when no table or key is found |
| `.txt` | `plain` (200-line windows) | |

## Common shape of a chunk

| Field | Value |
|-------|-------|
| `Kind` | `chunk` |
| `Module` | Path relative to the root without extension (`docs/guide`); the stem for a file outside the root |
| `Name` | Heading, title, key, or file stem |
| `Owner` | Parent heading (Markdown) or parent key / table (data files) |
| `Signature` | The first line of the chunk (the heading or key line), at most 200 bytes |
| `Content` | The chunk's lines |
| `Exported` | always true |
| `Metadata` | `doc=readme` for files whose name starts with `README.` |

A file emits at most 500 symbols.

## Markdown

- One chunk per ATX heading (`#` to `######`). A heading spans to the line
  before the next heading of the same or a higher level, so a chunk includes
  its subsections.
- The **owner** is the nearest enclosing heading of a lower level (its name
  only, not a path).
- Lines inside fenced blocks (```` ``` ```` or `~~~`) and lines indented
  more than three spaces are never headings.
- A closing `#` sequence is stripped only when a space precedes it
  (`Guide #` -> `Guide`, `C#` stays `C#`). An empty heading is named after
  the file stem.
- Text before the first heading becomes a chunk named after the file stem.
- A section longer than 400 lines (trailing blank lines dropped) is split
  into parts named `Name (1/3)`, `Name (2/3)`, ...; every part keeps the
  heading as its signature.
- **Links**: every non-image local link in a section's own text (before its
  first subsection) adds an `imports` relation on the part holding the line.
  The target qualified name is the linked file's module when the link stays
  inside the root and points at a documented extension (`.md .mdx .markdown
  .rst .txt .json .yaml .yml .toml`); `""` otherwise. Anchors (`#x`),
  absolute paths (`/x`) and URLs (anything containing `:`) are skipped.

### Example

File `/ws/README.md`:

```markdown
Intro before any heading.

# Project

See [spec](specs/spec.md#rules).

## Install

Steps.
```

| Qualified name | Owner | Lines |
|----------------|-------|-------|
| `README.README` | | 1 |
| `README.Project` | | 3-9 |
| `README.Project.Install` | `Project` | 7-9 |

`README.Project` has an `imports` relation to `specs/spec.md#rules` with target
`specs/spec`. All three carry `Metadata["doc"] = "readme"`.

## reStructuredText

A title is a text line followed by a line of one repeated punctuation
character (`= - ~ ^ " ' * + # : . _`, at least 3 long and at least as long
as the title). An overline equal to the underline is folded into the section.
Each title spans to the next one. Owners are not tracked, and links are not
extracted. Long sections are split like Markdown.

## JSON

Top-level members become chunks; members of a nested object become chunks
owned by their parent key. Deeper levels stay inside the parent's content and
arrays are never expanded. A chunk spans from the key's line to the value's
last line, but its content is the exact `"key": value` source, so a minified
one-line file does not repeat the whole file in every chunk. Content is
capped at 100 lines and 16 KiB.

## YAML

Every document of a multi-document file is decoded into a `yaml.Node` tree.
Mapping keys of the top level become chunks, and keys of a nested mapping
become chunks owned by their parent key; deeper levels stay in the content.
Because `yaml.v3` records only start lines, a key spans to the line before
the next key (or the next document's `---`). Content is capped at 100 lines
and 16 KiB.

## TOML

`[table]` and `[[table]]` headers become chunks, and keys become chunks
owned by the current table (the owner is the table name as written, e.g.
`tool.poetry`). A key spans to the line before the next key or header, so
multi-line values stay attached; a table spans to the next header. Content is
capped at 100 lines and 16 KiB.

```toml
[server]
port = 8080
```

For `/ws/config.toml`: `config.server` and `config.server.port`.

## Plain text

`.txt` files (and every fallback) are cut into 200-line windows; blank
windows are skipped. A single window is named after the file stem, several
are named `stem (1/k)`, `stem (2/k)`, ...

## Known limitations visible in the code

- The TOML scanner is line based: a line inside a multi-line string that
  looks like `k = v` is read as a key.
- A one-line YAML flow mapping gives every key up to 16 KiB of the shared
  line as content.
- reST sections have no owners and no link relations.
- Data files are indexed two levels deep; arrays are not expanded.

## Files

| File | Contents |
|------|----------|
| `docs.go` | Package doc, registration, limits, extension dispatch, the per-call `doc` state, chunk building and splitting, plain-text windows, the Markdown heading scanner with link relations, the reST title scanner, byte-offset-to-line helpers. |
| `data.go` | Data files: the JSON member walker (`jsonObject`, `jsonValue`), the YAML node walker, the TOML line scanner, and content capping. |

### Tests

| File | Covers |
|------|--------|
| `docs_test.go` | A README with fences, nesting and links; splitting of long sections; Markdown without headings; JSON (nested objects, invalid and non-object input, long values capped); YAML (and invalid YAML falling back); TOML; a 2000-line text file; reST; malformed and edge inputs; the symbol cap; registration; concurrency. |
| `zz_verify_test.go` | Verification tests: links and headings, nested TOML arrays, minified JSON content size, keys on their own line, YAML comments after `---`, timing, robustness, a CHANGELOG-style file, data edge cases, Markdown owners and spans, JSON / YAML / TOML nesting, reST and module names, determinism and the cap, content-size guards for minified JSON and split sections. |

## Testing

```bash
cd v3
go test ./parser/docs/
```

No build tags or external services.

## See also

- [parser](../README.md): symbol model, qualified names, registry
- [web](../web/README.md): HTML and Go templates
- [index](../../index/README.md): walks the workspace and indexes these chunks

// Package docs analyzes documentation and data files. Markdown sections,
// JSON/YAML/TOML keys and plain-text windows all become KindChunk symbols so
// that prose and configuration are searchable next to code.
package docs

import (
	"fmt"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/doITmagic/rag-code-mcp/v3/parser"
)

func init() { parser.Register(analyzer{}) }

type analyzer struct{}

func (analyzer) Language() string { return "docs" }
func (analyzer) Extensions() []string {
	return []string{".md", ".mdx", ".markdown", ".json", ".yaml", ".yml", ".toml", ".txt", ".rst"}
}

const (
	maxSymbols      = 500 // per file
	maxSectionLines = 400 // longer Markdown/rst sections are split into parts
	maxValueLines   = 100 // data values are capped at this many lines of content
	plainChunkLines = 200 // headingless text is chunked by this many lines
	maxSignature    = 200 // bytes; a minified file's first line is the whole file
	maxValueBytes   = 16 << 10
)

// doc is the per-call state every emitter reads: the file, its lines and the
// module every chunk belongs to. Analyze builds one and never shares it.
type doc struct {
	f      parser.File
	module string
	stem   string
	lines  []string
	meta   map[string]string
}

func (analyzer) Analyze(f parser.File) ([]parser.Symbol, error) {
	d := newDoc(f)
	var syms []parser.Symbol
	switch strings.ToLower(filepath.Ext(f.Path)) {
	case ".md", ".mdx", ".markdown":
		syms = d.markdown()
	case ".json":
		syms = d.json()
	case ".yaml", ".yml":
		syms = d.yaml()
	case ".toml":
		syms = d.toml()
	case ".rst":
		syms = d.rst()
	default:
		syms = d.plain()
	}
	if len(syms) > maxSymbols {
		syms = syms[:maxSymbols]
	}
	return parser.Finalize(f, syms), nil
}

func newDoc(f parser.File) *doc {
	base := filepath.Base(f.Path)
	d := &doc{f: f, stem: strings.TrimSuffix(base, filepath.Ext(base)), lines: strings.Split(string(f.Source), "\n")}
	if n := len(d.lines); n > 1 && d.lines[n-1] == "" { // a trailing newline is not a line
		d.lines = d.lines[:n-1]
	}
	d.module, _ = parser.RelModule(f.Root, f.Path)
	if strings.HasPrefix(strings.ToUpper(base), "README.") {
		d.meta = map[string]string{"doc": "readme"}
	}
	return d
}

// chunk builds one symbol spanning lines start..end (1-based, inclusive,
// clamped to the file). Signature is the first line, which is the heading
// for a section and the key line for a data value.
func (d *doc) chunk(owner, name string, start, end int) parser.Symbol {
	start, end = d.clamp(start, end)
	return parser.Symbol{
		Name:      name,
		Kind:      parser.KindChunk,
		Module:    d.module,
		Owner:     owner,
		Signature: clip(strings.TrimSpace(d.lines[start-1]), maxSignature),
		Content:   d.text(start, end),
		FilePath:  d.f.Path,
		StartLine: start,
		EndLine:   end,
		Language:  "docs",
		Exported:  true,
		Metadata:  d.meta,
	}
}

// clip cuts s to at most n bytes without splitting a UTF-8 character.
func clip(s string, n int) string {
	if len(s) <= n {
		return s
	}
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n]
}

func (d *doc) clamp(start, end int) (int, int) {
	if start < 1 {
		start = 1
	}
	if start > len(d.lines) {
		start = len(d.lines)
	}
	if end > len(d.lines) {
		end = len(d.lines)
	}
	if end < start {
		end = start
	}
	return start, end
}

func (d *doc) text(start, end int) string {
	start, end = d.clamp(start, end)
	return strings.Join(d.lines[start-1:end], "\n")
}

// trimEnd drops trailing blank lines from a range.
func (d *doc) trimEnd(start, end int) int {
	start, end = d.clamp(start, end)
	for end > start && strings.TrimSpace(d.lines[end-1]) == "" {
		end--
	}
	return end
}

// section emits a titled range, split into "name (i/k)" parts when it is
// longer than maxSectionLines. Every part keeps the heading as Signature.
func (d *doc) section(owner, name string, start, end int) []parser.Symbol {
	end = d.trimEnd(start, end)
	n := end - start + 1
	if n <= maxSectionLines {
		return []parser.Symbol{d.chunk(owner, name, start, end)}
	}
	k := (n + maxSectionLines - 1) / maxSectionLines
	sig := clip(strings.TrimSpace(d.lines[start-1]), maxSignature)
	out := make([]parser.Symbol, 0, k)
	for i := 0; i < k; i++ {
		ps := start + i*maxSectionLines
		s := d.chunk(owner, fmt.Sprintf("%s (%d/%d)", name, i+1, k), ps, min(end, ps+maxSectionLines-1))
		s.Signature = sig
		out = append(out, s)
	}
	return out
}

// plain chunks the whole file by plainChunkLines; blank windows are skipped.
func (d *doc) plain() []parser.Symbol {
	n := len(d.lines)
	k := (n + plainChunkLines - 1) / plainChunkLines
	var out []parser.Symbol
	for i := 0; i < k; i++ {
		start := i*plainChunkLines + 1
		end := d.trimEnd(start, min(n, start+plainChunkLines-1))
		if strings.TrimSpace(d.text(start, end)) == "" {
			continue
		}
		name := d.stem
		if k > 1 {
			name = fmt.Sprintf("%s (%d/%d)", d.stem, i+1, k)
		}
		out = append(out, d.chunk("", name, start, end))
	}
	return out
}

// ---- Markdown ----

// markdown is a line scanner: one chunk per ATX heading, owned by the nearest
// heading of a lower level, spanning to the next heading of the same or a
// higher level. Lines inside fenced blocks are never headings. Text before
// the first heading is a chunk named by the file stem; a file without
// headings falls back to line windows. The tree-sitter markdown grammar was
// dropped here: it costs ~1 s per MB of flat CHANGELOG-style files.
func (d *doc) markdown() []parser.Symbol {
	type heading struct {
		level, line int
		name        string
	}
	var hs []heading
	fence, fenceLen := byte(0), 0
	for i, line := range d.lines {
		t := strings.TrimLeft(line, " ")
		if len(line)-len(t) > 3 {
			continue // indented code
		}
		if fence != 0 {
			if n := len(t) - len(strings.TrimLeft(t, string(fence))); n >= fenceLen && strings.TrimSpace(t[n:]) == "" {
				fence = 0
			}
			continue
		}
		if c := t[:min(3, len(t))]; c == "```" || c == "~~~" {
			fence, fenceLen = c[0], len(t)-len(strings.TrimLeft(t, c[:1]))
			continue
		}
		rest := strings.TrimLeft(t, "#")
		level := len(t) - len(rest)
		if level < 1 || level > 6 || (rest != "" && rest[0] != ' ' && rest[0] != '\t') {
			continue
		}
		name := headingText(rest)
		if name == "" {
			name = d.stem
		}
		hs = append(hs, heading{level: level, line: i + 1, name: name})
	}
	if len(hs) == 0 {
		return d.plain()
	}
	var syms []parser.Symbol
	if hs[0].line > 1 && strings.TrimSpace(d.text(1, hs[0].line-1)) != "" {
		syms = d.section("", d.stem, 1, hs[0].line-1)
		d.links(syms, 1, hs[0].line-1)
	}
	var stack []heading // open ancestors, strictly increasing level
	for i, h := range hs {
		if len(syms) >= maxSymbols {
			break
		}
		for len(stack) > 0 && stack[len(stack)-1].level >= h.level {
			stack = stack[:len(stack)-1]
		}
		owner := ""
		if len(stack) > 0 {
			owner = stack[len(stack)-1].name
		}
		stack = append(stack, h)
		end, ownEnd := len(d.lines), len(d.lines)
		for _, next := range hs[i+1:] {
			ownEnd = min(ownEnd, next.line-1)
			if next.level <= h.level {
				end = next.line - 1
				break
			}
		}
		parts := d.section(owner, h.name, h.line, end)
		d.links(parts, h.line, ownEnd)
		syms = append(syms, parts...)
	}
	return syms
}

// headingText trims an ATX heading's optional closing sequence ("Guide #" ->
// "Guide"); CommonMark only strips it when a space precedes it, so "C#" stays.
func headingText(s string) string {
	s = strings.TrimSpace(s)
	if t := strings.TrimRight(s, "#"); t != s && strings.HasSuffix(t, " ") {
		return strings.TrimSpace(t)
	}
	return s
}

// linkRe matches a non-image Markdown link and captures its destination.
var linkRe = regexp.MustCompile(`(?:^|[^!])\[[^\]]*\]\(([^)\s]+)`)

var docExts = map[string]bool{".md": true, ".mdx": true, ".markdown": true, ".rst": true, ".txt": true, ".json": true, ".yaml": true, ".yml": true, ".toml": true}

// links adds a RelImports relation for every local link on lines start..end
// (the section's own text, before its first subsection) to the part that
// holds the line. The target module is the linked file's module when the
// link stays inside the root and points at a documented extension; "" otherwise.
func (d *doc) links(parts []parser.Symbol, start, end int) {
	start, end = d.clamp(start, end)
	for line := start; line <= end; line++ {
		for _, m := range linkRe.FindAllStringSubmatch(d.lines[line-1], -1) {
			dest := m[1]
			if dest == "" || dest[0] == '#' || dest[0] == '/' || strings.Contains(dest, ":") {
				continue
			}
			for i := range parts {
				if p := &parts[i]; line >= p.StartLine && line <= p.EndLine {
					p.Relations = append(p.Relations, parser.Relation{Kind: parser.RelImports, TargetName: dest, TargetQualifiedName: d.resolve(dest), Line: line})
					break
				}
			}
		}
	}
}

func (d *doc) resolve(dest string) string {
	if i := strings.IndexAny(dest, "#?"); i >= 0 {
		dest = dest[:i]
	}
	ext := path.Ext(dest)
	if !docExts[strings.ToLower(ext)] {
		return ""
	}
	p := path.Join(path.Dir(d.module), dest)
	if p == ".." || strings.HasPrefix(p, "../") {
		return ""
	}
	return strings.TrimSuffix(p, ext)
}

// ---- reStructuredText ----

// rst splits on underlined titles (a text line followed by a line of one
// repeated punctuation character at least as long as it). Overlines are
// folded into the section. Owners are not tracked: rst levels depend on the
// order the underline characters first appear, which is not worth the code.
func (d *doc) rst() []parser.Symbol {
	type title struct {
		name       string
		start, end int
	}
	var titles []title
	for i := 1; i < len(d.lines); i++ {
		t, u := strings.TrimSpace(d.lines[i-1]), strings.TrimSpace(d.lines[i])
		if t == "" || !isUnderline(u) || isUnderline(t) || len(u) < len(t) {
			continue
		}
		start := i
		if i >= 2 && strings.TrimSpace(d.lines[i-2]) == u {
			start = i - 1
		}
		if n := len(titles); n > 0 {
			titles[n-1].end = start - 1
		}
		titles = append(titles, title{name: t, start: start, end: len(d.lines)})
	}
	if len(titles) == 0 {
		return d.plain()
	}
	var out []parser.Symbol
	if titles[0].start > 1 && strings.TrimSpace(d.text(1, titles[0].start-1)) != "" {
		out = append(out, d.section("", d.stem, 1, titles[0].start-1)...)
	}
	for _, t := range titles {
		out = append(out, d.section("", t.name, t.start, t.end)...)
	}
	return out
}

func isUnderline(s string) bool {
	if len(s) < 3 || !strings.ContainsRune(`=-~^"'*+#:._`, rune(s[0])) {
		return false
	}
	return strings.Count(s, s[:1]) == len(s)
}

// ---- byte offset to line ----

// newlines returns the offsets of every '\n' so lineOf can binary-search.
func newlines(src []byte) []int {
	var nl []int
	for i, b := range src {
		if b == '\n' {
			nl = append(nl, i)
		}
	}
	return nl
}

// lineOf returns the 1-based line holding byte offset off.
func lineOf(nl []int, off int) int {
	if off < 0 {
		return 1
	}
	return sort.SearchInts(nl, off) + 1
}

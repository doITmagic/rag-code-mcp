// Package web analyzes HTML (with Go template actions) and CSS/SCSS/Sass/Less
// files with tree-sitter. Module is the file path relative to the workspace
// root without extension; relations to other files resolve only through
// relative hrefs and @import/@use paths, never by guessing.
package web

import (
	"path/filepath"
	"sort"
	"strings"

	"github.com/doITmagic/rag-code-mcp/v3/parser"
)

const (
	maxSymbols      = 2000 // per file
	maxContentLines = 400  // HTML document and element content
	maxSelector     = 120  // CSS rule name
)

func init() {
	parser.Register(htmlAnalyzer{})
	parser.Register(cssAnalyzer{})
}

// stem is the file name without extension.
func stem(path string) string {
	b := filepath.Base(path)
	return strings.TrimSuffix(b, filepath.Ext(b))
}

// moduleName is the file's module; files outside the root use their stem.
func moduleName(f parser.File) string {
	m, _ := parser.RelModule(f.Root, f.Path)
	return m
}

// refModule resolves a relative reference (href, src, @import path) from
// f's directory to a module inside the root. Absolute paths, URLs and
// references outside the root give "".
func refModule(f parser.File, ref string) string {
	if i := strings.IndexAny(ref, "?#"); i >= 0 {
		ref = ref[:i]
	}
	if ref == "" || strings.HasPrefix(ref, "/") || strings.Contains(ref, ":") {
		return ""
	}
	if m, ok := parser.RelModule(f.Root, filepath.Join(filepath.Dir(f.Path), filepath.FromSlash(ref))); ok {
		return m
	}
	return ""
}

// collapse joins all whitespace runs into single spaces.
func collapse(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

// capLines keeps at most n lines of s.
func capLines(s string, n int) string {
	for i, c := range s {
		if c == '\n' {
			if n--; n == 0 {
				return s[:i]
			}
		}
	}
	return s
}

// capRunes keeps at most n runes of s.
func capRunes(s string, n int) string {
	if r := []rune(s); len(r) > n {
		return string(r[:n])
	}
	return s
}

// trimComment strips HTML and CSS comment markers and per-line "*" / "//"
// prefixes.
func trimComment(s string) string {
	s = strings.TrimSpace(s)
	for _, p := range [][2]string{{"<!--", "-->"}, {"/*", "*/"}} {
		if strings.HasPrefix(s, p[0]) {
			s = strings.TrimSuffix(strings.TrimPrefix(s, p[0]), p[1])
		}
	}
	lines := strings.Split(s, "\n")
	for i, l := range lines {
		l = strings.TrimSpace(l)
		l = strings.TrimPrefix(l, "//")
		l = strings.TrimPrefix(l, "*")
		lines[i] = strings.TrimSpace(l)
	}
	return strings.TrimSpace(strings.Join(lines, "\n"))
}

// lineIndex maps byte offsets to 1-based lines. A trailing newline does not
// count as an extra line.
type lineIndex []int // start offset of each line

func newLineIndex(src []byte) lineIndex {
	idx := lineIndex{0}
	for i, b := range src {
		if b == '\n' && i+1 < len(src) {
			idx = append(idx, i+1)
		}
	}
	return idx
}

func (l lineIndex) lineOf(off int) int {
	return sort.Search(len(l), func(i int) bool { return l[i] > off })
}

// text returns the source of lines [from, to], 1-based inclusive.
func (l lineIndex) text(src []byte, from, to int) string {
	end := len(src)
	if to < len(l) {
		end = l[to]
	}
	return strings.TrimRight(string(src[l[from-1]:end]), "\n")
}

// exportedName is false for the "_name" / "-name" private convention.
func exportedName(name string) bool {
	name = strings.TrimLeft(name, "$-")
	return !strings.HasPrefix(name, "_")
}

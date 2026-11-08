package php

import (
	"path/filepath"
	"regexp"
	"strings"

	"github.com/doITmagic/rag-code-mcp/v3/parser"
)

// bladeDirective matches the directives the scanner cares about with their
// first quoted argument; group 3 is set when more arguments follow.
var bladeDirective = regexp.MustCompile(
	`@(extends|includeIf|include|each|section|component|livewire|endsection|endcomponent|stop|show|append|overwrite)\b` +
		`(?:\s*\(\s*['"]([^'"]*)['"]\s*(,)?\s*\)?)?`)

// blade scans a .blade.php view line by line: one template symbol for the
// file, one per @section/@component/@livewire, and an imports relation per
// @extends/@include/@includeIf/@each. Module is the view's dotted directory
// below the nearest "views" folder, so "layouts.app" resolves exactly.
func blade(f parser.File) []parser.Symbol {
	base := filepath.Base(f.Path)
	stem := base[:len(base)-len(".blade.php")] // Analyze checked the suffix
	lines := strings.Split(string(f.Source), "\n")
	syms := []parser.Symbol{{
		Name: stem, Kind: parser.KindTemplate, Module: viewModule(f.Path), Signature: "@" + stem,
		Content: string(f.Source), StartLine: 1, EndLine: max(1, strings.Count(strings.TrimSuffix(string(f.Source), "\n"), "\n")+1), Exported: true,
	}}
	var open []int // indexes of the block symbols waiting for their closing directive, innermost last
	for i, line := range lines {
		ln := i + 1
		for _, m := range bladeDirective.FindAllStringSubmatch(line, -1) {
			switch m[1] {
			case "extends", "include", "includeIf", "each":
				if m[2] != "" {
					syms[0].Relations = append(syms[0].Relations, parser.Relation{
						Kind: parser.RelImports, TargetName: m[2], TargetQualifiedName: m[2], Line: ln,
					})
				}
			case "section", "component", "livewire":
				if m[2] == "" {
					continue
				}
				syms = append(syms, parser.Symbol{
					Name: m[2], Kind: parser.KindTemplate, Module: syms[0].Module, Owner: stem,
					Signature: strings.TrimSpace(m[0]), Content: line, StartLine: ln, EndLine: ln, Exported: true,
				})
				if m[1] == "component" || (m[1] == "section" && m[3] == "") {
					open = append(open, len(syms)-1)
				}
			default: // a closing directive
				if k := len(open) - 1; k >= 0 {
					s := &syms[open[k]]
					s.EndLine, s.Content = ln, strings.Join(lines[s.StartLine-1:ln], "\n")
					open = open[:k]
				}
			}
		}
	}
	return syms
}

// viewModule is the dotted path of the view's directory below the last
// "views" folder in its path, "" when there is none.
func viewModule(path string) string {
	segs := strings.Split(filepath.ToSlash(filepath.Dir(path)), "/")
	for i := len(segs) - 1; i >= 0; i-- {
		if segs[i] == "views" {
			return strings.Join(segs[i+1:], ".")
		}
	}
	return ""
}

package php

import (
	"path/filepath"
	"regexp"
	"strings"

	"github.com/doITmagic/rag-code-mcp/v2/parser"
)

// bladeDirective matches the directives the scanner cares about with their
// first quoted argument; group 3 is set when more arguments follow.
var bladeDirective = regexp.MustCompile(
	`@(extends|includeIf|include|each|section|component|livewire|endsection|endcomponent|stop|show|append|overwrite)\b` +
		`(?:\s*\(\s*['"]([^'"]*)['"]\s*(,)?\s*\)?)?`)

// bladeComment matches a {{-- --}} comment, over several lines too.
var bladeComment = regexp.MustCompile(`(?s)\{\{--.*?--\}\}`)

// bladeTag matches an anonymous or class component tag <x-name> or
// <x-dir.name>; group 1 is the name.
var bladeTag = regexp.MustCompile(`<x-([\w.-]+)(?:[\s/>]|$)`)

// bladeLivewire matches a Livewire component tag <livewire:name> or
// <livewire:dir.name>; group 1 is the name.
var bladeLivewire = regexp.MustCompile(`<livewire:([\w.-]+)(?:[\s/>]|$)`)

// bladeRoute matches route('name') or to_route('name'), not ->route('id')
// (request()->route reads a parameter); group 1 is the name.
var bladeRoute = regexp.MustCompile(`(?:^|[^\w>:$])(?:to_)?route\(\s*['"]([^'"$]+)['"]`)

// componentClass is the class a component name maps to by convention under
// namespace ns: Livewire 3's "forum.post-list" is App\Livewire\Forum\PostList,
// a Blade class component's "forms.input" App\View\Components\Forms\Input.
func componentClass(ns, name string) string {
	segs := strings.Split(name, ".")
	for i, seg := range segs {
		var b strings.Builder
		for _, w := range strings.Split(seg, "-") {
			if w != "" {
				b.WriteString(strings.ToUpper(w[:1]) + w[1:])
			}
		}
		segs[i] = b.String()
	}
	return ns + strings.Join(segs, `\`)
}

// blade scans a .blade.php view line by line: one template symbol for the
// file, one per @section/@component/@livewire, and an imports relation per
// @extends/@include/@includeIf/@each/@component; an <x-name> tag imports
// the view components.name and uses the class App\View\Components\Name, a
// @livewire('x') or <livewire:x> imports the view livewire.x and uses the
// class App\Livewire\X. Module is
// the view's dotted directory below the nearest "views" folder, so
// "layouts.app" resolves exactly.
func blade(f parser.File) []parser.Symbol {
	base := filepath.Base(f.Path)
	stem := base[:len(base)-len(".blade.php")] // Analyze checked the suffix
	lines := strings.Split(string(f.Source), "\n")
	syms := []parser.Symbol{{
		Name: stem, Kind: parser.KindTemplate, Module: viewModule(f.Path), Signature: "@" + stem,
		Content: string(f.Source), StartLine: 1, EndLine: max(1, strings.Count(strings.TrimSuffix(string(f.Source), "\n"), "\n")+1), Exported: true,
	}}
	// Directives are read with the comments blanked out, newlines kept, so
	// line numbers and the symbols' content stay those of the source.
	scan := strings.Split(bladeComment.ReplaceAllStringFunc(string(f.Source), func(c string) string {
		return strings.Map(func(r rune) rune {
			if r == '\n' {
				return r
			}
			return ' '
		}, c)
	}), "\n")
	imports := func(view string, ln int) {
		syms[0].Relations = append(syms[0].Relations, parser.Relation{
			Kind: parser.RelImports, TargetName: view, TargetQualifiedName: view, Line: ln,
		})
	}
	// A component is its view and, by convention, a class: Livewire 3's
	// livewire.<name> and App\Livewire\<Name>, a Blade component's
	// components.<name> and App\View\Components\<Name>.
	component := func(view, ns, name string, ln int) {
		imports(view+"."+name, ln)
		class := componentClass(ns, name)
		syms[0].Relations = append(syms[0].Relations, parser.Relation{
			Kind: parser.RelUsesType, TargetName: class[strings.LastIndex(class, `\`)+1:], TargetQualifiedName: class, Line: ln,
		})
	}
	var open []int // indexes of the block symbols waiting for their closing directive, innermost last
	for i, line := range scan {
		ln := i + 1
		for _, m := range bladeDirective.FindAllStringSubmatch(line, -1) {
			switch m[1] {
			case "extends", "include", "includeIf", "each":
				if m[2] != "" {
					imports(m[2], ln)
				}
			case "section", "component", "livewire":
				if m[2] == "" {
					continue
				}
				if m[1] == "component" {
					imports(m[2], ln)
				}
				if m[1] == "livewire" {
					component("livewire", `App\Livewire\`, m[2], ln)
				}
				syms = append(syms, parser.Symbol{
					Name: m[2], Kind: parser.KindTemplate, Module: syms[0].Module, Owner: stem,
					Signature: strings.TrimSpace(m[0]), Content: lines[i], StartLine: ln, EndLine: ln, Exported: true,
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
		for _, m := range bladeRoute.FindAllStringSubmatch(line, -1) {
			syms[0].Relations = append(syms[0].Relations, parser.Relation{
				Kind: parser.RelImports, TargetName: m[1], TargetQualifiedName: "route:" + m[1], Line: ln,
			})
		}
		for _, m := range bladeLivewire.FindAllStringSubmatch(line, -1) {
			component("livewire", `App\Livewire\`, m[1], ln)
		}
		for _, m := range bladeTag.FindAllStringSubmatch(line, -1) {
			if m[1] != "slot" && m[1] != "dynamic-component" {
				component("components", `App\View\Components\`, m[1], ln)
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

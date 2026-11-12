// Package javascript analyzes JavaScript, TypeScript and Vue single-file
// components with tree-sitter. Module is the file path relative to the
// workspace root without extension; call targets are resolved through the
// file's own import table and never guessed.
package javascript

import (
	"bytes"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"unicode"

	"github.com/odvcencio/gotreesitter"

	"github.com/doITmagic/rag-code-mcp/v2/parser"
)

func init() { parser.Register(analyzer{}) }

type analyzer struct{}

func (analyzer) Language() string { return "javascript" }
func (analyzer) Extensions() []string {
	return []string{".js", ".mjs", ".cjs", ".jsx", ".ts", ".tsx", ".vue"}
}

// fileCtx is the per-call state: source, import table and the names defined
// at the top level. It is built once per script and read by every symbol.
type fileCtx struct {
	src       []byte
	lang      *gotreesitter.Language
	module    string
	dir       string                 // module directory, base for relative specifiers
	root      string                 // workspace root, to tell a directory import from a file
	def       string                 // local name of the default export, qualified as "default"
	component string                 // PascalCase file stem, owner of component methods
	imports   map[string]string      // local name -> qualified name ("m.default", "m.name", "m")
	defs      map[string]parser.Kind // top-level declared names and what they are
	exported  map[string]bool        // names exported by export {}, module.exports, exports.x
	alias     *aliases               // tsconfig paths and project dir, loaded on the first bare specifier
	noFollow  bool                   // a barrel being read: imports are not followed through re-exports
	anonDef   bool                   // an `export default` with no local name (function, object, call)
	exportAs  map[string]string      // export { local as name } without a source: name -> local
	reexp     map[string]string      // export { a as b } from './x': b -> "x.a"
	stars     []string               // export * from './x': the modules x
	bare      map[string]bool        // export ... from 'pkg': package modules, not workspace files
	props     map[string]string      // "obj.key" -> local name of a shorthand / key: ident member of a top-level object literal ("" for a method of an inline export default {...})
	picked    map[string]bool        // default or named imports of a workspace module: their members are followed
	esm       bool                   // the script imports, requires or exports something
	classic   bool                   // a browser script (no esm): its wrappers' declarations count as top-level ones
}

func newCtx(src []byte, lang *gotreesitter.Language, wsRoot, module, dir, component string) *fileCtx {
	return &fileCtx{src: src, lang: lang, module: module, dir: dir, root: wsRoot, component: component,
		imports: map[string]string{}, defs: map[string]parser.Kind{}, exported: map[string]bool{},
		exportAs: map[string]string{}, reexp: map[string]string{}, bare: map[string]bool{},
		props: map[string]string{}, picked: map[string]bool{}}
}

func (analyzer) Analyze(f parser.File) ([]parser.Symbol, error) {
	module, dir := moduleName(f)
	component := pascal(strings.TrimSuffix(filepath.Base(f.Path), filepath.Ext(f.Path)))
	var syms []parser.Symbol
	if strings.EqualFold(filepath.Ext(f.Path), ".vue") {
		type script struct {
			b    vueBlock
			c    *fileCtx
			tree *gotreesitter.Tree
		}
		var scripts []script
		tplAt, tpl := -1, vueBlock{}
		for _, b := range vueBlocks(f.Source) {
			if b.template {
				tplAt, tpl = len(syms), b
				start, end := lineSpan(f.Source, b.start, b.end)
				syms = append(syms, parser.Symbol{
					Name: component, Kind: parser.KindTemplate, Module: module,
					Content: string(f.Source[b.start:b.end]), StartLine: start, EndLine: end, Exported: true,
				})
				continue
			}
			// Leading newlines keep tree-sitter rows equal to .vue file rows.
			src := append(bytes.Repeat([]byte{'\n'}, bytes.Count(f.Source[:b.start], []byte{'\n'})), f.Source[b.start:b.end]...)
			c, tree, err := scanScript(b.grammar, src, f.Root, module, dir, component)
			if err != nil {
				return nil, err
			}
			if tree != nil {
				defer tree.Release()
				scripts = append(scripts, script{b, c, tree})
			}
		}
		// <script> and <script setup> are one module: each sees the other's names.
		for _, s := range scripts {
			for _, o := range scripts {
				for k, v := range o.c.imports {
					if s.c.imports[k] == "" && s.c.defs[k] == "" {
						s.c.imports[k], s.c.picked[k] = v, o.c.picked[k]
					}
				}
				for k, v := range o.c.defs {
					if s.c.imports[k] == "" && s.c.defs[k] == "" {
						s.c.defs[k] = v
					}
				}
			}
		}
		var setup *parser.Symbol // the component: top-level statements of <script setup>
		for _, sc := range scripts {
			out, rels := sc.c.emit(sc.tree)
			syms = append(syms, out...)
			if sc.b.setup && len(rels) > 0 {
				start, end := lineSpan(f.Source, sc.b.start, sc.b.end)
				setup = &parser.Symbol{Name: "default", Kind: parser.KindFunction, Module: module, Exported: true,
					Content: string(f.Source[sc.b.start:sc.b.end]), StartLine: start, EndLine: end, Relations: rels}
			}
		}
		if setup != nil {
			syms = appendDefault(syms, *setup)
		}
		if tplAt >= 0 {
			var c *fileCtx // every block's names are shared, so any one resolves
			if len(scripts) > 0 {
				c = scripts[0].c
			}
			syms[tplAt].Relations = templateRels(f.Source, tpl, c, syms, component)
		}
		return parser.Finalize(f, syms), nil
	}
	grammar := "javascript"
	switch strings.ToLower(filepath.Ext(f.Path)) {
	case ".ts":
		grammar = "typescript"
	case ".tsx":
		grammar = "tsx"
	}
	// shortcut: top-level expression statements of a plain script (describe/it in tests) stay unattributed.
	syms, _, err := analyzeScript(grammar, f.Source, f.Root, module, dir, component)
	if err != nil {
		return nil, err
	}
	return parser.Finalize(f, syms), nil
}

// appendDefault adds the <script setup> component symbol, or its relations to
// the file's existing "default" symbol.
func appendDefault(syms []parser.Symbol, d parser.Symbol) []parser.Symbol {
	for i := range syms {
		if syms[i].Name == "default" && syms[i].Owner == "" {
			syms[i].Relations = append(syms[i].Relations, d.Relations...)
			return syms
		}
	}
	return append(syms, d)
}

// analyzeScript returns the symbols of one script and the relations of its
// top-level expression statements (watch(...), defineProps<T>()), which no
// symbol owns.
func analyzeScript(grammar string, src []byte, wsRoot, module, dir, component string) ([]parser.Symbol, []parser.Relation, error) {
	c, tree, err := scanScript(grammar, src, wsRoot, module, dir, component)
	if err != nil || tree == nil {
		return nil, nil, err
	}
	defer tree.Release()
	syms, rels := c.emit(tree)
	return syms, rels, nil
}

// scanScript parses one script and runs pass 1: imports, top-level names and
// export lists, so calls resolve forward references. The caller releases
// the tree.
func scanScript(grammar string, src []byte, wsRoot, module, dir, component string) (*fileCtx, *gotreesitter.Tree, error) {
	if unbalanced(src) {
		// an error, so the file counts as failed instead of indexed as empty
		return nil, nil, fmt.Errorf("parser: %s: not parsed: more than %d unclosed brackets", grammar, maxUnclosed)
	}
	tree, lang, err := parser.ParseTree(grammar, src)
	if err != nil {
		return nil, nil, err
	}
	c := newCtx(src, lang, wsRoot, module, dir, component)
	for _, nc := range parser.NamedChildren(tree.RootNode()) {
		c.scan(nc)
	}
	if c.classic = !c.esm; c.classic {
		for _, nc := range parser.NamedChildren(tree.RootNode()) {
			c.wrapped(nc, 0, c.scan)
		}
	}
	return c, tree, nil
}

// maxWrap is how deep wrappers nest (an IIFE holding a ready callback ...)
// before their statements stop counting as top-level ones.
const maxWrap = 3

// wrapped calls fn on each statement of the function bodies a top-level
// expression statement wraps, as classic browser scripts do: an IIFE
// `(function () {...})()` / `!function () {...}()`, or a function argument
// `$(function () {...})`, `addEventListener('DOMContentLoaded', () => {...})`,
// and on the statements of wrappers inside those, up to maxWrap levels. A
// module (imports, require, exports) is not descended: there wrappers are
// test suites (`describe(() => {...})`) and hooks, whose locals are no API.
func (c *fileCtx) wrapped(n *gotreesitter.Node, depth int, fn func(*gotreesitter.Node)) {
	if !c.classic || depth >= maxWrap || n.Type(c.lang) != "expression_statement" || n.NamedChildCount() == 0 {
		return
	}
	e := c.unwrap(n.NamedChild(0))
	if e.Type(c.lang) != "call_expression" {
		return
	}
	funcs := []*gotreesitter.Node{c.unwrap(parser.Field(e, c.lang, "function"))}
	funcs = append(funcs, parser.NamedChildren(parser.Field(e, c.lang, "arguments"))...)
	for _, f := range funcs {
		if f == nil || !isFunc(f.Type(c.lang)) {
			continue
		}
		if body := parser.Field(f, c.lang, "body"); body != nil && body.Type(c.lang) == "statement_block" {
			for _, st := range parser.NamedChildren(body) {
				fn(st)
				c.wrapped(st, depth+1, fn)
			}
		}
	}
}

// unwrap strips the parentheses and unary operators around an IIFE.
func (c *fileCtx) unwrap(e *gotreesitter.Node) *gotreesitter.Node {
	for e != nil && (e.Type(c.lang) == "parenthesized_expression" || e.Type(c.lang) == "unary_expression") && e.NamedChildCount() > 0 {
		e = e.NamedChild(e.NamedChildCount() - 1)
	}
	return e
}

// emit runs pass 2 over a scanned script: its symbols and the relations of
// its top-level expression statements.
func (c *fileCtx) emit(tree *gotreesitter.Tree) ([]parser.Symbol, []parser.Relation) {
	root := tree.RootNode()
	var syms []parser.Symbol
	var rels []parser.Relation
	for _, nc := range parser.NamedChildren(root) {
		c.topLevel(nc, &syms)
		c.wrapped(nc, 0, func(st *gotreesitter.Node) {
			var in []parser.Symbol
			c.topLevel(st, &in)
			for _, s := range in {
				// Locals of a wrapper (`var form = ...`, a test's `const wrapper = mount()`) are no
				// symbols; shortcut: a name of one or two letters there is minified code, not indexed.
				if s.Kind != parser.KindVariable && s.Kind != parser.KindConstant && len(s.Name) > 2 {
					syms = append(syms, s)
				}
			}
		})
		switch nc.Type(c.lang) {
		case "expression_statement":
			rels = append(rels, c.relsUnder(nc, c.locals(nc), "")...)
		case "lexical_declaration", "variable_declaration":
			// const { a } = useX(): a pattern makes no symbol, so its calls are top-level ones.
			for _, d := range parser.NamedChildren(nc) {
				if nm, v := parser.Field(d, c.lang, "name"), parser.Field(d, c.lang, "value"); nm != nil && v != nil && nm.Type(c.lang) != "identifier" {
					rels = append(rels, c.relsUnder(v, c.locals(v), "")...)
				}
			}
		}
	}
	return syms, rels
}

// maxUnclosed is the most brackets a file may leave open before it is
// treated as garbage: tree-sitter error recovery is quadratic in the number
// of lines with an unclosed "(", so a 100 KB file of them takes minutes.
const maxUnclosed = 256

// unbalanced reports whether src ends with more than maxUnclosed brackets
// left open, in one linear pass that skips strings and comments. Deep but
// closed nesting is valid code and parses fast.
// shortcut: regex literals are not skipped; a "(" inside one counts as open.
func unbalanced(src []byte) bool {
	depth := 0
	for i := 0; i < len(src); i++ {
		switch c := src[i]; c {
		case '\\':
			i++
		case '\'', '"', '`':
			for i++; i < len(src) && src[i] != c; i++ {
				if src[i] == '\\' {
					i++
				}
			}
		case '/':
			if i+1 < len(src) && src[i+1] == '/' {
				for i += 2; i < len(src) && src[i] != '\n'; i++ {
				}
			} else if i+1 < len(src) && src[i+1] == '*' {
				end := bytes.Index(src[i+2:], []byte("*/"))
				if end < 0 {
					return false
				}
				i += 2 + end + 1
			}
		case '(', '[', '{':
			depth++
		case ')', ']', '}':
			if depth > 0 {
				depth--
			}
		}
	}
	return depth > maxUnclosed
}

// moduleName derives the module (root-relative path without extension) and
// its directory. Files outside the root use their stem.
func moduleName(f parser.File) (string, string) {
	mod, ok := parser.RelModule(f.Root, f.Path)
	if !ok {
		return mod, "."
	}
	return mod, path.Dir(mod)
}

// pascal turns "my-comp.spec" into "MyCompSpec".
func pascal(s string) string {
	var b strings.Builder
	up := true
	for _, r := range s {
		if r == '-' || r == '_' || r == '.' || r == ' ' {
			up = true
			continue
		}
		if up {
			r = unicode.ToUpper(r)
			up = false
		}
		b.WriteRune(r)
	}
	return b.String()
}

// ---- vue ----

type vueBlock struct {
	start, end int
	grammar    string
	template   bool
	setup      bool // <script setup>
}

// vueBlocks finds every top-level <script> body and the outermost <template>
// in a .vue file. shortcut: plain string scanning; a nested <template> inside
// the template body is swallowed by the outer one, which is what we want.
func vueBlocks(src []byte) []vueBlock {
	var out []vueBlock
	s := string(src)
	for pos := 0; ; {
		i := strings.Index(s[pos:], "<script")
		if i < 0 {
			break
		}
		open := pos + i
		gt := strings.IndexByte(s[open:], '>')
		if gt < 0 {
			break
		}
		attrs := s[open+7 : open+gt]
		body := open + gt + 1
		end := strings.Index(s[body:], "</script")
		if end < 0 {
			break
		}
		grammar := "javascript"
		if strings.Contains(attrs, `lang="ts"`) || strings.Contains(attrs, "lang='ts'") || strings.Contains(attrs, `lang="tsx"`) {
			grammar = "typescript"
		}
		out = append(out, vueBlock{start: body, end: body + end, grammar: grammar, setup: strings.Contains(attrs, "setup")})
		pos = body + end
	}
	if i := strings.Index(s, "<template"); i >= 0 {
		if j := strings.LastIndex(s, "</template>"); j > i {
			out = append(out, vueBlock{start: i, end: j + len("</template>"), template: true})
		}
	}
	return out
}

var (
	// reVueTag is a component tag: PascalCase (`<StatusCard`, `<ns.Comp`) or kebab-case with a dash (`<status-card`).
	reVueTag = regexp.MustCompile(`<([A-Z][\w]*(?:\.[A-Z]\w*)?|[a-z][a-z0-9]*(?:-[a-z0-9]+)+)[\s/>]`)
	// reVueExpr is a template expression: an event, bound or directive attribute value, or a {{ mustache }}.
	reVueExpr = regexp.MustCompile(`\s(@[\w.:-]+|v-on:[\w.:-]+|:[\w.:-]+|v-bind:[\w.:-]+|v-(?:if|else-if|show|for|html|text))\s*=\s*"([^"]*)"|\{\{((?s:.*?))\}\}`)
	reVueCall = regexp.MustCompile(`(?:^|[^\w$.])([A-Za-z_$][\w$]*(?:\.[A-Za-z_$][\w$]*)*)\s*\(`)
	reVueName = regexp.MustCompile(`^\s*([A-Za-z_$][\w$]*(?:\.[A-Za-z_$][\w$]*)*)\s*$`)
	// reHTMLComment is an HTML comment of the template: commented-out markup is not used.
	reHTMLComment = regexp.MustCompile(`(?s)<!--.*?-->`)
)

// blank replaces every match of re in b with spaces, keeping newlines so line numbers stay.
func blank(re *regexp.Regexp, b []byte) []byte {
	return re.ReplaceAllFunc(b, func(m []byte) []byte {
		return bytes.Map(func(r rune) rune {
			if r == '\n' {
				return r
			}
			return ' '
		}, m)
	})
}

// templateRels lists what a Vue <template> uses: component tags (uses_type)
// and the calls in its expressions, a bare event handler name (`@click="go"`)
// counting as a call. Names resolve through the script blocks (c, nil
// without one) or to an options-API method of the component.
// shortcut: regex over the template text, like the block scanner; v-for and slot locals are not tracked.
func templateRels(src []byte, b vueBlock, c *fileCtx, syms []parser.Symbol, component string) []parser.Relation {
	text := blank(reHTMLComment, src[b.start:b.end])
	base := bytes.Count(src[:b.start], []byte{'\n'}) + 1
	lineAt := func(off int) int { return base + bytes.Count(text[:off], []byte{'\n'}) }
	resolve := func(dotted string) string {
		if c != nil {
			if qn := c.resolveDotted(dotted); qn != "" {
				return qn
			}
		}
		for _, s := range syms {
			if s.Owner == component && s.Kind == parser.KindMethod && s.Name == dotted {
				return s.QualifiedName
			}
		}
		return ""
	}
	var rels []parser.Relation
	for _, m := range reVueTag.FindAllSubmatchIndex(text, -1) {
		name := string(text[m[2]:m[3]])
		if !strings.Contains(name, ".") {
			name = pascal(name)
		}
		qn := ""
		if c != nil {
			qn = c.resolveDotted(name)
		}
		rels = append(rels, parser.Relation{Kind: parser.RelUsesType, TargetName: name[strings.LastIndex(name, ".")+1:], TargetQualifiedName: qn, Line: lineAt(m[0])})
	}
	for _, m := range reVueExpr.FindAllSubmatchIndex(text, -1) {
		from, to := m[4], m[5]
		if from < 0 {
			from, to = m[6], m[7]
		}
		expr := text[from:to]
		var called [][]int // offsets of each called name in expr
		event := m[2] >= 0 && (text[m[2]] == '@' || bytes.HasPrefix(text[m[2]:m[3]], []byte("v-on:")))
		if n := reVueName.FindSubmatchIndex(expr); event && n != nil {
			called = append(called, n[2:4]) // @click="go": the handler itself
		}
		for _, cm := range reVueCall.FindAllSubmatchIndex(expr, -1) {
			called = append(called, cm[2:4])
		}
		for _, d := range called {
			dotted := string(expr[d[0]:d[1]])
			rels = append(rels, parser.Relation{Kind: parser.RelCall, TargetName: dotted[strings.LastIndex(dotted, ".")+1:], TargetQualifiedName: resolve(dotted), Line: lineAt(from + d[0])})
		}
	}
	return rels
}

func lineSpan(src []byte, start, end int) (int, int) {
	return bytes.Count(src[:start], []byte{'\n'}) + 1, bytes.Count(src[:end], []byte{'\n'}) + 1
}

// ---- pass 1: imports, definitions, export lists ----

func (c *fileCtx) scan(n *gotreesitter.Node) {
	switch n.Type(c.lang) {
	case "import_statement":
		c.esm = true
		c.addImport(n)
	case "export_statement":
		c.esm = true
		if d := parser.Field(n, c.lang, "declaration"); d != nil {
			if c.isDefault(n) { // export default function Button() {}
				c.def = parser.Text(parser.Field(d, c.lang, "name"), c.src)
				c.anonDef = c.def == ""
			}
			c.scan(d)
		}
		if v := parser.Field(n, c.lang, "value"); v != nil {
			if v.Type(c.lang) == "identifier" {
				c.def = parser.Text(v, c.src) // export default App;
				c.exported[c.def] = true
				c.exportAs["default"] = c.def
			} else {
				c.anonDef = true // export default {...} / defineX(...)
				c.addProps("default", v)
			}
		}
		from := ""
		if s := parser.Field(n, c.lang, "source"); s != nil {
			spec := unquote(parser.Text(s, c.src))
			from = c.resolveSpec(spec)
			if isPackage(from, spec) { // a package ('react'): never read as a workspace file
				c.bare[from] = true
			}
			if n.NamedChildCount() == 1 && !c.bare[from] { // export * from './x'; shortcut: export * as ns stays unresolved
				c.stars = append(c.stars, from)
			}
		}
		for _, nc := range parser.NamedChildren(n) {
			if cl := nc; cl.Type(c.lang) == "export_clause" {
				for _, nc := range parser.NamedChildren(cl) {
					name := parser.Text(parser.Field(nc, c.lang, "name"), c.src)
					alias := or(parser.Text(parser.Field(nc, c.lang, "alias"), c.src), name)
					if from != "" { // export { a as b } from './x'
						c.reexp[alias] = from + "." + name
						continue
					}
					c.exported[name] = true
					c.exportAs[alias] = name
					if alias == "default" {
						c.def = name // export { App as default }
					}
				}
			}
		}
	case "ambient_declaration":
		if n.NamedChildCount() > 0 {
			c.scan(n.NamedChild(0))
		}
	case "function_declaration", "generator_function_declaration", "function_signature":
		c.defs[parser.Text(parser.Field(n, c.lang, "name"), c.src)] = parser.KindFunction
	case "class_declaration", "abstract_class_declaration", "enum_declaration":
		c.defs[parser.Text(parser.Field(n, c.lang, "name"), c.src)] = parser.KindClass
	case "interface_declaration":
		c.defs[parser.Text(parser.Field(n, c.lang, "name"), c.src)] = parser.KindInterface
	case "type_alias_declaration":
		c.defs[parser.Text(parser.Field(n, c.lang, "name"), c.src)] = parser.KindType
	case "lexical_declaration", "variable_declaration":
		for _, d := range parser.NamedChildren(n) {
			name, value := parser.Field(d, c.lang, "name"), parser.Field(d, c.lang, "value")
			if spec := c.requireSpec(value); spec != "" {
				c.esm = true
				c.addRequire(name, c.resolveSpec(spec))
				continue
			}
			if name != nil && name.Type(c.lang) == "identifier" {
				c.defs[parser.Text(name, c.src)] = parser.KindVariable
				c.addProps(parser.Text(name, c.src), value)
			}
		}
	case "expression_statement":
		if n.NamedChildCount() > 0 {
			c.scanExportsAssign(n.NamedChild(0))
		}
	}
}

func (c *fileCtx) addImport(n *gotreesitter.Node) {
	src := parser.Field(n, c.lang, "source")
	if src == nil {
		return
	}
	spec := unquote(parser.Text(src, c.src))
	m := c.resolveSpec(spec)
	follow, pkg := c.follow, isPackage(m, spec)
	if pkg { // a package ('react') or an unresolved path: never read as a workspace file
		follow = func(m, name string) string { return m + "." + name }
	}
	parser.Walk(n, func(ch *gotreesitter.Node) bool {
		switch ch.Type(c.lang) {
		case "import_clause":
			if ch.NamedChildCount() > 0 && ch.NamedChild(0).Type(c.lang) == "identifier" {
				local := parser.Text(ch.NamedChild(0), c.src)
				c.imports[local], c.picked[local] = follow(m, "default"), !pkg
			}
		case "namespace_import":
			local := parser.Text(ch.NamedChild(0), c.src)
			c.imports[local], c.picked[local] = m, false
		case "import_specifier":
			name := parser.Text(parser.Field(ch, c.lang, "name"), c.src)
			local := name
			if a := parser.Field(ch, c.lang, "alias"); a != nil {
				local = parser.Text(a, c.src)
			} else if ch.NamedChildCount() > 1 { // older grammars: name, alias as plain children
				local = parser.Text(ch.NamedChild(1), c.src)
			}
			c.imports[local], c.picked[local] = follow(m, name), !pkg
		}
		return true
	})
}

// requireSpec returns the module specifier of a require('x') call, "" otherwise.
func (c *fileCtx) requireSpec(v *gotreesitter.Node) string {
	if v == nil || v.Type(c.lang) != "call_expression" || parser.Text(parser.Field(v, c.lang, "function"), c.src) != "require" {
		return ""
	}
	args := parser.Field(v, c.lang, "arguments")
	if args == nil || args.NamedChildCount() != 1 || args.NamedChild(0).Type(c.lang) != "string" {
		return ""
	}
	return unquote(parser.Text(args.NamedChild(0), c.src))
}

// addRequire binds `const x = require(m)` to m and `const {a, b: c} = require(m)` to m.a, m.b.
func (c *fileCtx) addRequire(pattern *gotreesitter.Node, m string) {
	switch pattern.Type(c.lang) {
	case "identifier":
		c.imports[parser.Text(pattern, c.src)], c.picked[parser.Text(pattern, c.src)] = m, false
	case "object_pattern":
		for _, p := range parser.NamedChildren(pattern) {
			switch p.Type(c.lang) {
			case "shorthand_property_identifier_pattern":
				c.imports[parser.Text(p, c.src)], c.picked[parser.Text(p, c.src)] = m+"."+parser.Text(p, c.src), false
			case "pair_pattern":
				key, val := parser.Field(p, c.lang, "key"), parser.Field(p, c.lang, "value")
				if val != nil && val.Type(c.lang) == "identifier" {
					c.imports[parser.Text(val, c.src)], c.picked[parser.Text(val, c.src)] = m+"."+parser.Text(key, c.src), false
				}
			}
		}
	}
}

// addProps records the shorthand (`{ f }`) and `key: ident` members of a
// top-level object literal bound to obj, which the barrel turns into
// re-exports of the names they point to. For the inline `export default {...}`
// it also records its methods (value ""), emitted owned by the component.
func (c *fileCtx) addProps(obj string, o *gotreesitter.Node) {
	if o == nil || o.Type(c.lang) != "object" {
		return
	}
	for _, p := range parser.NamedChildren(o) {
		switch p.Type(c.lang) {
		case "shorthand_property_identifier":
			c.props[obj+"."+parser.Text(p, c.src)] = parser.Text(p, c.src)
		case "pair":
			k, v := parser.Field(p, c.lang, "key"), parser.Field(p, c.lang, "value")
			switch {
			case k == nil || v == nil || k.Type(c.lang) != "property_identifier":
			case v.Type(c.lang) == "identifier":
				c.props[obj+"."+parser.Text(k, c.src)] = parser.Text(v, c.src)
			case obj == "default" && isFunc(v.Type(c.lang)):
				c.props[obj+"."+parser.Text(k, c.src)] = ""
			}
		case "method_definition":
			if k := parser.Field(p, c.lang, "name"); obj == "default" && k != nil && k.Type(c.lang) == "property_identifier" {
				c.props[obj+"."+parser.Text(k, c.src)] = ""
			}
		}
	}
}

// scanExportsAssign records CommonJS exports: module.exports = f | { f, g: h },
// exports.x = ..., module.exports.x = ....
func (c *fileCtx) scanExportsAssign(n *gotreesitter.Node) {
	if n.Type(c.lang) != "assignment_expression" {
		return
	}
	left := parser.Text(parser.Field(n, c.lang, "left"), c.src)
	right := parser.Field(n, c.lang, "right")
	if left == "module.exports" || strings.HasPrefix(left, "exports.") || strings.HasPrefix(left, "module.exports.") {
		c.esm = true
	}
	switch {
	case left == "module.exports" && right != nil && right.Type(c.lang) == "identifier":
		c.exported[parser.Text(right, c.src)] = true
	case left == "module.exports" && right != nil && right.Type(c.lang) == "object":
		for _, p := range parser.NamedChildren(right) {
			switch p.Type(c.lang) {
			case "shorthand_property_identifier":
				c.exported[parser.Text(p, c.src)] = true
			case "pair":
				if v := parser.Field(p, c.lang, "value"); v != nil && v.Type(c.lang) == "identifier" {
					c.exported[parser.Text(v, c.src)] = true
				}
			}
		}
	case strings.HasPrefix(left, "exports.") || strings.HasPrefix(left, "module.exports."):
		c.exported[left[strings.LastIndex(left, ".")+1:]] = true
		if right != nil && right.Type(c.lang) == "identifier" {
			c.exported[parser.Text(right, c.src)] = true
		}
	case windowProp(left) != "": // a browser global: window.f = f | function () {}
		c.exported[windowProp(left)] = true
		if right != nil && right.Type(c.lang) == "identifier" {
			c.exported[parser.Text(right, c.src)] = true
		} else if right != nil && isFunc(right.Type(c.lang)) && c.defs[windowProp(left)] == "" {
			c.defs[windowProp(left)] = parser.KindFunction
		}
	}
}

// windowProp is x for "window.x", else "".
func windowProp(left string) string {
	if p, ok := strings.CutPrefix(left, "window."); ok && p != "" && !strings.ContainsAny(p, ".[") {
		return p
	}
	return ""
}

// resolveSpec maps an import specifier to a module: relative paths are joined
// to this file's directory and stripped of their extension, and a directory
// with no sibling file of that name becomes its index module; aliases
// ('@/lib/x', '~/composables/y') resolve to the file they name; bare
// specifiers ('react', '@scope/pkg') stay as they are, under node_modules/
// when a root file has that name: its module must not be the package's.
func (c *fileCtx) resolveSpec(spec string) string {
	if strings.HasPrefix(spec, "/") && !strings.HasPrefix(spec, "//") {
		return c.rootAbsolute(spec)
	}
	if spec != "." && spec != ".." && !strings.HasPrefix(spec, "./") && !strings.HasPrefix(spec, "../") {
		if m := c.aliasSpec(spec); m != "" {
			return m
		}
		if c.root != "" && hasSource(filepath.Join(c.root, filepath.FromSlash(spec))) {
			return pkgDir + spec
		}
		return spec
	}
	return c.modulePath(path.Join(c.dir, spec))
}

// pkgDir prefixes a package module that a root file's module would equal.
const pkgDir = "node_modules/"

// isPackage reports whether resolveSpec left spec as a package (or an
// unresolved path) that must never be read from the workspace.
func isPackage(m, spec string) bool { return m == spec || m == pkgDir+spec }

// modulePath strips a source extension off a root-relative path and turns a
// directory with no sibling file of that name into its index module.
func (c *fileCtx) modulePath(p string) string {
	switch strings.ToLower(path.Ext(p)) {
	case ".js", ".mjs", ".cjs", ".jsx", ".ts", ".tsx", ".vue", ".json":
		p = strings.TrimSuffix(p, path.Ext(p))
	}
	if abs := filepath.Join(c.root, filepath.FromSlash(p)); c.root != "" && isDir(abs) && !hasSource(abs) {
		p += "/index"
	}
	return p
}

// rootAbsolute maps "/js/api.js" to the file under the web root the page is
// served from, unknown here: the nearest ancestor of this file's directory
// that has it. Not found: the specifier as written.
func (c *fileCtx) rootAbsolute(spec string) string {
	if c.root == "" {
		return spec
	}
	for dir := c.dir; ; dir = path.Dir(dir) {
		p := c.modulePath(path.Join(dir, spec))
		if abs := filepath.Join(c.root, filepath.FromSlash(p)); hasSource(abs) {
			return p
		}
		if dir == "." || dir == "/" || dir == "" {
			return spec
		}
	}
}

func isDir(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && fi.IsDir()
}

// hasSource reports whether base.<ext> exists, which wins over base/index.
func hasSource(base string) bool {
	for _, ext := range append(analyzer{}.Extensions(), ".json") {
		if _, err := os.Stat(base + ext); err == nil {
			return true
		}
	}
	return false
}

// isDefault reports whether an export statement is `export default ...`.
func (c *fileCtx) isDefault(n *gotreesitter.Node) bool {
	for i := 0; i < n.ChildCount(); i++ {
		if n.Child(i).Type(c.lang) == "default" {
			return true
		}
	}
	return false
}

// qual is the name a top-level declaration is qualified by: importers see the
// default export only as "default", so that is its qualified name too.
func (c *fileCtx) qual(name string) string {
	if name != "" && name == c.def {
		return "default"
	}
	return name
}

func unquote(s string) string {
	if len(s) >= 2 {
		return s[1 : len(s)-1]
	}
	return s
}

// ---- pass 2: symbols ----

// topLevel emits the symbols of one top-level statement. decl is the node
// whose source becomes Content (the export statement when there is one).
func (c *fileCtx) topLevel(n *gotreesitter.Node, out *[]parser.Symbol) {
	decl, exported := n, false
	if n.Type(c.lang) == "export_statement" {
		exported = true
		if d := parser.Field(n, c.lang, "declaration"); d != nil {
			n = d
		} else if v := parser.Field(n, c.lang, "value"); v != nil {
			n = v
		} else {
			return
		}
	}
	for n.Type(c.lang) == "ambient_declaration" && n.NamedChildCount() > 0 {
		n = n.NamedChild(0)
	}
	name := parser.Text(parser.Field(n, c.lang, "name"), c.src)
	switch n.Type(c.lang) {
	case "function_declaration", "generator_function_declaration", "function_signature", "function_expression", "arrow_function":
		s := c.symbol(n, decl, "", or(name, "default"), parser.KindFunction, exported)
		s.Relations = c.calls(n, "")
		*out = append(*out, s)
	case "class_declaration", "abstract_class_declaration", "class":
		c.class(n, decl, or(name, "default"), exported, out)
	case "interface_declaration":
		s := c.symbol(n, decl, "", name, parser.KindInterface, exported)
		if ext := child(n, c.lang, "extends_type_clause"); ext != nil {
			line, _ := parser.Lines(n)
			for _, nc := range parser.NamedChildren(ext) {
				s.Relations = append(s.Relations, c.typeRel(parser.RelExtends, parser.Text(nc, c.src), line))
			}
		}
		s.Relations = append(s.Relations, c.typeUses(parser.Field(n, c.lang, "body"))...)
		*out = append(*out, s)
	case "type_alias_declaration":
		s := c.symbol(n, decl, "", name, parser.KindType, exported)
		s.Relations = c.typeUses(parser.Field(n, c.lang, "value"))
		*out = append(*out, s)
	case "enum_declaration":
		*out = append(*out, c.symbol(n, decl, "", name, parser.KindClass, exported))
	case "lexical_declaration", "variable_declaration":
		c.variables(n, decl, exported, out)
	case "object":
		c.componentObject(n, out)
	case "call_expression":
		args := parser.Field(n, c.lang, "arguments")
		if obj := defineComponentArg(c, n); obj != nil {
			c.componentObject(obj, out)
		} else if decl != n && c.isDefault(decl) {
			// export default defineNuxtRouteMiddleware(() => ...): the callback's calls.
			kind := parser.KindConstant
			for _, a := range parser.NamedChildren(args) {
				if isFunc(a.Type(c.lang)) {
					kind = parser.KindFunction
				}
			}
			s := c.symbol(n, decl, "", "default", kind, true)
			s.Relations = c.relsUnder(n, c.locals(n), "")
			*out = append(*out, s)
		}
	case "expression_statement":
		if n.NamedChildCount() > 0 {
			c.exportsAssign(n.NamedChild(0), decl, out)
		}
	}
}

// symbol builds the common fields. body nil means the header is the first line.
func (c *fileCtx) symbol(n, decl *gotreesitter.Node, owner, name string, kind parser.Kind, exported bool) parser.Symbol {
	start, end := parser.Lines(decl)
	qn := parser.Qualify(c.module, c.qual(owner), name)
	if owner == "" {
		qn = parser.Qualify(c.module, "", c.qual(name))
	}
	return parser.Symbol{
		Name: name, Kind: kind, Module: c.module, Owner: owner, QualifiedName: qn,
		Signature: c.header(decl, parser.Field(n, c.lang, "body")),
		Doc:       c.doc(decl),
		Content:   parser.Text(decl, c.src),
		StartLine: start, EndLine: end,
		Exported: exported || c.exported[name],
	}
}

func (c *fileCtx) class(n, decl *gotreesitter.Node, name string, exported bool, out *[]parser.Symbol) {
	s := c.symbol(n, decl, "", name, parser.KindClass, exported)
	line, _ := parser.Lines(n)
	if h := child(n, c.lang, "class_heritage"); h != nil {
		for _, cl := range parser.NamedChildren(h) {
			switch cl.Type(c.lang) {
			case "extends_clause": // typescript: extends_clause(value)
				s.Relations = append(s.Relations, c.typeRel(parser.RelExtends, parser.Text(parser.Field(cl, c.lang, "value"), c.src), line))
			case "implements_clause":
				for _, nc := range parser.NamedChildren(cl) {
					s.Relations = append(s.Relations, c.typeRel(parser.RelImplements, parser.Text(nc, c.src), line))
				}
			case "identifier", "member_expression": // javascript: bare expression
				s.Relations = append(s.Relations, c.typeRel(parser.RelExtends, parser.Text(cl, c.src), line))
			}
		}
	}
	*out = append(*out, s)
	body := parser.Field(n, c.lang, "body")
	for _, m := range parser.NamedChildren(body) {
		switch m.Type(c.lang) {
		case "method_definition", "abstract_method_signature":
			c.method(m, m, name, s.Exported, out)
		case "public_field_definition":
			if v := parser.Field(m, c.lang, "value"); v != nil && isFunc(v.Type(c.lang)) {
				c.method(m, v, name, s.Exported, out)
			}
		}
	}
}

// method emits one method; fn carries the body (the member itself or the
// function value of a field / object pair).
func (c *fileCtx) method(m, fn *gotreesitter.Node, owner string, ownerExported bool, out *[]parser.Symbol) {
	name := parser.Text(parser.Field(m, c.lang, "name"), c.src)
	if name == "" {
		name = parser.Text(parser.Field(m, c.lang, "key"), c.src)
	}
	private := strings.HasPrefix(name, "#") || child(m, c.lang, "accessibility_modifier") != nil && parser.Text(child(m, c.lang, "accessibility_modifier"), c.src) == "private"
	s := c.symbol(fn, m, owner, name, parser.KindMethod, ownerExported && !private)
	s.Exported = ownerExported && !private
	s.Relations = c.calls(fn, owner)
	*out = append(*out, s)
}

// componentObject emits the methods, computed and watch handlers of an
// options-API object (export default {...} / defineComponent({...})) as
// methods owned by the component, which is named after the file.
func (c *fileCtx) componentObject(obj *gotreesitter.Node, out *[]parser.Symbol) {
	c.objectMethods(obj, c.component, true, true, out)
}

// objectMethods emits the methods and function-valued pairs of obj as methods
// of owner; top descends into Vue's methods / computed / watch.
func (c *fileCtx) objectMethods(obj *gotreesitter.Node, owner string, top, exported bool, out *[]parser.Symbol) {
	for _, p := range parser.NamedChildren(obj) {
		switch p.Type(c.lang) {
		case "method_definition":
			c.method(p, p, owner, exported, out)
		case "pair":
			key, v := parser.Text(parser.Field(p, c.lang, "key"), c.src), parser.Field(p, c.lang, "value")
			switch {
			case v == nil:
			case isFunc(v.Type(c.lang)):
				c.method(p, v, owner, exported, out)
			case top && v.Type(c.lang) == "object" && (key == "methods" || key == "computed" || key == "watch"):
				c.objectMethods(v, owner, false, exported, out)
			}
		}
	}
}

// variables emits one symbol per declarator: functions for arrow/function
// values, constants for const, variables for let/var.
func (c *fileCtx) variables(n, decl *gotreesitter.Node, exported bool, out *[]parser.Symbol) {
	isConst := strings.HasPrefix(parser.Text(n, c.src), "const")
	for _, d := range parser.NamedChildren(n) {
		name, v := parser.Field(d, c.lang, "name"), parser.Field(d, c.lang, "value")
		if name == nil || name.Type(c.lang) != "identifier" || c.requireSpec(v) != "" {
			continue
		}
		kind := parser.KindVariable
		switch {
		case v != nil && isFunc(v.Type(c.lang)):
			kind = parser.KindFunction
		case isConst:
			kind = parser.KindConstant
		}
		s := c.symbol(v, decl, "", parser.Text(name, c.src), kind, exported)
		switch {
		case kind == parser.KindFunction:
			s.Relations = c.calls(v, "")
		case v != nil && v.Type(c.lang) != "object" && defineComponentArg(c, v) == nil:
			// const x = useX(...): the initializer's calls are the const's.
			// shortcut: an object literal's non-method values are not walked (its methods own their calls).
			s.Relations = c.relsUnder(v, c.locals(v), "")
		}
		s.Relations = append(s.Relations, c.typeUses(parser.Field(d, c.lang, "type"))...) // const c: Foo[] = ...
		*out = append(*out, s)
		if v != nil && v.Type(c.lang) == "object" { // const model = { load() {...} }: a store model
			c.objectMethods(v, s.Name, false, s.Exported, out)
		}
		if obj := defineComponentArg(c, v); obj != nil { // const C = defineComponent({...})
			c.objectMethods(obj, s.Name, true, s.Exported, out)
		}
	}
}

// exportsAssign emits `exports.x = ...` / `module.exports.x = ...` as symbols.
func (c *fileCtx) exportsAssign(n, decl *gotreesitter.Node, out *[]parser.Symbol) {
	if n.Type(c.lang) != "assignment_expression" {
		return
	}
	left := parser.Text(parser.Field(n, c.lang, "left"), c.src)
	if !strings.HasPrefix(left, "exports.") && !strings.HasPrefix(left, "module.exports.") && windowProp(left) == "" {
		return
	}
	name, v := left[strings.LastIndex(left, ".")+1:], parser.Field(n, c.lang, "right")
	if v != nil && v.Type(c.lang) == "identifier" && c.defs[parser.Text(v, c.src)] != "" {
		return // re-export of a declared name: scanExportsAssign already flagged it
	}
	kind := parser.KindVariable
	if v != nil && isFunc(v.Type(c.lang)) {
		kind = parser.KindFunction
	}
	s := c.symbol(v, decl, "", name, kind, true)
	if kind == parser.KindFunction {
		s.Relations = c.calls(v, "")
	}
	*out = append(*out, s)
}

// defineComponentArg is the options object of a defineComponent({...}) call, or nil.
func defineComponentArg(c *fileCtx, n *gotreesitter.Node) *gotreesitter.Node {
	if n == nil || n.Type(c.lang) != "call_expression" || parser.Text(parser.Field(n, c.lang, "function"), c.src) != "defineComponent" {
		return nil
	}
	if args := parser.Field(n, c.lang, "arguments"); args != nil && args.NamedChildCount() > 0 && args.NamedChild(0).Type(c.lang) == "object" {
		return args.NamedChild(0)
	}
	return nil
}

func isFunc(t string) bool {
	return t == "arrow_function" || t == "function_expression" || t == "generator_function" || t == "function"
}

func or(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

// child returns the first named child of the given type, or nil.
func child(n *gotreesitter.Node, lang *gotreesitter.Language, typ string) *gotreesitter.Node {
	for _, nc := range parser.NamedChildren(n) {
		if ch := nc; ch.Type(lang) == typ {
			return ch
		}
	}
	return nil
}

// header is the declaration up to the body, whitespace collapsed; without a
// body it is the first line of the declaration.
func (c *fileCtx) header(decl, body *gotreesitter.Node) string {
	start, end := decl.StartByte(), decl.EndByte()
	if body != nil && body.StartByte() > start && body.StartByte() <= end {
		end = body.StartByte()
	} else if nl := bytes.IndexByte(c.src[start:end], '\n'); nl >= 0 {
		end = start + uint32(nl)
	}
	h := strings.Join(strings.Fields(string(c.src[start:end])), " ")
	return strings.TrimSpace(strings.TrimRight(h, " ;"))
}

// doc returns the JSDoc or line comments directly above decl, markers removed.
func (c *fileCtx) doc(decl *gotreesitter.Node) string {
	var lines []string
	prev := decl.PrevSibling()
	for prev != nil && prev.Type(c.lang) == "comment" {
		// Only an adjacent comment belongs to the declaration.
		if int(prev.EndPoint().Row)+1 < int(decl.StartPoint().Row) {
			break
		}
		text := parser.Text(prev, c.src)
		if strings.HasPrefix(text, "/*") {
			lines = append(blockLines(text), lines...)
			break
		}
		lines = append([]string{strings.TrimSpace(strings.TrimPrefix(text, "//"))}, lines...)
		decl, prev = prev, prev.PrevSibling()
	}
	return strings.TrimSpace(strings.Join(lines, "\n"))
}

func blockLines(text string) []string {
	text = strings.TrimSuffix(strings.TrimPrefix(strings.TrimPrefix(text, "/**"), "/*"), "*/")
	var out []string
	for _, l := range strings.Split(text, "\n") {
		l = strings.TrimSpace(l)
		l = strings.TrimSpace(strings.TrimPrefix(l, "*"))
		if l != "" || len(out) > 0 {
			out = append(out, l)
		}
	}
	return out
}

// ---- relations ----

// calls collects calls, constructions and JSX component uses inside fn's body,
// including those in nested callbacks, attributed to the enclosing symbol.
// A callee whose first segment is a parameter or local of fn is unresolved.
func (c *fileCtx) calls(fn *gotreesitter.Node, owner string) []parser.Relation {
	rels := c.relsUnder(parser.Field(fn, c.lang, "body"), c.locals(fn), owner)
	rels = append(rels, c.typeUses(parser.Field(fn, c.lang, "parameters"))...)
	return append(rels, c.typeUses(parser.Field(fn, c.lang, "return_type"))...)
}

// relsUnder collects the call, construction, type and JSX relations under
// root; a callee whose first segment is in locals is unresolved.
func (c *fileCtx) relsUnder(root *gotreesitter.Node, locals map[string]bool, owner string) []parser.Relation {
	var rels []parser.Relation
	parser.Walk(root, func(n *gotreesitter.Node) bool {
		line, _ := parser.Lines(n)
		switch n.Type(c.lang) {
		case "call_expression":
			callee := parser.Field(n, c.lang, "function")
			if callee == nil {
				return true
			}
			if callee.Type(c.lang) == "await_expression" { // TS parses `await f<T>()` as (await f)<T>()
				if ch := parser.NamedChildren(callee); len(ch) == 1 {
					callee = ch[0]
				}
			}
			switch callee.Type(c.lang) {
			case "identifier":
				name := parser.Text(callee, c.src)
				qn := c.resolveDotted(name)
				if locals[name] {
					qn = ""
				}
				rels = append(rels, parser.Relation{Kind: parser.RelCall, TargetName: name, TargetQualifiedName: qn, Line: line})
			case "member_expression":
				prop := parser.Field(callee, c.lang, "property")
				obj, name := parser.Field(callee, c.lang, "object"), parser.Text(prop, c.src)
				line, _ := parser.Lines(prop) // a chain split over lines: the method name's line
				qn := c.resolveMember(obj, name, owner)
				if first, _, _ := strings.Cut(parser.Text(obj, c.src), "."); obj.Type(c.lang) != "this" && locals[first] {
					qn = ""
				}
				rels = append(rels, parser.Relation{Kind: parser.RelCall, TargetName: name, TargetQualifiedName: qn, Line: line})
			}
		case "new_expression":
			if ctor := parser.Field(n, c.lang, "constructor"); ctor != nil {
				rels = append(rels, c.typeRel(parser.RelUsesType, parser.Text(ctor, c.src), line))
			}
		case "type_identifier", "nested_type_identifier":
			rels = append(rels, c.typeUses(n)...)
			return false
		case "jsx_opening_element", "jsx_self_closing_element":
			if n.NamedChildCount() == 0 {
				return true
			}
			name := parser.Text(n.NamedChild(0), c.src)
			if r := []rune(name); len(r) > 0 && (unicode.IsUpper(r[0]) || strings.Contains(name, ".")) {
				rels = append(rels, c.typeRel(parser.RelUsesType, name, line))
			}
		}
		return true
	})
	return rels
}

// typeUses lists the TypeScript types named under n (annotations, generic
// arguments, interface and alias bodies) that an import or this file
// defines; type parameters and globals resolve to nothing and are dropped.
func (c *fileCtx) typeUses(n *gotreesitter.Node) []parser.Relation {
	var rels []parser.Relation
	parser.Walk(n, func(t *gotreesitter.Node) bool {
		switch t.Type(c.lang) {
		case "type_identifier", "nested_type_identifier":
			if text := parser.Text(t, c.src); c.resolveDotted(text) != "" {
				line, _ := parser.Lines(t)
				rels = append(rels, c.typeRel(parser.RelUsesType, text, line))
			}
			return false
		}
		return true
	})
	return rels
}

// locals returns the parameter and let/const/var names bound anywhere in fn,
// nested functions included.
// shortcut: one set for the whole function, so a local shadows an import everywhere in it, not only after its declaration.
func (c *fileCtx) locals(fn *gotreesitter.Node) map[string]bool {
	set := map[string]bool{}
	parser.Walk(fn, func(n *gotreesitter.Node) bool {
		switch n.Type(c.lang) {
		case "variable_declarator":
			c.bind(parser.Field(n, c.lang, "name"), set)
		case "function_declaration", "generator_function_declaration", "function_signature", "function_expression", "generator_function", "arrow_function", "method_definition", "method_signature":
			c.bind(parser.Field(n, c.lang, "parameters"), set)
			c.bind(parser.Field(n, c.lang, "parameter"), set) // arrow function without parentheses
		}
		return true
	})
	return set
}

// bind adds the names declared by a parameter list or binding pattern,
// skipping default values.
func (c *fileCtx) bind(n *gotreesitter.Node, set map[string]bool) {
	if n == nil {
		return
	}
	switch n.Type(c.lang) {
	case "identifier", "shorthand_property_identifier_pattern":
		set[parser.Text(n, c.src)] = true
	case "required_parameter", "optional_parameter":
		c.bind(parser.Field(n, c.lang, "pattern"), set)
	case "assignment_pattern", "object_assignment_pattern":
		c.bind(parser.Field(n, c.lang, "left"), set)
	case "pair_pattern":
		c.bind(parser.Field(n, c.lang, "value"), set)
	default:
		for _, ch := range parser.NamedChildren(n) {
			c.bind(ch, set)
		}
	}
}

// typeRel builds a relation to a type written as "A" or "ns.A", dropping
// generics such as "Base<T>".
func (c *fileCtx) typeRel(kind parser.RelationKind, text string, line int) parser.Relation {
	text, _, _ = strings.Cut(text, "<")
	text = strings.TrimSpace(text)
	return parser.Relation{Kind: kind, TargetName: text[strings.LastIndex(text, ".")+1:], TargetQualifiedName: c.resolveDotted(text), Line: line}
}

// resolveDotted resolves "a.b.c" through the import table or the file's own
// top-level names; "" when the first segment is unknown. A member of a local
// name resolves only when that name is a class or enum: the type behind a
// variable is never guessed.
func (c *fileCtx) resolveDotted(text string) string {
	first, rest, _ := strings.Cut(text, ".")
	switch {
	case c.imports[first] != "":
		if t := c.imports[first]; c.picked[first] && rest != "" && !strings.Contains(rest, ".") {
			// Jwt.getToken() of `export default { getToken }`: the member's own declaration.
			i := strings.LastIndex(t, ".")
			if i < 0 { // a picked name that is a re-exported module namespace
				return join(t, rest)
			}
			return c.follow(t[:i], t[i+1:]+"."+rest)
		}
		return join(c.imports[first], rest)
	case c.defs[first] != "" && (rest == "" || c.defs[first] == parser.KindClass):
		return join(parser.Qualify(c.module, "", c.qual(first)), rest)
	}
	return ""
}

// resolveMember resolves obj.name() for this (inside a class or component),
// imported bindings and names defined in this file. Unknown receivers give "".
func (c *fileCtx) resolveMember(obj *gotreesitter.Node, name, owner string) string {
	if obj == nil {
		return ""
	}
	switch obj.Type(c.lang) {
	case "this":
		if owner == "" {
			return ""
		}
		return parser.Qualify(c.module, c.qual(owner), name)
	case "identifier", "member_expression":
		if parser.Text(obj, c.src) == "window" && c.imports["window"] == "" && c.defs["window"] == "" {
			return c.resolveDotted(name) // window.f() of a global this file declares
		}
		return c.resolveDotted(parser.Text(obj, c.src) + "." + name)
	}
	return ""
}

func join(a, b string) string {
	if b == "" {
		return a
	}
	return a + "." + b
}

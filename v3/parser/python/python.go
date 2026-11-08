// Package python analyzes Python files with tree-sitter. Qualified names use
// the dotted module path relative to the workspace root; call targets are
// resolved through the file's own import table and never guessed.
package python

import (
	"maps"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/odvcencio/gotreesitter"

	"github.com/doITmagic/rag-code-mcp/v3/parser"
)

func init() { parser.Register(analyzer{}) }

type analyzer struct{}

func (analyzer) Language() string     { return "python" }
func (analyzer) Extensions() []string { return []string{".py", ".pyi"} }

// fileCtx is the per-call state: the source, the import table and the names
// defined at module level. It is built once and read by every symbol.
type fileCtx struct {
	src     []byte
	lang    *gotreesitter.Language
	module  string
	isPkg   bool              // file is __init__.py: module names the package itself
	imports map[string]string // local name -> qualified module or symbol
	defs    map[string]bool   // module-level function and class names
	root    string
	path    string
	reexp   map[string]map[string]string // package -> import table of its __init__.py
}

func (analyzer) Analyze(f parser.File) ([]parser.Symbol, error) {
	tree, lang, err := parser.ParseTree("python", f.Source)
	if err != nil {
		return nil, err
	}
	defer tree.Release()
	c := newCtx(f, lang)
	c.reexp = map[string]map[string]string{}
	root := tree.RootNode()
	c.scan(root)
	var syms []parser.Symbol
	c.collect(root, "", true, &syms)
	return parser.Finalize(f, syms), nil
}

func newCtx(f parser.File, lang *gotreesitter.Language) *fileCtx {
	c := &fileCtx{src: f.Source, lang: lang, root: f.Root, path: f.Path, imports: map[string]string{}, defs: map[string]bool{}}
	c.module, c.isPkg = moduleName(f)
	return c
}

// scan is pass 1: imports and module-level names, so calls can resolve
// forward references.
func (c *fileCtx) scan(root *gotreesitter.Node) {
	for _, nc := range parser.NamedChildren(root) {
		n := unwrapStmt(nc, c.lang)
		switch n.Type(c.lang) {
		case "import_statement":
			c.addImport(n)
		case "import_from_statement":
			c.addImportFrom(n)
		case "decorated_definition":
			if d := parser.Field(n, c.lang, "definition"); d != nil {
				c.defs[parser.Text(parser.Field(d, c.lang, "name"), c.src)] = true
			}
		case "function_definition", "class_definition":
			c.defs[parser.Text(parser.Field(n, c.lang, "name"), c.src)] = true
		case "if_statement", "try_statement", "with_statement":
			// if TYPE_CHECKING: / try: import x except: import y
			parser.Walk(n, func(m *gotreesitter.Node) bool {
				switch m.Type(c.lang) {
				case "import_statement":
					c.addImport(m)
				case "import_from_statement":
					c.addImportFrom(m)
				case "function_definition", "class_definition", "decorated_definition":
					return false
				}
				return true
			})
		}
	}
}

// moduleName derives the dotted module from the path relative to the root.
// Files outside the root use their stem.
func moduleName(f parser.File) (string, bool) {
	rel, ok := parser.RelModule(f.Root, f.Path)
	if !ok {
		return rel, false
	}
	mod := strings.ReplaceAll(rel, "/", ".")
	if mod == "__init__" {
		return "", true
	}
	if strings.HasSuffix(mod, ".__init__") {
		return strings.TrimSuffix(mod, ".__init__"), true
	}
	return mod, false
}

// unwrapStmt descends through an expression_statement wrapper when the
// grammar emits one.
func unwrapStmt(n *gotreesitter.Node, lang *gotreesitter.Language) *gotreesitter.Node {
	if n.Type(lang) == "expression_statement" && n.NamedChildCount() > 0 {
		return n.NamedChild(0)
	}
	return n
}

// ---- imports ----

func (c *fileCtx) addImport(n *gotreesitter.Node) {
	for _, ch := range parser.NamedChildren(n) {
		switch ch.Type(c.lang) {
		case "dotted_name": // import a.b -> a
			full := parser.Text(ch, c.src)
			first, _, _ := strings.Cut(full, ".")
			c.imports[first] = first
		case "aliased_import": // import a.b as c -> c
			name := parser.Text(parser.Field(ch, c.lang, "name"), c.src)
			alias := parser.Text(parser.Field(ch, c.lang, "alias"), c.src)
			if alias != "" {
				c.imports[alias] = name
			}
		}
	}
}

func (c *fileCtx) addImportFrom(n *gotreesitter.Node) {
	modNode := parser.Field(n, c.lang, "module_name")
	if modNode == nil {
		return
	}
	base := parser.Text(modNode, c.src)
	if modNode.Type(c.lang) == "relative_import" {
		base = c.resolveRelative(modNode)
	}
	for _, ch := range parser.NamedChildren(n) {
		if ch == modNode {
			continue
		}
		switch ch.Type(c.lang) {
		case "dotted_name":
			name := parser.Text(ch, c.src)
			c.imports[name] = join(base, name)
		case "aliased_import":
			name := parser.Text(parser.Field(ch, c.lang, "name"), c.src)
			alias := parser.Text(parser.Field(ch, c.lang, "alias"), c.src)
			if alias != "" {
				c.imports[alias] = join(base, name)
			}
		}
	}
}

// resolveRelative turns ".." + "m" into the absolute dotted module, walking up
// from this file's package.
func (c *fileCtx) resolveRelative(n *gotreesitter.Node) string {
	var level int
	var name string
	for _, ch := range parser.NamedChildren(n) {
		switch ch.Type(c.lang) {
		case "import_prefix":
			level = strings.Count(parser.Text(ch, c.src), ".")
		case "dotted_name":
			name = parser.Text(ch, c.src)
		}
	}
	parts := []string{}
	if c.module != "" {
		parts = strings.Split(c.module, ".")
	}
	drop := level
	if c.isPkg {
		drop--
	}
	if drop > len(parts) {
		drop = len(parts)
	}
	return join(strings.Join(parts[:len(parts)-drop], "."), name)
}

func join(a, b string) string {
	switch {
	case a == "":
		return b
	case b == "":
		return a
	}
	return a + "." + b
}

// ---- symbols ----

// collect walks the statements of a module or class body. owner is the
// dotted class path ("Outer.Inner"); top is true only for the module body.
func (c *fileCtx) collect(body *gotreesitter.Node, owner string, top bool, out *[]parser.Symbol) {
	for _, nc := range parser.NamedChildren(body) {
		n := unwrapStmt(nc, c.lang)
		decl := n
		var decorators []string
		if n.Type(c.lang) == "decorated_definition" {
			for _, nc := range parser.NamedChildren(n) {
				if d := nc; d.Type(c.lang) == "decorator" {
					decorators = append(decorators, strings.TrimPrefix(parser.Text(d, c.src), "@"))
				}
			}
			if n = parser.Field(n, c.lang, "definition"); n == nil {
				continue
			}
		}
		switch n.Type(c.lang) {
		case "function_definition":
			*out = append(*out, c.function(n, decl, owner, decorators))
		case "class_definition":
			*out = append(*out, c.class(n, decl, owner, decorators))
			if b := parser.Field(n, c.lang, "body"); b != nil {
				c.collect(b, join(owner, parser.Text(parser.Field(n, c.lang, "name"), c.src)), false, out)
			}
		case "assignment":
			if top {
				*out = append(*out, c.assignment(n)...)
			}
		}
	}
}

func (c *fileCtx) base(n, decl *gotreesitter.Node, owner, name string, kind parser.Kind, decorators []string) parser.Symbol {
	start, end := parser.Lines(decl)
	s := parser.Symbol{
		Name:      name,
		Kind:      kind,
		Module:    c.module,
		Owner:     owner,
		Signature: c.header(n),
		Doc:       c.docstring(parser.Field(n, c.lang, "body")),
		Content:   parser.Text(decl, c.src),
		StartLine: start,
		EndLine:   end,
		Exported:  !strings.HasPrefix(name, "_"),
	}
	if len(decorators) > 0 {
		s.Metadata = map[string]string{"decorators": strings.Join(decorators, ",")}
	}
	return s
}

func (c *fileCtx) function(n, decl *gotreesitter.Node, owner string, decorators []string) parser.Symbol {
	kind := parser.KindFunction
	if owner != "" {
		kind = parser.KindMethod
	}
	s := c.base(n, decl, owner, parser.Text(parser.Field(n, c.lang, "name"), c.src), kind, decorators)
	s.Relations = c.calls(n, owner)
	return s
}

func (c *fileCtx) class(n, decl *gotreesitter.Node, owner string, decorators []string) parser.Symbol {
	name := parser.Text(parser.Field(n, c.lang, "name"), c.src)
	s := c.base(n, decl, owner, name, parser.KindClass, decorators)
	line, _ := parser.Lines(n)
	sup := parser.Field(n, c.lang, "superclasses")
	for _, b := range parser.NamedChildren(sup) {
		for b != nil && b.Type(c.lang) == "subscript" { // Generic[T] -> Generic
			b = parser.Field(b, c.lang, "value")
		}
		if b == nil || b.Type(c.lang) == "keyword_argument" {
			continue
		}
		text := parser.Text(b, c.src)
		s.Relations = append(s.Relations, parser.Relation{
			Kind:                parser.RelExtends,
			TargetName:          text[strings.LastIndex(text, ".")+1:],
			TargetQualifiedName: c.resolveDotted(text),
			Line:                line,
		})
	}
	s.Relations = append(s.Relations, c.classCalls(parser.Field(n, c.lang, "body"))...)
	return s
}

// assignment yields one symbol per module-level target identifier.
func (c *fileCtx) assignment(n *gotreesitter.Node) []parser.Symbol {
	left := parser.Field(n, c.lang, "left")
	if left == nil {
		return nil
	}
	var names []string
	switch left.Type(c.lang) {
	case "identifier":
		names = []string{parser.Text(left, c.src)}
	case "pattern_list", "tuple_pattern":
		for _, nc := range parser.NamedChildren(left) {
			if id := nc; id.Type(c.lang) == "identifier" {
				names = append(names, parser.Text(id, c.src))
			}
		}
	}
	start, end := parser.Lines(n)
	sig, _, _ := strings.Cut(parser.Text(n, c.src), "=")
	var out []parser.Symbol
	for _, name := range names {
		kind := parser.KindVariable
		if isConstName(name) {
			kind = parser.KindConstant
		}
		out = append(out, parser.Symbol{
			Name: name, Kind: kind, Module: c.module,
			Signature: strings.TrimSpace(sig),
			Content:   parser.Text(n, c.src),
			StartLine: start, EndLine: end,
			Exported: !strings.HasPrefix(name, "_"),
		})
	}
	return out
}

// isConstName is true for UPPER_SNAKE names, leading underscores allowed.
func isConstName(name string) bool {
	upper := false
	for _, r := range strings.TrimLeft(name, "_") {
		switch {
		case unicode.IsUpper(r):
			upper = true
		case unicode.IsLower(r):
			return false
		}
	}
	return upper
}

// header is the declaration up to the body, whitespace collapsed, without
// the trailing colon: "async def f(x: int) -> str" / "class A(Base)".
func (c *fileCtx) header(n *gotreesitter.Node) string {
	end := n.EndByte()
	if b := parser.Field(n, c.lang, "body"); b != nil {
		end = b.StartByte()
	}
	for i := 0; i < n.ChildCount(); i++ { // the def's own colon: comments after it are body
		if ch := n.Child(i); ch.Type(c.lang) == ":" && ch.EndByte() <= end {
			end = ch.EndByte()
			break
		}
	}
	h := strings.Join(strings.Fields(string(c.src[n.StartByte():end])), " ")
	return strings.TrimSpace(strings.TrimSuffix(h, ":"))
}

// docstring returns the dedented docstring of a body, "" when absent.
func (c *fileCtx) docstring(body *gotreesitter.Node) string {
	if body == nil || body.NamedChildCount() == 0 {
		return ""
	}
	first := unwrapStmt(body.NamedChild(0), c.lang)
	if first.Type(c.lang) != "string" {
		return ""
	}
	var content string
	for _, nc := range parser.NamedChildren(first) {
		if ch := nc; ch.Type(c.lang) == "string_content" {
			content += parser.Text(ch, c.src)
		}
	}
	return dedent(content)
}

func dedent(s string) string {
	lines := strings.Split(strings.Trim(s, "\n"), "\n")
	indent := -1
	for _, l := range lines[1:] {
		if strings.TrimSpace(l) == "" {
			continue
		}
		n := len(l) - len(strings.TrimLeft(l, " \t"))
		if indent < 0 || n < indent {
			indent = n
		}
	}
	lines[0] = strings.TrimSpace(lines[0])
	for i, l := range lines[1:] {
		if indent > 0 && len(l) >= indent {
			l = l[indent:]
		}
		lines[i+1] = strings.TrimRight(l, " \t")
	}
	return strings.TrimSpace(strings.Join(lines, "\n"))
}

// ---- calls ----

// calls collects every call in body, including those inside nested
// functions, lambdas and comprehensions, attributed to the enclosing symbol.
// A callee whose first segment is a parameter or local of fn is unresolved.
func (c *fileCtx) calls(def *gotreesitter.Node, owner string) []parser.Relation {
	return c.callsUnder(def, parser.Field(def, c.lang, "body"), owner, false)
}

// classCalls collects the calls of a class body outside its methods and
// nested classes, which are symbols of their own: field defaults, class-level
// assignments.
func (c *fileCtx) classCalls(body *gotreesitter.Node) []parser.Relation {
	return c.callsUnder(body, body, "", true)
}

// isDef reports a definition that is a symbol of its own.
func (c *fileCtx) isDef(n *gotreesitter.Node) bool {
	switch n.Type(c.lang) {
	case "function_definition", "class_definition", "decorated_definition":
		return true
	}
	return false
}

func (c *fileCtx) callsUnder(scope, body *gotreesitter.Node, owner string, skipDefs bool) []parser.Relation {
	if body == nil {
		return nil
	}
	var rels []parser.Relation
	locals := c.locals(scope, skipDefs)
	// Imports inside the scope (often to dodge a circular import) resolve
	// its calls only.
	// shortcut: they apply to the whole scope, not only after the import statement.
	parent, cloned := c.imports, false
	defer func() { c.imports = parent }()
	parser.Walk(body, func(n *gotreesitter.Node) bool {
		if skipDefs && n != body && c.isDef(n) {
			return false
		}
		switch n.Type(c.lang) {
		case "import_statement", "import_from_statement":
			if !cloned {
				c.imports, cloned = maps.Clone(parent), true
			}
			if n.Type(c.lang) == "import_statement" {
				c.addImport(n)
			} else {
				c.addImportFrom(n)
			}
			return false
		}
		return true
	})
	parser.Walk(body, func(n *gotreesitter.Node) bool {
		if skipDefs && n != body && c.isDef(n) {
			return false
		}
		if n.Type(c.lang) != "call" {
			return true
		}
		fn := parser.Field(n, c.lang, "function")
		if fn == nil {
			return true
		}
		line, _ := parser.Lines(n)
		switch fn.Type(c.lang) {
		case "identifier":
			name := parser.Text(fn, c.src)
			if name == "super" { // super().m() is reported as the m call
				return true
			}
			qn := c.resolveDotted(name)
			if locals[name] {
				qn = ""
			}
			rels = append(rels, parser.Relation{Kind: parser.RelCall, TargetName: name, TargetQualifiedName: qn, Line: line})
		case "attribute":
			obj := parser.Field(fn, c.lang, "object")
			name := parser.Text(parser.Field(fn, c.lang, "attribute"), c.src)
			qn := c.resolveMethod(obj, name, owner)
			if first, _, _ := strings.Cut(parser.Text(obj, c.src), "."); locals[first] {
				qn = ""
			}
			rels = append(rels, parser.Relation{Kind: parser.RelCall, TargetName: name, TargetQualifiedName: qn, Line: line})
		}
		return true
	})
	return rels
}

// locals returns the parameter and assignment / for / with / except target
// names bound anywhere in def, nested functions and lambdas included (unless
// skipDefs). self and cls stay out: resolveMethod handles them.
// shortcut: one set for the whole function, so a local shadows an import everywhere in it, not only after its declaration.
func (c *fileCtx) locals(def *gotreesitter.Node, skipDefs bool) map[string]bool {
	set := map[string]bool{}
	parser.Walk(def, func(n *gotreesitter.Node) bool {
		if skipDefs && n != def && c.isDef(n) {
			return false
		}
		switch n.Type(c.lang) {
		case "function_definition", "lambda":
			c.bind(parser.Field(n, c.lang, "parameters"), set)
		case "assignment", "for_statement", "for_in_clause":
			c.bind(parser.Field(n, c.lang, "left"), set)
		case "as_pattern":
			c.bind(parser.Field(n, c.lang, "alias"), set)
		case "named_expression":
			c.bind(parser.Field(n, c.lang, "name"), set)
		}
		return true
	})
	delete(set, "self")
	delete(set, "cls")
	return set
}

// bind adds the names declared by a parameter list or assignment target,
// skipping defaults, annotations and attribute / subscript targets.
func (c *fileCtx) bind(n *gotreesitter.Node, set map[string]bool) {
	if n == nil {
		return
	}
	switch n.Type(c.lang) {
	case "identifier":
		set[parser.Text(n, c.src)] = true
	case "default_parameter", "typed_default_parameter":
		c.bind(parser.Field(n, c.lang, "name"), set)
	case "typed_parameter":
		if n.NamedChildCount() > 0 {
			c.bind(n.NamedChild(0), set)
		}
	case "attribute", "subscript":
	default:
		for _, ch := range parser.NamedChildren(n) {
			c.bind(ch, set)
		}
	}
}

// resolveDotted resolves "a.b.c" through the import table or the module's
// own definitions; "" when the first segment is unknown.
func (c *fileCtx) resolveDotted(text string) string {
	first, rest, _ := strings.Cut(text, ".")
	switch {
	case c.imports[first] != "":
		return c.canonical(join(c.imports[first], rest), 0)
	case c.defs[first]:
		return join(join(c.module, first), rest)
	}
	return ""
}

// resolveMethod resolves obj.name() for self/cls, imported modules and
// classes defined in this file. super() and unknown receivers give "".
func (c *fileCtx) resolveMethod(obj *gotreesitter.Node, name, owner string) string {
	if obj == nil {
		return ""
	}
	switch obj.Type(c.lang) {
	case "identifier":
		if id := parser.Text(obj, c.src); id == "self" || id == "cls" {
			if owner == "" {
				return ""
			}
			return join(join(c.module, owner), name)
		}
		fallthrough
	case "attribute":
		return c.resolveDotted(parser.Text(obj, c.src) + "." + name)
	}
	return ""
}

// canonical follows package __init__.py re-exports: with app/models/__init__.py
// doing "from .user import User", "app.models.User.find" becomes
// "app.models.user.User.find", the name the method is indexed under.
// shortcut: explicit imports only, a "from .user import *" re-export stays unresolved.
func (c *fileCtx) canonical(qn string, depth int) string {
	if c.root == "" || depth > 8 {
		return qn
	}
	parts := strings.Split(qn, ".")
	for i := 1; i < len(parts); i++ {
		pkg := strings.Join(parts[:i], ".")
		if t := c.reexports(pkg)[parts[i]]; t != "" && t != join(pkg, parts[i]) {
			return c.canonical(join(t, strings.Join(parts[i+1:], ".")), depth+1)
		}
	}
	return qn
}

// reexports is the import table of the workspace package pkg's __init__.py,
// nil when there is none. The package is looked up under the workspace root,
// then under each directory down to this file's (src/ layouts, monorepo
// subprojects). Only a regular file within the index size cap is read, so
// a symlink to a device is treated as absent. The lookup is cached per
// analyzed file, the parsed table process-wide (initTable).
func (c *fileCtx) reexports(pkg string) map[string]string {
	if m, ok := c.reexp[pkg]; ok {
		return m
	}
	var m map[string]string
	dirs := []string{c.root}
	if r, err := filepath.Rel(c.root, filepath.Dir(c.path)); err == nil && r != "." && !strings.HasPrefix(r, "..") {
		for _, seg := range strings.Split(r, string(filepath.Separator)) {
			dirs = append(dirs, filepath.Join(dirs[len(dirs)-1], seg))
		}
	}
	// src/ layout: tests/ and examples/ import the package from outside src/.
	dirs = append(dirs, filepath.Join(c.root, "src"))
	for _, d := range dirs {
		path := filepath.Join(d, filepath.Join(strings.Split(pkg, ".")...), "__init__.py")
		info, err := os.Lstat(path)
		if err != nil || !info.Mode().IsRegular() || info.Size() > 4<<20 {
			continue
		}
		m = initTable(c.root, path, info)
		break
	}
	c.reexp[pkg] = m
	return m
}

// initCache maps root+path of a package __init__.py to its import table and
// the mtime+size it was parsed from, so every importing file in a run shares
// one parse instead of re-reading app/__init__.py per file.
var initCache sync.Map

type initEntry struct {
	mtime time.Time
	size  int64
	table map[string]string
}

// initTable is the import table of the __init__.py at path, whose Lstat is
// info; nil when it cannot be read. The table is read-only once stored.
func initTable(root, path string, info os.FileInfo) map[string]string {
	key := root + "\x00" + path
	if v, ok := initCache.Load(key); ok {
		if e := v.(initEntry); e.size == info.Size() && e.mtime.Equal(info.ModTime()) {
			return e.table
		}
	}
	src, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	tree, lang, err := parser.ParseTree("python", src)
	if err != nil {
		return nil
	}
	ic := newCtx(parser.File{Path: path, Root: root, Source: src}, lang)
	ic.scan(tree.RootNode())
	tree.Release()
	initCache.Store(key, initEntry{info.ModTime(), info.Size(), ic.imports})
	return ic.imports
}

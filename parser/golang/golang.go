// Package golang is the Go analyzer: tree-sitter "go" grammar, one symbol
// per top-level declaration, relations resolved through the file's own
// import table and the types it declares.
package golang

import (
	"bufio"
	"bytes"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/doITmagic/rag-code-mcp/v2/parser"
	"github.com/odvcencio/gotreesitter"
)

func init() { parser.Register(Analyzer{}) }

// Analyzer implements parser.Analyzer for Go. It is stateless.
type Analyzer struct{}

func (Analyzer) Language() string     { return "go" }
func (Analyzer) Extensions() []string { return []string{".go"} }

// builtins are identifiers a call_expression may name without referring to a
// declared function: predeclared functions and conversion-only type names.
var builtins = map[string]bool{
	"append": true, "cap": true, "clear": true, "close": true, "complex": true, "copy": true,
	"delete": true, "imag": true, "len": true, "make": true, "max": true, "min": true, "new": true,
	"panic": true, "print": true, "println": true, "real": true, "recover": true,
	"bool": true, "byte": true, "rune": true, "string": true, "error": true, "any": true,
	"int": true, "int8": true, "int16": true, "int32": true, "int64": true,
	"uint": true, "uint8": true, "uint16": true, "uint32": true, "uint64": true, "uintptr": true,
	"float32": true, "float64": true, "complex64": true, "complex128": true, "comparable": true,
}

// file is the per-call context: nothing in it outlives Analyze.
type file struct {
	src     []byte
	lang    *gotreesitter.Language
	module  string
	imports map[string]string // local name → import path
	types   map[string]bool   // type names declared in this file
	tparams map[string]bool   // type parameters of the declaration being walked
	test    bool              // a _test.go file: nothing in it is package API
}

// Analyze parses one Go file. Malformed input yields whatever declarations
// tree-sitter could recover; only a parser failure returns an error.
func (Analyzer) Analyze(f parser.File) ([]parser.Symbol, error) {
	tree, lang, err := parser.ParseTree("go", f.Source)
	if err != nil {
		return nil, err
	}
	defer tree.Release()
	fc := &file{
		src:     f.Source,
		lang:    lang,
		module:  modulePath(f.Root, filepath.Dir(f.Path)),
		imports: map[string]string{},
		types:   map[string]bool{},
		test:    strings.HasSuffix(f.Path, "_test.go"),
	}
	root := tree.RootNode()
	// Pass 1: import table and locally declared type names.
	for _, n := range parser.NamedChildren(root) {
		switch n.Type(lang) {
		case "package_clause":
			// An external test package (package flux_test) is another package
			// than flux, as go/types sees it.
			if p := parser.NamedChildren(n); fc.test && len(p) == 1 && strings.HasSuffix(fc.text(p[0]), "_test") {
				fc.module += "_test"
			}
		case "import_declaration":
			parser.Walk(n, func(c *gotreesitter.Node) bool {
				if c.Type(lang) == "import_spec" {
					fc.addImport(c)
					return false
				}
				return true
			})
		case "type_declaration":
			for _, spec := range fc.specs(n, "type_spec", "type_alias") {
				if name := fc.text(parser.Field(spec, lang, "name")); name != "" {
					fc.types[name] = true
				}
			}
		}
	}
	// Pass 2: symbols.
	var syms []parser.Symbol
	for _, n := range parser.NamedChildren(root) {
		switch n.Type(lang) {
		case "function_declaration":
			syms = fc.appendFunc(syms, n, parser.KindFunction, "", "")
		case "method_declaration":
			recv, owner := fc.receiver(n)
			if owner != "" {
				syms = fc.appendFunc(syms, n, parser.KindMethod, recv, owner)
			}
		case "type_declaration":
			syms = fc.appendTypes(syms, n)
		case "var_declaration":
			syms = fc.appendValues(syms, n, "var_spec", parser.KindVariable)
		case "const_declaration":
			syms = fc.appendValues(syms, n, "const_spec", parser.KindConstant)
		}
	}
	return parser.Finalize(f, syms), nil
}

func (fc *file) text(n *gotreesitter.Node) string { return parser.Text(n, fc.src) }

func (fc *file) addImport(spec *gotreesitter.Node) {
	path := fc.text(parser.Field(spec, fc.lang, "path"))
	if p, err := strconv.Unquote(path); err == nil {
		path = p
	}
	if path == "" {
		return
	}
	name := assumedName(path)
	if n := parser.Field(spec, fc.lang, "name"); n != nil {
		switch n.Type(fc.lang) {
		case "dot", "blank_identifier":
			return
		}
		name = fc.text(n)
	}
	fc.imports[name] = path
}

// assumedName is goimports' guess of a package's name from its import path:
// the last element (the one before a /vN major suffix), without a "go-"
// prefix, cut at the first character an identifier cannot hold, so
// gopkg.in/yaml.v3 is yaml and github.com/mattn/go-isatty is isatty.
func assumedName(path string) string {
	parts := strings.Split(path, "/")
	name := parts[len(parts)-1]
	if len(parts) > 1 && len(name) > 1 && name[0] == 'v' && strings.Trim(name[1:], "0123456789") == "" {
		name = parts[len(parts)-2]
	}
	name = strings.TrimPrefix(name, "go-")
	if i := strings.IndexFunc(name, func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '_' }); i > 0 {
		name = name[:i]
	}
	return name
}

// specs returns the spec nodes of a grouped or single declaration; grouped
// specs may sit under a *_spec_list node.
func (fc *file) specs(decl *gotreesitter.Node, kinds ...string) []*gotreesitter.Node {
	var out []*gotreesitter.Node
	parser.Walk(decl, func(n *gotreesitter.Node) bool {
		if n == decl {
			return true
		}
		t := n.Type(fc.lang)
		for _, k := range kinds {
			if t == k {
				out = append(out, n)
				return false
			}
		}
		return strings.HasSuffix(t, "_spec_list")
	})
	return out
}

// base builds the symbol shared fields from the node that holds the whole
// declaration text.
func (fc *file) base(n *gotreesitter.Node, name string, kind parser.Kind, owner string) parser.Symbol {
	start, end := parser.Lines(n)
	content := fc.text(n)
	return parser.Symbol{
		Name:          name,
		Kind:          kind,
		Module:        fc.module,
		Owner:         owner,
		QualifiedName: parser.Qualify(fc.module, owner, name),
		Content:       content,
		Signature:     strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(firstLine(content)), "{")),
		Doc:           fc.docAbove(n),
		StartLine:     start,
		EndLine:       end,
		Exported:      !fc.test && upper(name) && (owner == "" || upper(owner)),
	}
}

// upper reports a Go exported identifier: the first rune is upper case.
func upper(name string) bool {
	r, _ := utf8.DecodeRuneInString(name)
	return unicode.IsUpper(r)
}

func (fc *file) appendFunc(syms []parser.Symbol, n *gotreesitter.Node, kind parser.Kind, recv, owner string) []parser.Symbol {
	name := fc.text(parser.Field(n, fc.lang, "name"))
	if name == "" {
		return syms
	}
	s := fc.base(n, name, kind, owner)
	if body := parser.Field(n, fc.lang, "body"); body != nil {
		s.Signature = strings.TrimSpace(string(fc.src[n.StartByte():body.StartByte()]))
	}
	s.Relations, _ = fc.relations(n, recv, owner)
	return append(syms, s)
}

// receiver returns the receiver variable name and base type of a method.
func (fc *file) receiver(n *gotreesitter.Node) (recv, owner string) {
	var decl *gotreesitter.Node
	parser.Walk(parser.Field(n, fc.lang, "receiver"), func(c *gotreesitter.Node) bool {
		if c.Type(fc.lang) == "parameter_declaration" {
			decl = c
		}
		return decl == nil
	})
	if decl == nil {
		return "", ""
	}
	recv = fc.text(parser.Field(decl, fc.lang, "name"))
	if recv == "_" {
		recv = ""
	}
	owner, _ = fc.resolveType(parser.Field(decl, fc.lang, "type"))
	return recv, owner
}

func (fc *file) appendTypes(syms []parser.Symbol, decl *gotreesitter.Node) []parser.Symbol {
	specs := fc.specs(decl, "type_spec", "type_alias")
	for _, spec := range specs {
		name := fc.text(parser.Field(spec, fc.lang, "name"))
		if name == "" {
			continue
		}
		node := spec
		if len(specs) == 1 {
			node = decl
		}
		kind := parser.KindType
		typ := parser.Field(spec, fc.lang, "type")
		switch typ.Type(fc.lang) {
		case "struct_type":
			kind = parser.KindClass
		case "interface_type":
			kind = parser.KindInterface
		}
		s := fc.base(node, name, kind, "")
		var methods []string
		s.Relations, methods = fc.relations(spec, "", "")
		if len(methods) > 0 {
			s.Metadata = map[string]string{"methods": strings.Join(methods, ",")}
		}
		syms = append(syms, s)
	}
	return syms
}

func (fc *file) appendValues(syms []parser.Symbol, decl *gotreesitter.Node, specKind string, kind parser.Kind) []parser.Symbol {
	specs := fc.specs(decl, specKind)
	for _, spec := range specs {
		var names []string
		for i := 0; i < spec.ChildCount(); i++ {
			if spec.FieldNameForChild(i, fc.lang) == "name" {
				names = append(names, fc.text(spec.Child(i)))
			}
		}
		node := spec
		if len(specs) == 1 {
			node = decl
		}
		rels, _ := fc.relations(spec, "", "")
		for _, name := range names {
			if name == "_" && len(rels) == 0 { // var _ = Describe(...) keeps its calls
				continue
			}
			s := fc.base(node, name, kind, "")
			s.Relations = rels
			syms = append(syms, s)
		}
	}
	return syms
}

// relations collects calls, type uses and embeddings under n. recv is the
// enclosing method's receiver variable (calls on it resolve to owner), and
// the returned names are interface method names when n is an interface.
// locals returns the parameter and variable names declared inside n.
// shortcut: one set for the whole declaration, so a local shadows a package
// function or an import of the same name everywhere in it, not only after its
// declaration.
func (fc *file) locals(n *gotreesitter.Node) map[string]bool {
	out := map[string]bool{}
	idents := func(c *gotreesitter.Node) {
		for _, id := range parser.NamedChildren(c) {
			if id.Type(fc.lang) == "identifier" {
				out[fc.text(id)] = true
			}
		}
	}
	recvList := parser.Field(n, fc.lang, "receiver") // the receiver is no local: a local of its name shadows it
	parser.Walk(n, func(c *gotreesitter.Node) bool {
		if recvList != nil && c.StartByte() == recvList.StartByte() && c.Type(fc.lang) == "parameter_list" {
			return false
		}
		switch c.Type(fc.lang) {
		case "parameter_declaration", "variadic_parameter_declaration", "var_spec":
			idents(c)
		case "short_var_declaration", "range_clause":
			if l := parser.Field(c, fc.lang, "left"); l != nil {
				idents(l)
			}
		}
		return true
	})
	return out
}

func (fc *file) relations(n *gotreesitter.Node, recv, owner string) (rels []parser.Relation, methods []string) {
	add := func(kind parser.RelationKind, name, tqn string, at *gotreesitter.Node) {
		if name == "" {
			return
		}
		line, _ := parser.Lines(at)
		rels = append(rels, parser.Relation{Kind: kind, TargetName: name, TargetQualifiedName: tqn, Line: line})
	}
	locals := fc.locals(n)
	fc.tparams = map[string]bool{}
	parser.Walk(n, func(c *gotreesitter.Node) bool {
		if c.Type(fc.lang) == "type_parameter_declaration" {
			for i := 0; i < c.ChildCount(); i++ {
				if c.FieldNameForChild(i, fc.lang) == "name" {
					fc.tparams[fc.text(c.Child(i))] = true
				}
			}
		}
		return true
	})
	recvList := parser.Field(n, fc.lang, "receiver") // the owner, not a use of it
	parser.Walk(recvList, func(c *gotreesitter.Node) bool {
		if c.Type(fc.lang) == "type_identifier" && fc.text(c) != owner { // T of (s *Stack[T])
			fc.tparams[fc.text(c)] = true
		}
		return true
	})
	var visit func(c *gotreesitter.Node) bool
	visit = func(c *gotreesitter.Node) bool {
		switch c.Type(fc.lang) {
		case "parameter_list":
			if recvList != nil && c.StartByte() == recvList.StartByte() {
				return false
			}
		case "type_spec", "type_alias": // skip the declared name
			parser.Walk(parser.Field(c, fc.lang, "type_parameters"), visit)
			parser.Walk(parser.Field(c, fc.lang, "type"), visit)
			return false
		case "type_identifier":
			if name, tqn := fc.resolveType(c); tqn != "" {
				add(parser.RelUsesType, name, tqn, c)
			}
		case "call_expression":
			fn := parser.Field(c, fc.lang, "function")
			switch fn.Type(fc.lang) {
			case "identifier":
				name := fc.text(fn)
				switch {
				case builtins[name], locals[name]: // a local func value is no package symbol
				case fc.types[name]: // conversion to a local type
					add(parser.RelUsesType, name, parser.Qualify(fc.module, "", name), c)
				default:
					add(parser.RelCall, name, parser.Qualify(fc.module, "", name), c)
				}
			case "selector_expression":
				field := parser.Field(fn, fc.lang, "field")
				name := fc.text(field)
				// at the field: a chain split over lines is on the method name's line
				add(parser.RelCall, name, fc.callTarget(parser.Field(fn, fc.lang, "operand"), name, recv, owner, locals), field)
			}
		case "composite_literal", "type_conversion_expression":
			t := parser.Field(c, fc.lang, "type")
			if c.Type(fc.lang) == "type_conversion_expression" && t != nil && t.Type(fc.lang) == "generic_type" {
				// "F[int](x)" parses as a conversion; without type info it
				// is a generic call unless F is a type declared here.
				kind, name, tqn := fc.genericCall(t)
				add(kind, name, tqn, c)
				parser.Walk(parser.Field(c, fc.lang, "operand"), visit)
				return false
			}
			// The type itself is picked up by the type_identifier and
			// qualified_type cases.
		case "qualified_type":
			name, tqn := fc.resolveType(c)
			add(parser.RelUsesType, name, tqn, c)
			return false
		case "field_declaration":
			if parser.Field(c, fc.lang, "name") == nil { // embedded
				name, tqn := fc.resolveType(parser.Field(c, fc.lang, "type"))
				add(parser.RelExtends, name, tqn, c)
				return false
			}
		case "interface_type":
			for _, e := range parser.NamedChildren(c) {
				switch e.Type(fc.lang) {
				case "type_elem":
					name, tqn := fc.resolveType(e.NamedChild(0))
					add(parser.RelExtends, name, tqn, e)
				case "method_elem":
					methods = append(methods, fc.text(parser.Field(e, fc.lang, "name")))
					parser.Walk(e, visit)
				}
			}
			return false
		}
		return true
	}
	parser.Walk(n, visit)
	return rels, methods
}

// callTarget resolves the qualified name of "operand.name(...)": the
// receiver variable, a type declared here (T.M, (*T).M, T{}.M), or an
// import; "" when the operand's type is unknown, as for a parameter or local
// that shadows an import.
func (fc *file) callTarget(operand *gotreesitter.Node, name, recv, owner string, locals map[string]bool) string {
	switch operand.Type(fc.lang) {
	case "identifier":
		id := fc.text(operand)
		switch {
		case locals[id]:
			return ""
		case recv != "" && id == recv:
			return parser.Qualify(fc.module, owner, name)
		case fc.types[id]:
			return parser.Qualify(fc.module, id, name)
		}
		if p, ok := fc.imports[id]; ok {
			return p + "." + name
		}
	case "parenthesized_expression":
		if u := operand.NamedChild(0); u != nil && u.Type(fc.lang) == "unary_expression" {
			return fc.callTarget(parser.Field(u, fc.lang, "operand"), name, "", "", locals)
		}
	case "composite_literal":
		if _, tqn := fc.resolveType(parser.Field(operand, fc.lang, "type")); tqn != "" {
			return tqn + "." + name
		}
	}
	return ""
}

// genericCall classifies "F[T](x)" / "pkg.F[T](x)": a conversion to a type
// declared in this file, otherwise a call resolved like an identifier or
// an import selector.
func (fc *file) genericCall(t *gotreesitter.Node) (parser.RelationKind, string, string) {
	name, tqn := fc.resolveType(t)
	inner := fc.bare(t)
	switch {
	case inner == nil || builtins[name]:
		return parser.RelUsesType, "", ""
	case inner.Type(fc.lang) == "qualified_type":
		return parser.RelCall, name, tqn
	case fc.types[name]:
		return parser.RelUsesType, name, tqn
	}
	return parser.RelCall, name, parser.Qualify(fc.module, "", name)
}

// bare strips pointers, generics and parentheses off a type node.
func (fc *file) bare(t *gotreesitter.Node) *gotreesitter.Node {
	for t != nil {
		switch t.Type(fc.lang) {
		case "generic_type":
			t = parser.Field(t, fc.lang, "type")
		case "pointer_type", "parenthesized_type", "unary_expression":
			t = t.NamedChild(0)
		default:
			return t
		}
	}
	return nil
}

// resolveType returns the short name of a type node and its qualified name:
// an import for pkg.T, this module for a type declared in the file, "" when
// unknown. Pointers, generics and parentheses are stripped.
func (fc *file) resolveType(t *gotreesitter.Node) (name, tqn string) {
	t = fc.bare(t)
	if t == nil {
		return "", ""
	}
	switch t.Type(fc.lang) {
	case "type_identifier":
		// Not predeclared, not a type parameter: declared in this package,
		// in this file or another one.
		name = fc.text(t)
		if !builtins[name] && !fc.tparams[name] {
			tqn = parser.Qualify(fc.module, "", name)
		}
	case "qualified_type":
		name = fc.text(parser.Field(t, fc.lang, "name"))
		if p, ok := fc.imports[fc.text(parser.Field(t, fc.lang, "package"))]; ok {
			tqn = p + "." + name
		}
	}
	return name, tqn
}

// docAbove returns the comment block that ends on the line right above n,
// without comment markers; "//go:" directives are dropped.
func (fc *file) docAbove(n *gotreesitter.Node) string {
	var lines []string
	next := int(n.StartPoint().Row)
	for p := n.PrevSibling(); p != nil && p.Type(fc.lang) == "comment" && int(p.EndPoint().Row) == next-1; p = p.PrevSibling() {
		next = int(p.StartPoint().Row)
		lines = append([]string{fc.text(p)}, lines...)
	}
	var out []string
	for _, c := range lines {
		if strings.HasPrefix(c, "/*") {
			c = strings.TrimSuffix(strings.TrimPrefix(c, "/*"), "*/")
			for _, l := range strings.Split(c, "\n") {
				out = append(out, strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(l), "*")))
			}
			continue
		}
		c = strings.TrimPrefix(c, "//")
		if strings.HasPrefix(c, "go:") {
			continue
		}
		out = append(out, strings.TrimSpace(c))
	}
	return strings.TrimSpace(strings.Join(out, "\n"))
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}

// ---- module path ----

// modCache maps root+dir to a modEntry, so go.mod is read once per directory
// and only stat-ed afterwards. shortcut: a go.mod created later in a
// directory that had none is not seen until a restart.
var modCache sync.Map

type modEntry struct {
	mod   string
	gomod string // the go.mod it came from, "" when none
	mtime time.Time
}

// modulePath is the Go import path of dir: the nearest go.mod at or above
// dir (not above root) plus the relative directory, or the directory
// relative to root when there is no go.mod.
func modulePath(root, dir string) string {
	root, dir = filepath.Clean(root), filepath.Clean(dir)
	key := root + "\x00" + dir
	if v, ok := modCache.Load(key); ok {
		e := v.(modEntry)
		if e.gomod == "" {
			return e.mod
		}
		if fi, err := os.Stat(e.gomod); err == nil && fi.ModTime().Equal(e.mtime) {
			return e.mod
		}
	}
	e := modEntry{}
	for d := dir; ; {
		p := filepath.Join(d, "go.mod")
		fi, err := os.Stat(p)
		if m := readModuleLine(p); err == nil && m != "" {
			e = modEntry{mod: joinSlash(m, relSlash(d, dir)), gomod: p, mtime: fi.ModTime()}
			break
		}
		parent := filepath.Dir(d)
		if d == root || parent == d {
			e.mod = relSlash(root, dir)
			break
		}
		d = parent
	}
	modCache.Store(key, e)
	return e.mod
}

func readModuleLine(path string) string {
	if info, err := os.Stat(path); err != nil || !info.Mode().IsRegular() { // a FIFO would block the open
		return ""
	}
	fh, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer fh.Close()
	sc := bufio.NewScanner(fh)
	for sc.Scan() {
		line := bytes.TrimSpace(sc.Bytes())
		if rest, ok := bytes.CutPrefix(line, []byte("module ")); ok {
			m := strings.TrimSpace(string(rest))
			if u, err := strconv.Unquote(m); err == nil {
				m = u
			}
			return m
		}
	}
	return ""
}

// relSlash is dir relative to base with forward slashes, "" when equal or
// not computable.
func relSlash(base, dir string) string {
	rel, err := filepath.Rel(base, dir)
	if err != nil || rel == "." || strings.HasPrefix(rel, "..") {
		return ""
	}
	return filepath.ToSlash(rel)
}

func joinSlash(a, b string) string {
	if b == "" {
		return a
	}
	return a + "/" + b
}

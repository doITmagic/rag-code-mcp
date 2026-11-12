// Package php analyzes PHP (tree-sitter grammar) and Blade (line scanner)
// files into parser symbols. Every name is resolved through the file's own
// namespace and import table at parse time; the analyzer keeps no state.
package php

import (
	"maps"
	"strings"

	"github.com/doITmagic/rag-code-mcp/v2/parser"
	"github.com/odvcencio/gotreesitter"
)

type analyzer struct{}

func init() { parser.Register(analyzer{}) }

func (analyzer) Language() string     { return "php" }
func (analyzer) Extensions() []string { return []string{".php", ".blade.php"} }

// Analyze parses one PHP or Blade file. Malformed input yields the
// declarations tree-sitter could still recover, never an error.
func (analyzer) Analyze(f parser.File) ([]parser.Symbol, error) {
	if strings.HasSuffix(strings.ToLower(f.Path), ".blade.php") {
		return parser.Finalize(f, blade(f)), nil
	}
	tree, lang, err := parser.ParseTree("php", f.Source)
	if err != nil {
		return nil, err
	}
	defer tree.Release()
	c := &collector{src: f.Source, lang: lang}
	c.resetImports()
	c.walkTop(tree.RootNode())
	c.syms = append(c.syms, frameworkSymbols(c.calls)...)
	return parser.Finalize(f, c.syms), nil
}

// collector holds the per-file walk state: current namespace, import table
// and the symbols and calls found so far.
type collector struct {
	src     []byte
	lang    *gotreesitter.Language
	ns      string            // current namespace, backslash separated, "" for global
	classes map[string]string // alias -> fully qualified class name
	funcs   map[string]string // alias -> fully qualified function name
	syms    []parser.Symbol
	calls   []rawCall
	vars    map[string]string // receiver -> declared class FQN in the body being walked: "$p", "$this->x"
}

// rawCall is one function or static call seen anywhere in the file, kept for
// the framework post-pass (routes and hooks).
type rawCall struct {
	ns, owner  string // namespace and enclosing symbol Name ("" at top level)
	recv       string // scope text for Foo::bar(), "" for a plain call
	recvFQN    string // recv resolved through the import table
	name       string
	arg        string // name argument when a plain string literal, "" otherwise
	content    string
	start, end int
	from, to   uint32 // byte range, to place a route inside its groups
	route      routeInfo
}

// routeInfo is what a Laravel route call says beyond its URI: for a group,
// the URI prefix and controller namespace it adds; for a route, its handler.
type routeInfo struct {
	prefix, ns string
	ctrl       string // controller of a string handler as written ("Backend\C"), joined to the group namespaces
	ctrlFQN    string // controller resolved from C::class
	action     string // controller method, "" for a resource or an invokable controller
	view       string // Route::view template name
}

func (c *collector) resetImports() {
	c.classes = map[string]string{}
	c.funcs = map[string]string{}
}

func (c *collector) text(n *gotreesitter.Node) string { return parser.Text(n, c.src) }

func (c *collector) typ(n *gotreesitter.Node) string {
	if n == nil {
		return ""
	}
	return n.Type(c.lang)
}

func (c *collector) field(n *gotreesitter.Node, name string) *gotreesitter.Node {
	return parser.Field(n, c.lang, name)
}

// walkTop visits the file's statements. Declarations are handled here and
// not descended into; everything else (namespaces, if-blocks, top-level
// calls and their closures) is walked through.
func (c *collector) walkTop(root *gotreesitter.Node) {
	parser.Walk(root, func(n *gotreesitter.Node) bool {
		switch n.Type(c.lang) {
		case "namespace_definition":
			c.ns = strings.TrimPrefix(c.text(c.field(n, "name")), "\\")
			c.resetImports()
			if body := c.field(n, "body"); body != nil { // braced form: scoped to its block
				c.walkTop(body)
				c.ns = ""
				c.resetImports()
			}
		case "namespace_use_declaration":
			c.use(n)
		case "const_declaration":
			c.constants(n, "")
		case "function_definition":
			c.function(n)
		case "class_declaration", "trait_declaration", "enum_declaration", "interface_declaration":
			c.classLike(n)
		case "anonymous_class":
			return false
		case "function_call_expression", "scoped_call_expression", "member_call_expression",
			"nullsafe_member_call_expression", "object_creation_expression":
			c.call(n, nil, "")
			return true
		default:
			return true
		}
		return false
	})
}

// walkBody records the calls of one function or method body into sym. Nested
// named declarations and anonymous classes keep their calls to themselves;
// closures and arrow functions count toward the enclosing symbol.
func (c *collector) walkBody(body *gotreesitter.Node, sym *parser.Symbol, owner string) {
	parser.Walk(body, func(n *gotreesitter.Node) bool {
		switch n.Type(c.lang) {
		case "function_call_expression", "scoped_call_expression", "member_call_expression",
			"nullsafe_member_call_expression", "object_creation_expression":
			c.call(n, sym, owner)
		case "class_constant_access_expression":
			c.classRef(n, sym, owner)
		case "anonymous_function", "arrow_function":
			c.closure(n, sym, owner)
			return false
		case "function_definition", "class_declaration", "trait_declaration", "enum_declaration",
			"interface_declaration", "anonymous_class":
			return false
		}
		return true
	})
}

// closure walks a closure or arrow function body into sym with its own
// receiver scope: its parameters shadow outer variables, typed or not; a
// closure sees only $this properties and its use() variables, an arrow
// function every outer variable it does not redeclare.
func (c *collector) closure(n *gotreesitter.Node, sym *parser.Symbol, owner string) {
	outer, inner := c.vars, map[string]string{}
	arrow := n.Type(c.lang) == "arrow_function"
	for k, v := range outer {
		if arrow || strings.HasPrefix(k, "$this->") {
			inner[k] = v
		}
	}
	for _, ch := range parser.NamedChildren(n) {
		if ch.Type(c.lang) == "anonymous_function_use_clause" {
			parser.Walk(ch, func(v *gotreesitter.Node) bool {
				if v.Type(c.lang) == "variable_name" {
					inner[c.text(v)] = outer[c.text(v)]
				}
				return true
			})
		}
	}
	params := c.field(n, "parameters")
	c.walkBody(params, sym, owner) // default values: new X(), X::class
	for _, p := range parser.NamedChildren(params) {
		inner[c.text(c.field(p, "name"))] = c.declType(c.field(p, "type"), owner)
	}
	c.vars = inner
	c.walkBody(c.field(n, "body"), sym, owner)
	c.vars = outer
}

// ---- imports and name resolution ----

// keyword returns "function" or "const" when that token is a direct child
// of a use declaration or clause, "" otherwise.
func (c *collector) keyword(n *gotreesitter.Node) string {
	for i := 0; i < n.ChildCount(); i++ {
		if ch := n.Child(i); !ch.IsNamed() {
			if t := ch.Type(c.lang); t == "function" || t == "const" {
				return t
			}
		}
	}
	return ""
}

func (c *collector) use(n *gotreesitter.Node) {
	kind, prefix := c.keyword(n), ""
	for _, ch := range parser.NamedChildren(n) {
		switch ch.Type(c.lang) {
		case "namespace_name":
			prefix = strings.TrimPrefix(c.text(ch), "\\") + "\\"
		case "namespace_use_group":
			for _, nc := range parser.NamedChildren(ch) {
				c.useClause(nc, prefix, kind)
			}
		case "namespace_use_clause":
			c.useClause(ch, "", kind)
		}
	}
}

func (c *collector) useClause(n *gotreesitter.Node, prefix, kind string) {
	if k := c.keyword(n); k != "" {
		kind = k
	}
	target := n.NamedChild(0)
	if t := c.typ(target); t != "name" && t != "qualified_name" {
		return
	}
	fqn := prefix + strings.TrimPrefix(c.text(target), "\\")
	alias := c.text(c.field(n, "alias"))
	if alias == "" {
		alias = short(fqn)
	}
	switch kind {
	case "function":
		c.funcs[alias] = fqn
	case "const":
		// Constant references are never relation targets.
	default:
		c.classes[alias] = fqn
	}
}

// resolve maps a class or function name as written to its fully qualified
// name (backslash separated, no leading backslash). table is the import
// table for unqualified names; qualified names always go through the class
// table, as PHP does. owner is the enclosing class for self/static. The
// result is "" when the target cannot be known (parent, self outside a
// class, dynamic names).
func (c *collector) resolve(name string, table map[string]string, owner string) string {
	switch name {
	case "":
		return ""
	case "self", "static":
		if owner == "" {
			return ""
		}
		return c.inNS(owner)
	case "parent":
		return ""
	}
	if name[0] == '\\' {
		return name[1:]
	}
	if rest, ok := strings.CutPrefix(name, "namespace\\"); ok {
		return c.inNS(rest)
	}
	first, rest, qualified := strings.Cut(name, "\\")
	if qualified {
		if fqn, ok := c.classes[first]; ok {
			return fqn + "\\" + rest
		}
		return c.inNS(name)
	}
	if fqn, ok := table[name]; ok {
		return fqn
	}
	return c.inNS(name)
}

func (c *collector) inNS(name string) string {
	if c.ns == "" {
		return name
	}
	return c.ns + "\\" + name
}

func short(fqn string) string { return fqn[strings.LastIndex(fqn, "\\")+1:] }

// qualified turns a fully qualified name into the parser's canonical form:
// the namespace keeps its backslashes, the last segment is joined with ".",
// so it equals parser.Qualify(namespace, "", name) of the declaration.
func qualified(fqn string) string {
	if fqn == "" {
		return ""
	}
	if i := strings.LastIndex(fqn, "\\"); i >= 0 {
		return fqn[:i] + "." + fqn[i+1:]
	}
	return fqn
}

// ---- declarations ----

func (c *collector) symbol(n *gotreesitter.Node, name string, kind parser.Kind, owner string, exported bool) parser.Symbol {
	start, end := parser.Lines(n)
	return parser.Symbol{
		Name: name, Kind: kind, Module: c.ns, Owner: owner,
		Signature: c.signature(n), Doc: c.doc(n), Content: c.text(n),
		StartLine: start, EndLine: end, Exported: exported,
	}
}

// signature is the declaration header: after the attributes, up to the body,
// without a trailing ";", collapsed to one line.
func (c *collector) signature(n *gotreesitter.Node) string {
	start, end := n.StartByte(), n.EndByte()
	if a := c.field(n, "attributes"); a != nil {
		start = a.EndByte()
	}
	if b := c.field(n, "body"); b != nil {
		end = b.StartByte()
	}
	if start > end || int(end) > len(c.src) {
		return ""
	}
	s := strings.TrimSuffix(strings.TrimSpace(string(c.src[start:end])), ";")
	return strings.Join(strings.Fields(s), " ")
}

// doc is the run of comments directly above the declaration, markers
// stripped.
func (c *collector) doc(n *gotreesitter.Node) string {
	var parts []string
	want, _ := parser.Lines(n)
	for p := n.PrevSibling(); p != nil; p = p.PrevSibling() {
		if !p.IsNamed() {
			continue
		}
		start, end := parser.Lines(p)
		if p.Type(c.lang) != "comment" || end < want-1 {
			break
		}
		parts = append([]string{c.text(p)}, parts...)
		want = start
	}
	return cleanComment(strings.Join(parts, "\n"))
}

func cleanComment(s string) string {
	var out []string
	for _, line := range strings.Split(s, "\n") {
		line = strings.TrimSpace(line)
		line = strings.TrimSuffix(line, "*/")
		for _, m := range []string{"/**", "/*", "//", "#", "*"} {
			if strings.HasPrefix(line, m) {
				line = line[len(m):]
				break
			}
		}
		if line = strings.TrimSpace(line); line != "" {
			out = append(out, line)
		}
	}
	return strings.Join(out, "\n")
}

// visible reports whether a member is public: no private/protected modifier.
func (c *collector) visible(n *gotreesitter.Node) bool {
	for _, ch := range parser.NamedChildren(n) {
		if ch.Type(c.lang) == "visibility_modifier" && c.text(ch) != "public" {
			return false
		}
	}
	return true
}

func (c *collector) constants(n *gotreesitter.Node, owner string) {
	exported := c.visible(n)
	for _, el := range parser.NamedChildren(n) {
		if el.Type(c.lang) != "const_element" {
			continue
		}
		if name := c.text(el.NamedChild(0)); name != "" {
			s := c.symbol(n, name, parser.KindConstant, owner, exported)
			s.Signature = strings.Join(strings.Fields(c.text(el)), " ")
			c.syms = append(c.syms, s)
		}
	}
}

func (c *collector) function(n *gotreesitter.Node) {
	name := c.text(c.field(n, "name"))
	if name == "" {
		return
	}
	s := c.symbol(n, name, parser.KindFunction, "", true)
	c.vars = c.paramTypes(n, "")
	c.walkBody(c.field(n, "body"), &s, "")
	c.vars = nil
	c.signatureTypes(n, &s)
	c.syms = append(c.syms, s)
}

// declType is the class FQN of a type declaration naming one class
// (?C included), "" for a scalar, union or missing type and for parent.
func (c *collector) declType(t *gotreesitter.Node, owner string) string {
	if c.typ(t) == "optional_type" {
		t = t.NamedChild(0)
	}
	if c.typ(t) != "named_type" || t.NamedChildCount() != 1 {
		return ""
	}
	ch := t.NamedChild(0)
	if k := c.typ(ch); k != "name" && k != "qualified_name" {
		return ""
	}
	return c.resolve(c.text(ch), c.classes, owner)
}

// paramTypes maps each parameter of fn declared with one class to it
// ("$request" -> Illuminate\Http\Request).
// shortcut: a parameter reassigned in the body keeps its declared class; flow analysis if that misleads.
func (c *collector) paramTypes(fn *gotreesitter.Node, owner string) map[string]string {
	out := map[string]string{}
	for _, p := range parser.NamedChildren(c.field(fn, "parameters")) {
		if t := c.declType(c.field(p, "type"), owner); t != "" {
			out[c.text(c.field(p, "name"))] = t
		}
	}
	return out
}

// propTypes maps "$this->x" to the class of property x when declared: a
// typed property, a constructor-promoted parameter, or a property the
// constructor assigns from a typed parameter ($this->repo = $users).
func (c *collector) propTypes(body *gotreesitter.Node, owner string) map[string]string {
	out := map[string]string{}
	for _, ch := range parser.NamedChildren(body) {
		switch ch.Type(c.lang) {
		case "property_declaration":
			if t := c.declType(c.field(ch, "type"), owner); t != "" {
				for _, el := range parser.NamedChildren(ch) {
					if el.Type(c.lang) == "property_element" {
						out["$this->"+strings.TrimPrefix(c.text(c.field(el, "name")), "$")] = t
					}
				}
			}
		case "method_declaration":
			if !strings.EqualFold(c.text(c.field(ch, "name")), "__construct") {
				continue
			}
			params := c.paramTypes(ch, owner)
			for _, p := range parser.NamedChildren(c.field(ch, "parameters")) {
				pn := c.text(c.field(p, "name"))
				if p.Type(c.lang) == "property_promotion_parameter" && params[pn] != "" {
					out["$this->"+strings.TrimPrefix(pn, "$")] = params[pn]
				}
			}
			parser.Walk(c.field(ch, "body"), func(a *gotreesitter.Node) bool {
				if a.Type(c.lang) != "assignment_expression" {
					return true
				}
				l, r := c.field(a, "left"), c.field(a, "right")
				if c.typ(l) == "member_access_expression" && c.text(c.field(l, "object")) == "$this" && params[c.text(r)] != "" {
					if k := "$this->" + c.text(c.field(l, "name")); out[k] == "" {
						out[k] = params[c.text(r)]
					}
				}
				return false
			})
		}
	}
	return out
}

// signatureTypes adds a uses_type relation per class named in the parameter
// and return type declarations (nullable and union types included); scalar
// types and self/static/parent are skipped.
func (c *collector) signatureTypes(n *gotreesitter.Node, s *parser.Symbol) {
	c.typeUses(c.field(n, "parameters"), s)
	c.typeUses(c.field(n, "return_type"), s)
}

// typeUses adds a uses_type relation per class named in the type
// declarations under n.
func (c *collector) typeUses(n *gotreesitter.Node, s *parser.Symbol) {
	parser.Walk(n, func(t *gotreesitter.Node) bool {
		if t.Type(c.lang) != "named_type" {
			return true
		}
		for _, ch := range parser.NamedChildren(t) {
			if k := ch.Type(c.lang); k != "name" && k != "qualified_name" {
				continue
			}
			raw := c.text(ch)
			switch strings.ToLower(raw) {
			case "self", "static", "parent":
				continue
			}
			line, _ := parser.Lines(ch)
			s.Relations = append(s.Relations, parser.Relation{Kind: parser.RelUsesType, TargetName: short(raw),
				TargetQualifiedName: qualified(c.resolve(raw, c.classes, "")), Line: line})
		}
		return false
	})
}

func (c *collector) classLike(n *gotreesitter.Node) {
	nameNode := c.field(n, "name")
	name := c.text(nameNode)
	if name == "" {
		return
	}
	// "class Object extends Base": a name the grammar reserves parses as an
	// ERROR node "Object extends" and the base as the class name.
	var baseKind parser.RelationKind
	for _, ch := range parser.NamedChildren(n) {
		if ch.Type(c.lang) != "ERROR" || ch.StartByte() > nameNode.StartByte() {
			continue
		}
		if f := strings.Fields(c.text(ch)); len(f) == 2 && (f[1] == "extends" || f[1] == "implements") {
			name, baseKind = f[0], parser.RelExtends
			if f[1] == "implements" {
				baseKind = parser.RelImplements
			}
		}
	}
	kind := parser.KindClass
	if n.Type(c.lang) == "interface_declaration" {
		kind = parser.KindInterface
	}
	s := c.symbol(n, name, kind, "", true)
	if baseKind != "" {
		c.typeRefs(&s, n, baseKind) // the only name child of n is the misparsed base
	}
	for _, nc := range parser.NamedChildren(n) {
		switch ch := nc; ch.Type(c.lang) {
		case "base_clause":
			c.typeRefs(&s, ch, parser.RelExtends)
		case "class_interface_clause":
			c.typeRefs(&s, ch, parser.RelImplements)
		}
	}
	body := c.field(n, "body")
	for _, nc := range parser.NamedChildren(body) {
		if ch := nc; ch.Type(c.lang) == "use_declaration" {
			c.typeRefs(&s, ch, parser.RelUsesType)
		}
	}
	c.syms = append(c.syms, s)
	props := c.propTypes(body, name)
	for _, nc := range parser.NamedChildren(body) {
		switch ch := nc; ch.Type(c.lang) {
		case "const_declaration":
			c.constants(ch, name)
		case "property_declaration":
			c.properties(ch, name)
		case "method_declaration":
			c.method(ch, name, props)
		case "enum_case":
			if cn := c.text(c.field(ch, "name")); cn != "" {
				c.syms = append(c.syms, c.symbol(ch, cn, parser.KindConstant, name, true))
			}
		}
	}
}

// typeRefs adds one relation per class name in an extends/implements/use
// clause.
func (c *collector) typeRefs(s *parser.Symbol, clause *gotreesitter.Node, kind parser.RelationKind) {
	line, _ := parser.Lines(clause)
	for _, ch := range parser.NamedChildren(clause) {
		if t := ch.Type(c.lang); t != "name" && t != "qualified_name" {
			continue
		}
		raw := c.text(ch)
		s.Relations = append(s.Relations, parser.Relation{
			Kind: kind, TargetName: short(raw),
			TargetQualifiedName: qualified(c.resolve(raw, c.classes, "")), Line: line,
		})
	}
}

func (c *collector) properties(n *gotreesitter.Node, owner string) {
	exported := c.visible(n)
	for _, el := range parser.NamedChildren(n) {
		if el.Type(c.lang) != "property_element" {
			continue
		}
		name := strings.TrimPrefix(c.text(c.field(el, "name")), "$")
		if name == "" {
			continue
		}
		s := c.symbol(n, name, parser.KindVariable, owner, exported)
		c.typeUses(n, &s)
		parser.Walk(el, func(v *gotreesitter.Node) bool {
			switch v.Type(c.lang) {
			case "class_constant_access_expression":
				c.classRef(v, &s, owner)
			case "string", "encapsed_string":
				// Laravel's event map names its classes as strings:
				// $listen = ['App\Events\E' => ['App\Listeners\L']].
				if lit := c.literal(v); name == "listen" && strings.Contains(lit, `\`) && className(lit) {
					c.classString(v, lit, &s)
				}
			}
			return true
		})
		c.syms = append(c.syms, s)
	}
}

// classRef adds a uses_type relation for X::class; self, static and parent
// name the enclosing class and are skipped.
func (c *collector) classRef(n *gotreesitter.Node, s *parser.Symbol, owner string) {
	fqn := c.classConst(n, owner)
	if fqn == "" {
		return
	}
	switch strings.ToLower(c.text(n.NamedChild(0))) {
	case "self", "static", "parent":
		return
	}
	line, _ := parser.Lines(n)
	s.Relations = append(s.Relations, parser.Relation{Kind: parser.RelUsesType, TargetName: short(fqn),
		TargetQualifiedName: qualified(fqn), Line: line})
}

// classString adds a uses_type relation for a class named by a string
// literal, which PHP always reads as fully qualified.
func (c *collector) classString(n *gotreesitter.Node, lit string, s *parser.Symbol) {
	fqn := strings.TrimPrefix(lit, `\`)
	line, _ := parser.Lines(n)
	s.Relations = append(s.Relations, parser.Relation{Kind: parser.RelUsesType, TargetName: short(fqn),
		TargetQualifiedName: qualified(fqn), Line: line})
}

func (c *collector) method(n *gotreesitter.Node, owner string, props map[string]string) {
	name := c.text(c.field(n, "name"))
	if name == "" {
		return
	}
	s := c.symbol(n, name, parser.KindMethod, owner, c.visible(n))
	c.vars = c.paramTypes(n, owner)
	maps.Copy(c.vars, props)
	c.walkBody(c.field(n, "body"), &s, owner)
	c.vars = nil
	c.signatureTypes(n, &s)
	c.syms = append(c.syms, s)
	params := c.field(n, "parameters")
	for _, p := range parser.NamedChildren(params) {
		if p.Type(c.lang) != "property_promotion_parameter" {
			continue
		}
		if pn := strings.TrimPrefix(c.text(c.field(p, "name")), "$"); pn != "" {
			ps := c.symbol(p, pn, parser.KindVariable, owner, c.visible(p))
			ps.Doc = ""
			c.syms = append(c.syms, ps)
		}
	}
}

// ---- calls ----

// call records one call expression as a relation of sym (when inside a
// declaration) and, for plain and static calls, as a rawCall for the
// framework pass. define() at top level becomes a constant.
func (c *collector) call(n *gotreesitter.Node, sym *parser.Symbol, owner string) {
	kind, track := parser.RelCall, false // track: plain and static calls feed the framework pass
	var recv, recvFQN, name, target string
	viewCall := false // view('x') or View::make('x')
	at := n           // the node whose line the relation takes
	switch n.Type(c.lang) {
	case "function_call_expression":
		track = true
		fn := c.field(n, "function")
		if t := c.typ(fn); t != "name" && t != "qualified_name" && t != "relative_name" {
			return // $fn(), (...)(): nothing to name
		}
		raw := c.text(fn)
		name, target = short(raw), qualified(c.resolve(raw, c.funcs, owner))
		viewCall = raw == "view" || raw == `\view`
		if _, imported := c.funcs[raw]; c.ns != "" && !imported && !strings.ContainsRune(raw, '\\') {
			// PHP falls back to the global function at runtime, so the
			// namespace is only a guess: leave the target to a name lookup.
			target = ""
		}
		if sym == nil && name == "define" {
			c.define(n)
			return
		}
	case "scoped_call_expression":
		track = true
		scope, fn := c.field(n, "scope"), c.field(n, "name")
		if c.typ(fn) != "name" {
			return
		}
		recv, name = c.text(scope), c.text(fn)
		if t := c.typ(scope); t == "name" || t == "qualified_name" || t == "relative_scope" {
			recvFQN = c.resolve(recv, c.classes, owner)
		}
		if recvFQN != "" {
			target = qualified(recvFQN) + "." + name
		}
		viewCall = name == "make" && (recv == "View" || recvFQN == `Illuminate\Support\Facades\View`)
	case "member_call_expression", "nullsafe_member_call_expression":
		fn := c.field(n, "name")
		if c.typ(fn) != "name" {
			return
		}
		name, at = c.text(fn), fn
		if obj := c.text(c.field(n, "object")); obj == "$this" && owner != "" {
			target = parser.Qualify(c.ns, owner, name)
		} else if fqn := c.vars[obj]; fqn != "" { // a declared class: typed parameter or property
			target = qualified(fqn) + "." + name
		}
		if name == "group" {
			c.fluentGroup(n)
		}
	case "object_creation_expression":
		kind = parser.RelUsesType
		cls := n.NamedChild(0)
		if t := c.typ(cls); t != "name" && t != "qualified_name" {
			return // new $cls, new class {...}
		}
		raw := c.text(cls)
		name, target = short(raw), qualified(c.resolve(raw, c.classes, owner))
	}
	if name == "" {
		return
	}
	start, end := parser.Lines(n)
	line, _ := parser.Lines(at) // a -> chain split over lines: the method name's line
	if sym != nil {
		sym.Relations = append(sym.Relations, parser.Relation{
			Kind: kind, TargetName: name, TargetQualifiedName: target, Line: line,
		})
		// view('users.show'): the Blade template's qualified name; a package
		// view (mail::message) is not in the workspace.
		// $this->hasMany('App\Post'): an Eloquent relation naming its model
		// as a string (C::class is handled by the walk).
		if eloquentRelations[name] && target != "" && c.typ(n) == "member_call_expression" { // target set: on $this
			for i, a := range c.args(n) {
				if lit := c.literal(a); i < 2 && className(lit) && (i == 0 || strings.HasSuffix(name, "Through")) {
					c.classString(a, lit, sym)
				}
			}
		}
		if v := c.viewName(n, viewCall); v != "" {
			sym.Relations = append(sym.Relations, parser.Relation{
				Kind: parser.RelImports, TargetName: v, TargetQualifiedName: v, Line: line,
			})
		}
	}
	if track {
		var ownerName string
		if sym != nil {
			ownerName = sym.Name
		}
		pos := 0
		if name == "match" { // Route::match(methods, uri, action)
			pos = 1
		}
		rc := rawCall{
			ns: c.ns, owner: ownerName, recv: recv, recvFQN: recvFQN, name: name,
			arg: c.stringArg(n, pos), content: c.text(n), start: start, end: end,
			from: n.StartByte(), to: n.EndByte(),
		}
		if isRouteRecv(recv, recvFQN) {
			rc.route = c.routeInfo(n, name, pos, owner)
		}
		c.calls = append(c.calls, rc)
	}
}

// viewName is the template a view call renders: its first argument when a
// plain string not naming a package view.
func (c *collector) viewName(n *gotreesitter.Node, viewCall bool) string {
	if !viewCall {
		return ""
	}
	if v := c.stringArg(n, 0); !strings.Contains(v, "::") {
		return v
	}
	return ""
}

// define turns a top-level define('NAME', value) into a constant symbol.
// define() always creates a global constant, whatever the namespace.
func (c *collector) define(n *gotreesitter.Node) {
	name := c.stringArg(n, 0)
	if name == "" {
		return
	}
	s := c.symbol(n, name, parser.KindConstant, "", true)
	s.Module, s.Signature = "", c.text(n)
	c.syms = append(c.syms, s)
}

// stringArg returns argument pos without quotes when it is a plain string
// literal (no concatenation or interpolation), "" otherwise.
func (c *collector) stringArg(n *gotreesitter.Node, pos int) string {
	args := c.args(n)
	if pos >= len(args) {
		return ""
	}
	return c.literal(args[pos])
}

// args returns the expressions of the call's arguments.
func (c *collector) args(n *gotreesitter.Node) []*gotreesitter.Node {
	var out []*gotreesitter.Node
	for _, nc := range parser.NamedChildren(c.field(n, "arguments")) {
		if nc.Type(c.lang) == "argument" {
			out = append(out, nc.NamedChild(0))
		}
	}
	return out
}

// singleQuoted decodes the only two escapes a single-quoted PHP string has.
var singleQuoted = strings.NewReplacer(`\\`, `\`, `\'`, `'`)

// literal returns a plain string literal (no concatenation or
// interpolation) without quotes and with \\ decoded ('App\\User' is
// App\User), "" for anything else.
// shortcut: in double quotes only \\ is decoded, enough for class names; a full decoder if other escapes matter.
func (c *collector) literal(v *gotreesitter.Node) string {
	s := c.text(v)
	if t := c.typ(v); len(s) < 2 || t != "string" && (t != "encapsed_string" || strings.ContainsRune(s, '$')) {
		return ""
	}
	if s[0] == '\'' {
		return singleQuoted.Replace(s[1 : len(s)-1])
	}
	return strings.ReplaceAll(s[1:len(s)-1], `\\`, `\`)
}

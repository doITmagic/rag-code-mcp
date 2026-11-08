package gdscript

import (
	"strings"

	"github.com/odvcencio/gotreesitter"

	"github.com/doITmagic/rag-code-mcp/v3/parser"
)

// scriptCtx is the per-call state of one .gd file.
type scriptCtx struct {
	src    []byte
	lang   *gotreesitter.Language
	module string
}

// scope is the class whose body is being collected: the script itself
// (owner = its class_name, "" when none) or an inner class.
type scope struct {
	owner string
	inner bool
	defs  map[string]bool // functions and signals the body declares
	base  string          // the class_name this class extends, "" for a path or none
	fn    string          // the function whose body is collected, for super()
}

func script(f parser.File) ([]parser.Symbol, error) {
	tree, lang, err := parser.ParseTree("gdscript", f.Source)
	if err != nil {
		return nil, err
	}
	defer tree.Release()
	c := &scriptCtx{src: f.Source, lang: lang, module: module(f)}
	root := tree.RootNode()

	// Header pass: class_name and extends may come in either order.
	var classNode, extNode *gotreesitter.Node
	for _, n := range parser.NamedChildren(root) {
		switch c.typ(n) {
		case "class_name_statement":
			classNode = n
			if e := c.child(n, "extends_statement"); e != nil {
				extNode = e
			}
		case "extends_statement":
			extNode = n
		}
	}
	className := ""
	if classNode != nil {
		className = c.name(classNode)
	}
	sc := scope{owner: className, defs: c.declared(root), base: c.baseName(extNode)}
	var syms []parser.Symbol
	c.collect(root, sc, classNode, extNode, &syms)
	return syms, nil
}

func (c *scriptCtx) typ(n *gotreesitter.Node) string  { return n.Type(c.lang) }
func (c *scriptCtx) text(n *gotreesitter.Node) string { return parser.Text(n, c.src) }

// child returns the first direct child of the given node type, or nil.
func (c *scriptCtx) child(n *gotreesitter.Node, typ string) *gotreesitter.Node {
	for i := 0; i < n.ChildCount(); i++ {
		if ch := n.Child(i); c.typ(ch) == typ {
			return ch
		}
	}
	return nil
}

func (c *scriptCtx) name(n *gotreesitter.Node) string {
	if c.typ(n) == "constructor_definition" {
		return "_init"
	}
	return c.text(c.child(n, "name"))
}

// declared lists the functions and signals a class body declares, so calls
// can resolve forward references.
func (c *scriptCtx) declared(body *gotreesitter.Node) map[string]bool {
	defs := map[string]bool{}
	for _, n := range parser.NamedChildren(body) {
		switch c.typ(n) {
		case "function_definition", "constructor_definition", "signal_statement":
			if name := c.name(n); name != "" {
				defs[name] = true
			}
		}
	}
	return defs
}

// collect emits the members of a script or inner class body. classNode and
// extNode are the script header statements (nil for inner classes); the
// class symbol is emitted at the class_name line, or at the extends line
// named after the file when the script declares no class_name.
func (c *scriptCtx) collect(body *gotreesitter.Node, sc scope, classNode, extNode *gotreesitter.Node, syms *[]parser.Symbol) {
	var doc, annos []string
	docEnd, lastEnd := -2, -1
	for _, n := range parser.NamedChildren(body) {
		start, end := parser.Lines(n)
		switch c.typ(n) {
		case "comment":
			if start == lastEnd { // trailing comment of the previous member
				continue
			}
			if docEnd != start-1 {
				doc = doc[:0]
			}
			doc = append(doc, strings.TrimPrefix(strings.TrimLeft(c.text(n), "#"), " "))
			docEnd = end
			continue
		case "annotation":
			if docEnd == start-1 {
				docEnd = end
			}
			annos = append(annos, c.text(n))
			continue
		}
		d := ""
		if docEnd == start-1 {
			d = strings.Join(doc, "\n")
		}
		doc, docEnd = doc[:0], -2
		lastEnd = end
		s := parser.Symbol{Module: c.module, Owner: sc.owner, Doc: d, Content: c.text(n), StartLine: start, EndLine: end}
		switch c.typ(n) {
		case "class_name_statement":
			s.Name, s.Kind, s.Owner, s.Signature = c.name(n), parser.KindClass, "", strings.TrimSpace(c.text(n))
			if extNode != nil {
				s.Relations = []parser.Relation{c.extendsRel(extNode)}
			}
		case "extends_statement":
			if classNode != nil {
				continue
			}
			s.Name, s.Kind, s.Owner, s.Signature = stem(c.module), parser.KindClass, "", strings.TrimSpace(c.text(n))
			s.Relations = []parser.Relation{c.extendsRel(n)}
		case "function_definition", "constructor_definition":
			s.Name, s.Kind, s.Signature = c.name(n), parser.KindFunction, c.header(n, "body")
			if sc.inner {
				s.Kind = parser.KindMethod
			}
			fsc := sc
			fsc.fn = s.Name
			s.Relations = c.calls(c.child(n, "body"), fsc)
		case "signal_statement":
			s.Name, s.Kind, s.Signature = c.name(n), parser.KindVariable, firstLine(s.Content)
			s.Metadata = map[string]string{"gd": "signal"}
		case "const_statement":
			s.Name, s.Kind, s.Signature = c.name(n), parser.KindConstant, firstLine(s.Content)
		case "variable_statement", "export_variable_statement", "onready_variable_statement":
			s.Name, s.Kind, s.Signature = c.name(n), parser.KindVariable, firstLine(s.Content)
			s.Metadata = c.varMeta(n, annos)
		case "enum_definition":
			s.Name, s.Kind, s.Signature = c.name(n), parser.KindConstant, firstLine(s.Content)
			s.Metadata = map[string]string{"gd": "enum"}
			if s.Name == "" { // an unnamed enum declares its values as constants
				c.enumValues(n, s, syms)
				continue
			}
		case "class_definition":
			s.Name, s.Kind, s.Owner, s.Signature = c.name(n), parser.KindClass, "", c.header(n, "class_body")
			if e := c.child(n, "extends_statement"); e != nil {
				s.Relations = []parser.Relation{c.extendsRel(e)}
			}
			s.Exported = !strings.HasPrefix(s.Name, "_")
			*syms = append(*syms, s)
			if b := c.child(n, "class_body"); b != nil && s.Name != "" {
				c.collect(b, scope{owner: s.Name, inner: true, defs: c.declared(b), base: c.baseName(c.child(n, "extends_statement"))}, nil, nil, syms)
			}
			annos = annos[:0]
			continue
		default:
			continue
		}
		annos = annos[:0]
		if s.Name == "" {
			continue
		}
		s.Exported = !strings.HasPrefix(s.Name, "_")
		*syms = append(*syms, s)
	}
}

func (c *scriptCtx) enumValues(n *gotreesitter.Node, base parser.Symbol, syms *[]parser.Symbol) {
	list := c.child(n, "enumerator_list")
	if list == nil {
		return
	}
	for _, e := range parser.NamedChildren(list) {
		if c.typ(e) != "enumerator" {
			continue
		}
		s := base
		s.Name = c.text(c.child(e, "identifier"))
		if s.Name == "" {
			continue
		}
		s.Exported = !strings.HasPrefix(s.Name, "_")
		*syms = append(*syms, s)
	}
}

// header returns the source of n up to its body child, without the colon.
func (c *scriptCtx) header(n *gotreesitter.Node, bodyType string) string {
	end := n.EndByte()
	if b := c.child(n, bodyType); b != nil {
		end = b.StartByte()
	}
	return strings.TrimSuffix(strings.TrimSpace(string(c.src[n.StartByte():end])), ":")
}

func firstLine(s string) string {
	line, _, _ := strings.Cut(s, "\n")
	return strings.TrimSpace(line)
}

// varMeta records @export / @onready from standalone annotation lines, the
// member's own annotations and the Godot 3 keyword forms.
func (c *scriptCtx) varMeta(n *gotreesitter.Node, standalone []string) map[string]string {
	names := append([]string(nil), standalone...)
	if a := c.child(n, "annotations"); a != nil {
		for _, nc := range parser.NamedChildren(a) {
			names = append(names, c.text(nc))
		}
	}
	m := map[string]string{}
	switch c.typ(n) {
	case "export_variable_statement":
		m["export"] = "true"
	case "onready_variable_statement":
		m["onready"] = "true"
	}
	for _, a := range names {
		name, _, _ := strings.Cut(strings.TrimPrefix(a, "@"), "(")
		switch {
		case name == "onready":
			m["onready"] = "true"
		case name == "export_group", name == "export_subgroup", name == "export_category":
		case strings.HasPrefix(name, "export"):
			m["export"] = "true"
		}
	}
	if len(m) == 0 {
		return nil
	}
	return m
}

// extendsRel targets the base class: a res:// module for a string path,
// unresolved for a class name (the base may be an engine type).
func (c *scriptCtx) extendsRel(e *gotreesitter.Node) parser.Relation {
	line, _ := parser.Lines(e)
	if s := c.child(e, "string"); s != nil {
		p := strings.Trim(c.text(s), `"'`)
		return parser.Relation{Kind: parser.RelExtends, TargetName: stem(p), TargetQualifiedName: resModule(p, c.module), Line: line}
	}
	return parser.Relation{Kind: parser.RelExtends, TargetName: c.text(e.NamedChild(0)), Line: line}
}

// baseName is the class name an extends statement names, "" for a res://
// path (whose class name is not known here) or no statement.
func (c *scriptCtx) baseName(e *gotreesitter.Node) string {
	if e == nil {
		return ""
	}
	if r := c.extendsRel(e); r.TargetQualifiedName == "" {
		return r.TargetName
	}
	return ""
}

// calls collects the call relations under a function body. Bare and self
// calls resolve to the enclosing class when it declares the callee; any
// other receiver stays unresolved. connect() handlers and emitted signals
// count as calls to the handler / signal.
func (c *scriptCtx) calls(body *gotreesitter.Node, sc scope) []parser.Relation {
	var rels []parser.Relation
	seen := map[parser.Relation]bool{}
	add := func(name string, line int, local bool) {
		if name == "" {
			return
		}
		r := parser.Relation{Kind: parser.RelCall, TargetName: name, Line: line}
		if local && sc.defs[name] {
			r.TargetQualifiedName = parser.Qualify(c.module, sc.owner, name)
		}
		if !seen[r] {
			seen[r] = true
			rels = append(rels, r)
		}
	}
	parser.Walk(body, func(n *gotreesitter.Node) bool {
		t := c.typ(n)
		if t != "call" && t != "attribute_call" {
			return true
		}
		fn := n.Child(0)
		if fn == nil || c.typ(fn) != "identifier" {
			return true
		}
		name := c.text(fn)
		line, _ := parser.Lines(n)
		recv := ""
		if t == "attribute_call" {
			recv = c.receiver(n)
		}
		local := t == "call" || recv == "self"
		if t == "call" && name == "super" { // super(): the base's method of the same name
			name, recv, local = sc.fn, "super", false
		}
		if recv == "super" {
			// The base's method: a qualified suffix the index resolves when one class has it.
			r := parser.Relation{Kind: parser.RelCall, TargetName: name, Line: line}
			if sc.base != "" && name != "" {
				r.TargetQualifiedName = sc.base + "." + name
			}
			if name != "" && !seen[r] {
				seen[r] = true
				rels = append(rels, r)
			}
			return true
		}
		add(name, line, local)
		switch name {
		case "emit_signal":
			if local {
				add(c.firstString(n), line, true)
			}
		case "emit":
			if recv != "" && recv != "self" && sc.defs[recv] {
				add(recv, line, true)
			}
		case "connect":
			h, self := c.handler(n)
			add(h, line, self)
		}
		return true
	})
	return rels
}

// receiver returns the identifier an attribute_call is invoked on when the
// chain is a single `ident.call()`, "" for anything else ($Node, get_node(),
// a.b.c()).
func (c *scriptCtx) receiver(call *gotreesitter.Node) string {
	p := call.Parent()
	if p == nil || c.typ(p) != "attribute" || p.ChildCount() != 3 || c.typ(p.Child(0)) != "identifier" {
		return ""
	}
	return c.text(p.Child(0))
}

func (c *scriptCtx) args(call *gotreesitter.Node) []*gotreesitter.Node {
	a := c.child(call, "arguments")
	if a == nil {
		return nil
	}
	var out []*gotreesitter.Node
	for _, nc := range parser.NamedChildren(a) {
		if n := nc; c.typ(n) != "comment" {
			out = append(out, n)
		}
	}
	return out
}

func (c *scriptCtx) str(n *gotreesitter.Node) string {
	if n == nil {
		return ""
	}
	if t := c.typ(n); t != "string" && t != "string_name" {
		return ""
	}
	return strings.Trim(strings.TrimPrefix(c.text(n), "&"), `"'`)
}

func (c *scriptCtx) firstString(call *gotreesitter.Node) string {
	if a := c.args(call); len(a) > 0 {
		return c.str(a[0])
	}
	return ""
}

// handler returns the method a connect() wires up and whether it belongs
// to this script: the name string of Godot 3 `connect("sig", target,
// "method")` (self's only when target is the identifier self), or the
// callable identifier of Godot 4 `sig.connect(method)` / `connect("sig",
// method)`, which always names a method of this script.
func (c *scriptCtx) handler(call *gotreesitter.Node) (string, bool) {
	a := c.args(call)
	if len(a) >= 3 {
		if s := c.str(a[2]); s != "" {
			return s, c.typ(a[1]) == "identifier" && c.text(a[1]) == "self"
		}
	}
	for _, n := range a {
		if c.typ(n) == "identifier" && c.text(n) != "self" {
			return c.text(n), true
		}
	}
	return "", false
}

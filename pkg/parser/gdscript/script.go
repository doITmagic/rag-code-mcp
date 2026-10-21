package gdscript

import (
	"strings"
	"unicode"

	"github.com/odvcencio/gotreesitter"

	pkgParser "github.com/doITmagic/rag-code-mcp/pkg/parser"
)

// builtinTypes are GDScript built-in types that never resolve to a symbol in
// the workspace, so they are not recorded as uses_type relations.
var builtinTypes = map[string]struct{}{
	"void": {}, "bool": {}, "int": {}, "float": {}, "String": {}, "StringName": {},
	"NodePath": {}, "Variant": {}, "Array": {}, "Dictionary": {}, "Callable": {},
	"Signal": {}, "Object": {}, "RID": {}, "Vector2": {}, "Vector2i": {},
	"Vector3": {}, "Vector3i": {}, "Vector4": {}, "Vector4i": {}, "Color": {},
	"Rect2": {}, "Rect2i": {}, "Transform2D": {}, "Transform3D": {}, "Basis": {},
	"Quaternion": {}, "Plane": {}, "AABB": {}, "Projection": {},
	"PackedByteArray": {}, "PackedInt32Array": {}, "PackedInt64Array": {},
	"PackedFloat32Array": {}, "PackedFloat64Array": {}, "PackedStringArray": {},
	"PackedVector2Array": {}, "PackedVector3Array": {}, "PackedColorArray": {},
}

// classScope is the class that owns the members being extracted: the script
// itself, or an inner class declared with `class`.
type classScope struct {
	name        string
	qualified   string
	extends     string
	extendsPath string
	// members are the methods and signals the class declares itself.
	members map[string]bool
}

type scriptParser struct {
	src     []byte
	lang    *gotreesitter.Language
	path    string
	pkg     string
	symbols []pkgParser.Symbol
	// inner maps an inner class name to its qualified name ("" when two
	// inner classes share the name).
	inner map[string]string
}

func (a *Analyzer) parseScript(source []byte, path string) ([]pkgParser.Symbol, error) {
	var symbols []pkgParser.Symbol
	err := a.parse(source, path, func(root *gotreesitter.Node, lang *gotreesitter.Language) {
		sp := &scriptParser{src: source, lang: lang, path: path}
		symbols = sp.parseFile(root)
	})
	return symbols, err
}

func (sp *scriptParser) typ(n *gotreesitter.Node) string { return n.Type(sp.lang) }

func (sp *scriptParser) text(n *gotreesitter.Node) string {
	if n == nil {
		return ""
	}
	return n.Text(sp.src)
}

func (sp *scriptParser) field(n *gotreesitter.Node, name string) *gotreesitter.Node {
	return n.ChildByFieldName(name, sp.lang)
}

// parseFile emits one class symbol for the script (every GDScript file is a
// class, named by class_name or, failing that, by its file name) followed by
// its members.
func (sp *scriptParser) parseFile(root *gotreesitter.Node) []pkgParser.Symbol {
	var (
		className   string
		extends     string
		extendsPath string
		isTool      bool
		annotations []string
	)
	for i := 0; i < root.ChildCount(); i++ {
		child := root.Child(i)
		switch sp.typ(child) {
		case "class_name_statement":
			className = sp.text(sp.field(child, "name"))
			// Godot 4 allows `class_name X extends Y` on one line.
			for j := 0; j < child.ChildCount(); j++ {
				if sp.typ(child.Child(j)) == "extends_statement" {
					extends, extendsPath = sp.extendsTarget(child.Child(j))
				}
			}
		case "extends_statement":
			extends, extendsPath = sp.extendsTarget(child)
		case "annotation":
			if !sp.fileLevelAnnotation(root, i) {
				continue
			}
			name := sp.annotationName(child)
			if name == "tool" {
				isTool = true
			}
			annotations = append(annotations, name)
		case "expression_statement":
			// Godot 3 marks editor scripts with a bare `tool` line.
			if strings.TrimSpace(sp.text(child)) == "tool" {
				isTool = true
			}
		}
	}

	name := className
	if name == "" {
		name = fileStem(sp.path)
	}
	sp.pkg = name
	sp.inner = map[string]string{}
	sp.collectInnerClasses(root, name)
	scope := classScope{name: name, qualified: name, extends: extends, extendsPath: extendsPath}

	var sig strings.Builder
	if className != "" {
		sig.WriteString("class_name " + className)
	}
	if extends != "" || extendsPath != "" {
		if sig.Len() > 0 {
			sig.WriteString(" ")
		}
		if extendsPath != "" {
			sig.WriteString(`extends "` + extendsPath + `"`)
		} else {
			sig.WriteString("extends " + extends)
		}
	}
	if sig.Len() == 0 {
		sig.WriteString("extends RefCounted")
	}

	scriptSym := pkgParser.Symbol{
		Name:          name,
		QualifiedName: name,
		Type:          pkgParser.Class,
		Package:       name,
		Content:       string(sp.src),
		Signature:     sig.String(),
		StartLine:     1,
		EndLine:       int(root.EndPoint().Row) + 1,
		FilePath:      sp.path,
		Language:      "gdscript",
		IsPublic:      true,
		Metadata: map[string]any{
			"gdscript_kind": "script",
			"class_name":    className,
			"extends":       extends,
			"extends_path":  extendsPath,
			"is_tool":       isTool,
			"annotations":   annotations,
		},
	}
	if extends != "" {
		scriptSym.Relations = append(scriptSym.Relations, pkgParser.Relation{TargetName: extends, Type: pkgParser.RelInheritance})
	} else if extendsPath != "" {
		scriptSym.Relations = append(scriptSym.Relations, pkgParser.Relation{TargetName: fileStem(extendsPath), Type: pkgParser.RelInheritance})
	}

	scriptDoc := sp.parseBody(root, scope)
	scriptSym.Docstring = scriptDoc
	return append([]pkgParser.Symbol{scriptSym}, sp.symbols...)
}

// parseBody extracts the members of a script or inner class body and returns
// the class documentation: the first `##` block not attached to a member.
func (sp *scriptParser) parseBody(body *gotreesitter.Node, scope classScope) string {
	var (
		classDoc    []string
		docLines    []string
		docEndRow   = -2
		pendingAnno []string
		seenMember  bool
		// The last member symbol and the row it ends on, for inline docs.
		lastIdx    = -1
		lastEndRow = -1
	)
	scope.members = sp.declared(body)
	flushDoc := func() {
		if len(docLines) > 0 && !seenMember && len(classDoc) == 0 {
			classDoc = docLines
		}
		docLines = nil
	}
	takeDoc := func(n *gotreesitter.Node) string {
		doc := ""
		if len(docLines) > 0 && docEndRow == int(n.StartPoint().Row)-1 {
			doc = strings.Join(docLines, "\n")
			docLines = nil
		}
		flushDoc()
		seenMember = true
		return doc
	}

	for i := 0; i < body.ChildCount(); i++ {
		child := body.Child(i)
		switch sp.typ(child) {
		case "comment":
			line, ok := docCommentLine(sp.text(child))
			if !ok {
				continue
			}
			row := int(child.StartPoint().Row)
			if row == lastEndRow {
				// `var a = 1 ## doc` documents the member on its own line.
				if lastIdx >= 0 && sp.symbols[lastIdx].Docstring == "" {
					sp.symbols[lastIdx].Docstring = line
				}
				continue
			}
			if row != docEndRow+1 {
				flushDoc()
			}
			docLines = append(docLines, line)
			docEndRow = row
		case "annotation":
			// A doc block stays attached through the annotation lines
			// between it and its member (`## doc` / `@export_range(...)` / `var`).
			if len(docLines) > 0 && int(child.StartPoint().Row) == docEndRow+1 {
				docEndRow = int(child.EndPoint().Row)
			}
			// A standalone annotation line (e.g. `@rpc` above a func) applies
			// to the next member; file-level ones were read by parseFile.
			if !sp.fileLevelAnnotation(body, i) {
				pendingAnno = append(pendingAnno, sp.annotationName(child))
			}
		case "function_definition", "constructor_definition":
			sp.addFunction(child, scope, takeDoc(child), pendingAnno)
			pendingAnno = nil
		case "signal_statement":
			sp.addSignal(child, scope, takeDoc(child))
			pendingAnno = nil
		case "enum_definition":
			sp.addEnum(child, scope, takeDoc(child))
			pendingAnno = nil
		case "const_statement":
			sp.addConst(child, scope, takeDoc(child))
			pendingAnno = nil
		case "variable_statement", "export_variable_statement", "onready_variable_statement":
			sp.addVariable(child, scope, takeDoc(child), pendingAnno)
			pendingAnno = nil
		case "class_definition":
			sp.addInnerClass(child, scope, takeDoc(child))
			pendingAnno = nil
			lastIdx, lastEndRow = -1, -1
			continue
		default:
			continue
		}
		if t := sp.typ(child); t != "comment" && t != "annotation" {
			lastIdx, lastEndRow = len(sp.symbols)-1, int(child.EndPoint().Row)
		}
	}
	flushDoc()
	return strings.Join(classDoc, "\n")
}

func isFileAnnotation(name string) bool {
	switch name {
	case "tool", "icon", "static_unload", "abstract":
		return true
	}
	return false
}

// fileLevelAnnotation reports whether the standalone annotation at body's
// child i applies to the script rather than to the next member: a script
// annotation (`@tool`, `@icon`, `@abstract`, ...) followed by class_name /
// extends or by nothing. `@abstract` above a func belongs to the func.
func (sp *scriptParser) fileLevelAnnotation(body *gotreesitter.Node, i int) bool {
	if !isFileAnnotation(sp.annotationName(body.Child(i))) {
		return false
	}
	for j := i + 1; j < body.ChildCount(); j++ {
		switch sp.typ(body.Child(j)) {
		case "comment", "annotation":
			continue
		case "class_name_statement", "extends_statement", "expression_statement":
			return true
		default:
			return false
		}
	}
	return true
}

// declared returns the methods and signals a class body declares itself.
func (sp *scriptParser) declared(body *gotreesitter.Node) map[string]bool {
	names := map[string]bool{}
	for i := 0; i < body.ChildCount(); i++ {
		c := body.Child(i)
		switch sp.typ(c) {
		case "function_definition", "constructor_definition", "signal_statement":
			if name := sp.text(sp.field(c, "name")); name != "" {
				names[name] = true
			} else if sp.typ(c) == "constructor_definition" {
				names["_init"] = true
			}
		}
	}
	return names
}

// collectInnerClasses records every inner class under n by name.
func (sp *scriptParser) collectInnerClasses(n *gotreesitter.Node, qualified string) {
	for i := 0; i < n.ChildCount(); i++ {
		c := n.Child(i)
		if sp.typ(c) != "class_definition" {
			continue
		}
		name := sp.text(sp.field(c, "name"))
		if name == "" {
			continue
		}
		q := qualified + "." + name
		if _, dup := sp.inner[name]; dup {
			sp.inner[name] = ""
		} else {
			sp.inner[name] = q
		}
		if body := sp.field(c, "body"); body != nil {
			sp.collectInnerClasses(body, q)
		}
	}
}

// docCommentLine strips the `##` marker of a GDScript documentation comment.
func docCommentLine(comment string) (string, bool) {
	if !strings.HasPrefix(comment, "##") {
		return "", false
	}
	line := strings.TrimRight(strings.TrimPrefix(comment, "##"), "\r")
	return strings.TrimPrefix(line, " "), true
}

func (sp *scriptParser) annotationName(n *gotreesitter.Node) string {
	for i := 0; i < n.ChildCount(); i++ {
		if c := n.Child(i); sp.typ(c) == "identifier" {
			return sp.text(c)
		}
	}
	return strings.TrimPrefix(sp.text(n), "@")
}

// annotations returns the names in a member's `annotations` child
// (`@export`, `@onready`, `@rpc`, ...).
func (sp *scriptParser) annotations(n *gotreesitter.Node) []string {
	var names []string
	for i := 0; i < n.ChildCount(); i++ {
		c := n.Child(i)
		if sp.typ(c) != "annotations" {
			continue
		}
		for j := 0; j < c.ChildCount(); j++ {
			if a := c.Child(j); sp.typ(a) == "annotation" {
				names = append(names, sp.annotationName(a))
			}
		}
	}
	return names
}

// extendsTarget returns the class an extends_statement names, or the script
// path for `extends "res://base.gd"`.
func (sp *scriptParser) extendsTarget(n *gotreesitter.Node) (string, string) {
	for i := 0; i < n.ChildCount(); i++ {
		c := n.Child(i)
		switch sp.typ(c) {
		case "type", "identifier", "attribute":
			return sp.text(c), ""
		case "string":
			return "", strings.Trim(sp.text(c), `"'`)
		}
	}
	return "", ""
}

func (sp *scriptParser) hasChild(n *gotreesitter.Node, typ string) bool {
	for i := 0; i < n.ChildCount(); i++ {
		if sp.typ(n.Child(i)) == typ {
			return true
		}
	}
	return false
}

// header returns the source of n up to its body, without the trailing colon:
// `static func create() -> Player`.
func (sp *scriptParser) header(n *gotreesitter.Node) string {
	end := n.EndByte()
	if body := sp.field(n, "body"); body != nil {
		end = body.StartByte()
	}
	h := strings.TrimSpace(string(sp.src[n.StartByte():end]))
	return strings.TrimSpace(strings.TrimSuffix(h, ":"))
}

// firstLine returns the first line of n's source, for single-line signatures
// of statements that may carry a setget block.
func (sp *scriptParser) firstLine(n *gotreesitter.Node) string {
	line, _, _ := strings.Cut(sp.text(n), "\n")
	return strings.TrimSuffix(strings.TrimSpace(line), ":")
}

func (sp *scriptParser) newMember(n *gotreesitter.Node, name string, typ pkgParser.SymbolType, scope classScope, doc string) pkgParser.Symbol {
	return pkgParser.Symbol{
		Name:          name,
		QualifiedName: scope.qualified + "." + name,
		Type:          typ,
		Package:       sp.pkg,
		Content:       sp.text(n),
		Docstring:     doc,
		StartLine:     int(n.StartPoint().Row) + 1,
		EndLine:       int(n.EndPoint().Row) + 1,
		FilePath:      sp.path,
		Language:      "gdscript",
		IsPublic:      !strings.HasPrefix(name, "_"),
		Metadata:      map[string]any{"class": scope.name},
	}
}

func (sp *scriptParser) addFunction(n *gotreesitter.Node, scope classScope, doc string, pending []string) {
	name := sp.text(sp.field(n, "name"))
	if name == "" {
		name = "_init"
	}
	sym := sp.newMember(n, name, pkgParser.Method, scope, doc)
	sym.Signature = sp.header(n)
	annos := append(append([]string(nil), pending...), sp.annotations(n)...)
	isStatic := sp.hasChild(n, "static_keyword")
	sym.Metadata["gdscript_kind"] = "func"
	sym.Metadata["is_static"] = isStatic
	sym.Metadata["annotations"] = annos
	sym.Metadata["return_type"] = sp.text(sp.field(n, "return_type"))

	rels := &relationSet{}
	if params := sp.field(n, "parameters"); params != nil {
		sp.collectTypes(params, rels)
	}
	if ret := sp.field(n, "return_type"); ret != nil {
		sp.collectTypes(ret, rels)
	}
	if body := sp.field(n, "body"); body != nil {
		sp.collectCalls(body, scope, rels)
	}
	sym.Relations = rels.list
	sp.symbols = append(sp.symbols, sym)
}

func (sp *scriptParser) addSignal(n *gotreesitter.Node, scope classScope, doc string) {
	name := sp.text(sp.field(n, "name"))
	if name == "" {
		return
	}
	sym := sp.newMember(n, name, "signal", scope, doc)
	// Signals have no body, so a multi-line declaration collapses to one line.
	sym.Signature = strings.Join(strings.Fields(sp.text(n)), " ")
	sym.Signature = strings.ReplaceAll(strings.ReplaceAll(sym.Signature, "( ", "("), " )", ")")
	sym.Metadata["gdscript_kind"] = "signal"
	rels := &relationSet{}
	if params := sp.field(n, "parameters"); params != nil {
		sp.collectTypes(params, rels)
	}
	sym.Relations = rels.list
	sp.symbols = append(sp.symbols, sym)
}

func (sp *scriptParser) addEnum(n *gotreesitter.Node, scope classScope, doc string) {
	var values []string
	if list := sp.field(n, "body"); list != nil {
		for i := 0; i < list.ChildCount(); i++ {
			e := list.Child(i)
			if sp.typ(e) != "enumerator" {
				continue
			}
			values = append(values, sp.text(sp.field(e, "left")))
		}
	}

	name := sp.text(sp.field(n, "name"))
	if name == "" {
		// An unnamed enum declares its values as class constants.
		for _, v := range values {
			sym := sp.newMember(n, v, pkgParser.Const, scope, doc)
			sym.Signature = sp.firstLine(n)
			sym.Metadata["gdscript_kind"] = "enum_value"
			sp.symbols = append(sp.symbols, sym)
		}
		return
	}
	sym := sp.newMember(n, name, "enum", scope, doc)
	sym.Signature = sp.firstLine(n)
	sym.Metadata["gdscript_kind"] = "enum"
	sym.Metadata["values"] = values
	sp.symbols = append(sp.symbols, sym)
}

func (sp *scriptParser) addConst(n *gotreesitter.Node, scope classScope, doc string) {
	name := sp.text(sp.field(n, "name"))
	if name == "" {
		return
	}
	sym := sp.newMember(n, name, pkgParser.Const, scope, doc)
	sym.Signature = sp.firstLine(n)
	sym.Metadata["gdscript_kind"] = "const"
	rels := &relationSet{}
	if t := sp.field(n, "type"); t != nil {
		sp.collectTypes(t, rels)
	}
	sym.Relations = rels.list
	sp.symbols = append(sp.symbols, sym)
}

func (sp *scriptParser) addVariable(n *gotreesitter.Node, scope classScope, doc string, pending []string) {
	name := sp.text(sp.field(n, "name"))
	if name == "" {
		return
	}
	sym := sp.newMember(n, name, pkgParser.Var, scope, doc)
	sym.Signature = sp.firstLine(n)
	annos := append(append([]string(nil), pending...), sp.annotations(n)...)
	// Godot 3 spells these as keywords: `export var`, `onready var`.
	if sp.typ(n) == "export_variable_statement" {
		annos = append(annos, "export")
	}
	if sp.typ(n) == "onready_variable_statement" {
		annos = append(annos, "onready")
	}
	exported, onready := false, false
	for _, a := range annos {
		// @export_range, @export_file, ... export; @export_group and
		// friends only label the inspector section that follows.
		switch a {
		case "export_group", "export_subgroup", "export_category":
		default:
			if strings.HasPrefix(a, "export") {
				exported = true
			}
		}
		if a == "onready" {
			onready = true
		}
	}
	varType := ""
	if t := sp.field(n, "type"); t != nil && sp.typ(t) != "inferred_type" {
		varType = sp.text(t)
	}
	sym.Metadata["gdscript_kind"] = "var"
	sym.Metadata["type"] = varType
	sym.Metadata["annotations"] = annos
	sym.Metadata["is_exported"] = exported
	sym.Metadata["is_onready"] = onready
	sym.Metadata["is_static"] = sp.field(n, "static") != nil || sp.hasChild(n, "static_keyword")
	sym.Metadata["has_setget"] = sp.field(n, "setget") != nil

	rels := &relationSet{}
	if t := sp.field(n, "type"); t != nil {
		sp.collectTypes(t, rels)
	}
	if setget := sp.field(n, "setget"); setget != nil {
		sp.collectCalls(setget, scope, rels)
		// Godot 3 `setget set_hp, get_hp` names the accessor methods.
		for i := 0; i < setget.ChildCount(); i++ {
			if c := setget.Child(i); sp.typ(c) == "setter" || sp.typ(c) == "getter" {
				accessor := strings.TrimSpace(sp.text(c))
				rels.add(pkgParser.Relation{TargetName: accessor, Type: pkgParser.RelCalls, TargetQualifiedName: sp.selfTarget(scope, accessor)})
			}
		}
	}
	sym.Relations = rels.list
	sp.symbols = append(sp.symbols, sym)
}

func (sp *scriptParser) addInnerClass(n *gotreesitter.Node, scope classScope, doc string) {
	name := sp.text(sp.field(n, "name"))
	if name == "" {
		return
	}
	inner := classScope{name: name, qualified: scope.qualified + "." + name}
	sym := sp.newMember(n, name, pkgParser.Class, scope, doc)
	sym.Signature = sp.header(n)
	sym.Metadata["gdscript_kind"] = "inner_class"
	sym.Metadata["outer_class"] = scope.name
	delete(sym.Metadata, "class")
	if ext := sp.field(n, "extends"); ext != nil {
		extends, extendsPath := sp.extendsTarget(ext)
		sym.Metadata["extends"] = extends
		inner.extends, inner.extendsPath = extends, extendsPath
		switch {
		case extends != "":
			sym.Relations = append(sym.Relations, pkgParser.Relation{TargetName: extends, Type: pkgParser.RelInheritance})
		case extendsPath != "":
			sym.Relations = append(sym.Relations, pkgParser.Relation{TargetName: fileStem(extendsPath), Type: pkgParser.RelInheritance})
		}
	}
	// Reserve the class's slot so it precedes its members.
	idx := len(sp.symbols)
	sp.symbols = append(sp.symbols, sym)
	if body := sp.field(n, "body"); body != nil {
		if classDoc := sp.parseBody(body, inner); sp.symbols[idx].Docstring == "" {
			sp.symbols[idx].Docstring = classDoc
		}
	}
}

// relationSet collects relations without duplicates, in first-seen order.
type relationSet struct {
	seen map[pkgParser.Relation]struct{}
	list []pkgParser.Relation
}

func (r *relationSet) add(rel pkgParser.Relation) {
	if rel.TargetName == "" {
		return
	}
	if r.seen == nil {
		r.seen = make(map[pkgParser.Relation]struct{})
	}
	if _, ok := r.seen[rel]; ok {
		return
	}
	r.seen[rel] = struct{}{}
	r.list = append(r.list, rel)
}

// collectTypes records uses_type relations for the non-builtin types named
// under n (parameter, return and variable types).
func (sp *scriptParser) collectTypes(n *gotreesitter.Node, rels *relationSet) {
	if sp.typ(n) == "type" {
		// `Array[Enemy]` names Array and Enemy; `Foo.Bar` is one type.
		name := sp.text(n)
		if base, inner, ok := strings.Cut(name, "["); ok {
			sp.addType(base, rels)
			sp.addType(strings.TrimSuffix(inner, "]"), rels)
		} else {
			sp.addType(name, rels)
		}
		return
	}
	for i := 0; i < n.ChildCount(); i++ {
		sp.collectTypes(n.Child(i), rels)
	}
}

func (sp *scriptParser) addType(name string, rels *relationSet) {
	name = strings.TrimSpace(name)
	if name == "" {
		return
	}
	if _, ok := builtinTypes[name]; ok {
		return
	}
	rels.add(pkgParser.Relation{TargetName: name, Type: pkgParser.RelUsesType})
}

// collectCalls records calls made under n, each with the qualified name it
// resolves to when that is known statically, so the indexer never binds a
// call to a same-named method of an unrelated class:
//   - `foo()` and `self.foo()` target the enclosing class when it declares
//     foo, else its base class (`<Base>.foo`).
//   - `super.foo()` targets the base class.
//   - `Utils.foo()`, `Inner.foo()` target that class_name / inner class.
//   - Any other receiver (`sprite.play()`, `$Anim.play()`,
//     `get_node("x").stop()`) keeps the receiver and stays unresolved.
//
// Signal emissions also count as calls to the signal (`died.emit()`,
// `emit_signal("died")`), and `sig.connect(_on_handler)` as a call to the
// handler, so find-usages links handlers to where they are wired up.
func (sp *scriptParser) collectCalls(n *gotreesitter.Node, scope classScope, rels *relationSet) {
	self := func(name string) {
		rels.add(pkgParser.Relation{TargetName: name, Type: pkgParser.RelCalls, TargetQualifiedName: sp.selfTarget(scope, name)})
	}
	switch sp.typ(n) {
	case "call":
		if fn := n.Child(0); fn != nil && sp.typ(fn) == "identifier" {
			name := sp.text(fn)
			self(name)
			switch name {
			case "emit_signal":
				if sig := sp.firstStringArg(n); sig != "" {
					self(sig)
				}
			case "connect":
				if handler := sp.connectHandler(n); handler != "" {
					self(handler)
				}
			}
		}
	case "attribute_call":
		if fn := n.Child(0); fn != nil && sp.typ(fn) == "identifier" {
			name := sp.text(fn)
			receiver := sp.receiver(n)
			rels.add(pkgParser.Relation{
				TargetName:          name,
				Type:                pkgParser.RelCalls,
				Receiver:            receiver,
				TargetQualifiedName: sp.receiverTarget(receiver, scope, name),
			})
			switch {
			case name == "new" && isDottedName(receiver):
				sp.addType(receiver, rels)
			case name == "emit" && isIdentifier(receiver):
				self(receiver)
			case name == "emit_signal":
				if sig := sp.firstStringArg(n); sig != "" && receiver == "self" {
					self(sig)
				}
			case name == "connect":
				if handler := sp.connectHandler(n); handler != "" {
					self(handler)
				}
			}
		}
	}
	for i := 0; i < n.ChildCount(); i++ {
		sp.collectCalls(n.Child(i), scope, rels)
	}
}

// receiver returns the source of what an attribute_call is called on: the
// part of the enclosing attribute chain before its dot (`sprite`,
// `$Anim`, `get_tree()`, `Outer.Inner`), whitespace-collapsed.
func (sp *scriptParser) receiver(call *gotreesitter.Node) string {
	dot := call.PrevSibling()
	parent := call.Parent()
	if dot == nil || sp.typ(dot) != "." || parent == nil {
		return ""
	}
	recv := strings.Join(strings.Fields(string(sp.src[parent.StartByte():dot.StartByte()])), " ")
	if len(recv) > 120 {
		recv = recv[:120]
	}
	return recv
}

// selfTarget returns the qualified name of a call on the enclosing class.
func (sp *scriptParser) selfTarget(scope classScope, method string) string {
	if scope.members[method] {
		return scope.qualified + "." + method
	}
	if base := sp.baseTarget(scope, method); base != "" {
		return base
	}
	// Not declared here and no known base: a qualified name that matches
	// nothing keeps the call unresolved rather than bound by name alone.
	return scope.qualified + "." + method
}

// baseTarget returns the qualified name of method on the class's base.
func (sp *scriptParser) baseTarget(scope classScope, method string) string {
	switch {
	case scope.extends != "":
		return sp.classTarget(scope.extends, method)
	case scope.extendsPath != "":
		return fileStem(scope.extendsPath) + "." + method
	}
	return ""
}

func (sp *scriptParser) receiverTarget(receiver string, scope classScope, method string) string {
	switch {
	case receiver == "self":
		return sp.selfTarget(scope, method)
	case receiver == "super":
		return sp.baseTarget(scope, method)
	case isDottedName(receiver) && receiver[0] >= 'A' && receiver[0] <= 'Z':
		return sp.classTarget(receiver, method)
	}
	return ""
}

// classTarget qualifies method on a class reference: an inner class of this
// script (`Inner`, `Inner.Deep`) or a global class_name. Built-in engine
// types resolve to nothing.
func (sp *scriptParser) classTarget(class, method string) string {
	first, rest, _ := strings.Cut(class, ".")
	if q, ok := sp.inner[first]; ok {
		if q == "" {
			return ""
		}
		if rest != "" {
			q += "." + rest
		}
		return q + "." + method
	}
	if _, builtin := builtinTypes[class]; builtin {
		return ""
	}
	return class + "." + method
}

func isIdentifier(s string) bool {
	if s == "" {
		return false
	}
	for i, r := range s {
		if r != '_' && !unicode.IsLetter(r) && (i == 0 || !unicode.IsDigit(r)) {
			return false
		}
	}
	return true
}

// isDottedName reports whether s is a chain of identifiers (`Foo.Bar`).
func isDottedName(s string) bool {
	if s == "" {
		return false
	}
	for _, part := range strings.Split(s, ".") {
		if !isIdentifier(part) {
			return false
		}
	}
	return true
}

func (sp *scriptParser) arguments(call *gotreesitter.Node) []*gotreesitter.Node {
	args := sp.field(call, "arguments")
	if args == nil {
		return nil
	}
	var out []*gotreesitter.Node
	for i := 0; i < args.ChildCount(); i++ {
		if a := args.Child(i); a.IsNamed() && sp.typ(a) != "comment" {
			out = append(out, a)
		}
	}
	return out
}

func (sp *scriptParser) firstStringArg(call *gotreesitter.Node) string {
	args := sp.arguments(call)
	if len(args) == 0 {
		return ""
	}
	switch sp.typ(args[0]) {
	case "string", "string_name":
		return strings.Trim(strings.TrimPrefix(sp.text(args[0]), "&"), `"'`)
	}
	return ""
}

// connectHandler returns the method a connect() call wires up: the callable
// in Godot 4 (`pressed.connect(_on_pressed)`, `connect(self._on_pressed)`) or
// the method-name string in Godot 3 (`connect("pressed", self, "_on_pressed")`).
func (sp *scriptParser) connectHandler(call *gotreesitter.Node) string {
	args := sp.arguments(call)
	if len(args) == 0 {
		return ""
	}
	if len(args) >= 3 && sp.typ(args[2]) == "string" {
		return strings.Trim(sp.text(args[2]), `"'`)
	}
	for _, a := range args {
		switch sp.typ(a) {
		case "identifier":
			if name := sp.text(a); name != "self" {
				return name
			}
		case "attribute":
			// self._on_pressed: the handler is the last name.
			last := a.Child(a.ChildCount() - 1)
			if last != nil && sp.typ(last) == "identifier" {
				return sp.text(last)
			}
		}
	}
	return ""
}

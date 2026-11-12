package golang

import (
	"fmt"
	"maps"
	"strconv"
	"strings"

	"github.com/doITmagic/rag-code-mcp/v2/parser"
	"github.com/odvcencio/gotreesitter"
)

// framework is the route vocabulary of one HTTP router, used only in a file
// that imports it: a method named GET on another type is no route elsewhere.
type framework struct {
	name    string
	module  string          // import path without a /vN suffix
	methods map[string]int  // registration method -> index of its path argument
	group   string          // method returning a sub-router prefixed by its path argument
	last    bool            // the handler is the last argument (after middleware), not the one after the path
	inline  map[string]bool // methods returning a router with the receiver's prefix (chi With, Group)
}

var frameworks = []framework{
	{name: "echo", module: "github.com/labstack/echo", group: "Group", methods: map[string]int{
		"GET": 0, "POST": 0, "PUT": 0, "DELETE": 0, "PATCH": 0, "OPTIONS": 0, "HEAD": 0, "CONNECT": 0, "TRACE": 0,
		"Any": 0, "Match": 1, "Add": 1, "Static": 0, "File": 0,
	}},
	{name: "gin", module: "github.com/gin-gonic/gin", group: "Group", last: true, methods: map[string]int{
		"GET": 0, "POST": 0, "PUT": 0, "DELETE": 0, "PATCH": 0, "OPTIONS": 0, "HEAD": 0,
		"Any": 0, "Match": 1, "Handle": 1, "Static": 0, "StaticFile": 0, "StaticFS": 0,
	}},
	{name: "chi", module: "github.com/go-chi/chi", group: "Route", inline: map[string]bool{"With": true, "Group": true}, methods: map[string]int{
		"Get": 0, "Post": 0, "Put": 0, "Delete": 0, "Patch": 0, "Options": 0, "Head": 0, "Connect": 0, "Trace": 0,
		"Handle": 0, "HandleFunc": 0, "Method": 1, "MethodFunc": 1, "Mount": 0,
	}},
}

// noHandler are registration methods whose argument after the path is not a
// handler (a directory or file name).
var noHandler = map[string]bool{"Static": true, "File": true, "StaticFile": true, "StaticFS": true}

// addFramework records the router an import path belongs to.
func (fc *file) addFramework(path string) {
	if i := strings.LastIndexByte(path, '/'); i > 0 && len(path) > i+2 && path[i+1] == 'v' && strings.Trim(path[i+2:], "0123456789") == "" {
		path = path[:i]
	}
	for i := range frameworks {
		if frameworks[i].module == path {
			fc.routers = append(fc.routers, &frameworks[i])
		}
	}
}

// routes returns one route symbol per registration call in the function and
// method declarations decls; syms[at[i]] is the symbol of decls[i]. A group
// variable passed to a function of this file gives that function's parameter
// its prefix when every call agrees; a few rounds follow such chains.
func (fc *file) routes(decls []*gotreesitter.Node, at []int, syms []parser.Symbol) []parser.Symbol {
	var out []parser.Symbol
	params := map[string]map[int]string{}
	for round := 0; round < 4; round++ {
		out = out[:0]
		seen := map[string]map[int]string{}
		for i, d := range decls {
			var p map[int]string
			if d.Type(fc.lang) == "function_declaration" {
				p = params[syms[at[i]].Name]
			}
			out = fc.declRoutes(d, &syms[at[i]], p, seen, out)
		}
		if fmt.Sprint(seen) == fmt.Sprint(params) { // fmt prints maps sorted
			break
		}
		params = seen
	}
	return out
}

// declRoutes walks one declaration in source order, tracking the prefix of
// each group variable (by its text, so s.api works too).
func (fc *file) declRoutes(decl *gotreesitter.Node, sym *parser.Symbol, params map[int]string, seen map[string]map[int]string, out []parser.Symbol) []parser.Symbol {
	groups := map[string]string{}
	i := 0
	for _, pd := range parser.NamedChildren(parser.Field(decl, fc.lang, "parameters")) {
		for _, name := range fc.names(pd) {
			if p, ok := params[i]; ok {
				groups[name] = p
			}
			i++
		}
	}
	var walk func(n *gotreesitter.Node, groups map[string]string)
	walk = func(n *gotreesitter.Node, groups map[string]string) {
		parser.Walk(n, func(c *gotreesitter.Node) bool {
			switch c.Type(fc.lang) {
			case "short_var_declaration", "assignment_statement":
				l := parser.NamedChildren(parser.Field(c, fc.lang, "left"))
				r := parser.NamedChildren(parser.Field(c, fc.lang, "right"))
				if len(l) == 1 && len(r) == 1 {
					if p, ok := fc.groupPrefix(r[0], groups); ok {
						groups[fc.text(l[0])] = p
					}
				}
			case "call_expression":
				fn := parser.Field(c, fc.lang, "function")
				args := parser.NamedChildren(parser.Field(c, fc.lang, "arguments"))
				switch fn.Type(fc.lang) {
				case "identifier": // f(g): g's prefix reaches f's parameter
					name := fc.text(fn)
					for j, a := range args {
						p := fc.prefix(a, groups) // "" for anything that is no group
						if seen[name] == nil {
							seen[name] = map[int]string{}
						}
						if q, dup := seen[name][j]; dup && q != p {
							p = "" // calls disagree: the path stays as written
						}
						seen[name][j] = p
					}
				case "selector_expression":
					if body, param, p, ok := fc.scope(c, fn, args, groups); ok {
						inner := maps.Clone(groups) // the closure's parameter shadows an outer router
						inner[param] = p
						walk(body, inner)
						return false
					}
					if s, ok := fc.route(c, fn, args, groups, sym); ok {
						out = append(out, s)
					}
				}
			}
			return true
		})
	}
	walk(parser.Field(decl, fc.lang, "body"), groups)
	return out
}

// scope matches chi's r.Route("/x", func(r chi.Router) {...}) and
// r.Group(func(r chi.Router) {...}): the closure body, its router
// parameter's name and that router's prefix.
func (fc *file) scope(call, fn *gotreesitter.Node, args []*gotreesitter.Node, groups map[string]string) (*gotreesitter.Node, string, string, bool) {
	if len(args) == 0 || args[len(args)-1].Type(fc.lang) != "func_literal" {
		return nil, "", "", false
	}
	lit := args[len(args)-1]
	var param string
	if pds := parser.NamedChildren(parser.Field(lit, fc.lang, "parameters")); len(pds) > 0 {
		if names := fc.names(pds[0]); len(names) > 0 {
			param = names[0]
		}
	}
	method := fc.text(parser.Field(fn, fc.lang, "field"))
	for _, fw := range fc.routers {
		if fw.inline == nil || param == "" {
			continue
		}
		if fw.inline[method] {
			return parser.Field(lit, fc.lang, "body"), param, fc.prefix(parser.Field(fn, fc.lang, "operand"), groups), true
		}
		if p, ok := fc.groupPrefix(call, groups); ok {
			return parser.Field(lit, fc.lang, "body"), param, p, true
		}
	}
	return nil, "", "", false
}

// route builds the symbol of "recv.Method(path, handler, ...)" when Method
// is a registration method of a router the file imports and path is a
// string literal that looks like one.
func (fc *file) route(call, fn *gotreesitter.Node, args []*gotreesitter.Node, groups map[string]string, sym *parser.Symbol) (parser.Symbol, bool) {
	method := fc.text(parser.Field(fn, fc.lang, "field"))
	for _, fw := range fc.routers {
		idx, ok := fw.methods[method]
		if !ok || idx >= len(args) {
			continue
		}
		path, ok := fc.pathLiteral(args[idx])
		if !ok {
			continue
		}
		var handler *gotreesitter.Node
		if !noHandler[method] {
			if idx+1 >= len(args) {
				continue
			}
			if handler = args[idx+1]; strings.HasSuffix(handler.Type(fc.lang), "string_literal") {
				continue // Header().Add("k", "v")
			}
			if fw.last {
				handler = args[len(args)-1]
			}
		}
		name := fc.prefix(parser.Field(fn, fc.lang, "operand"), groups) + path
		if name == "" {
			name = "/"
		}
		s := fc.base(call, name, parser.KindRoute, sym.Name)
		s.Exported = !fc.test
		s.Metadata = map[string]string{"framework": fw.name, "method": method}
		if r, ok := fc.handlerRelation(handler, sym.Relations); ok {
			s.Relations = []parser.Relation{r}
		}
		return s, true
	}
	return parser.Symbol{}, false
}

// handlerRelation is the value relation the enclosing declaration already
// has for the handler argument (a function, method value or package
// function); an inline func literal or an operand of unknown type has none.
func (fc *file) handlerRelation(h *gotreesitter.Node, rels []parser.Relation) (parser.Relation, bool) {
	if h == nil {
		return parser.Relation{}, false
	}
	if h.Type(fc.lang) == "selector_expression" {
		h = parser.Field(h, fc.lang, "field")
	} else if h.Type(fc.lang) != "identifier" {
		return parser.Relation{}, false
	}
	name := fc.text(h)
	line, _ := parser.Lines(h)
	for _, r := range rels {
		if r.Value && r.Line == line && r.TargetName == name {
			return r, true
		}
	}
	return parser.Relation{}, false
}

// groupPrefix returns the full prefix of "recv.Group(path, ...)".
func (fc *file) groupPrefix(e *gotreesitter.Node, groups map[string]string) (string, bool) {
	if e.Type(fc.lang) != "call_expression" {
		return "", false
	}
	fn := parser.Field(e, fc.lang, "function")
	if fn.Type(fc.lang) != "selector_expression" {
		return "", false
	}
	method := fc.text(parser.Field(fn, fc.lang, "field"))
	for _, fw := range fc.routers {
		if fw.group != method {
			continue
		}
		args := parser.NamedChildren(parser.Field(e, fc.lang, "arguments"))
		if len(args) == 0 {
			continue
		}
		if path, ok := fc.pathLiteral(args[0]); ok {
			return fc.prefix(parser.Field(fn, fc.lang, "operand"), groups) + path, true
		}
	}
	return "", false
}

// prefix is the path prefix of a router expression: a tracked group
// variable, an inline Group(...) call, "" for anything else (the root, or a
// router whose prefix this file does not show).
func (fc *file) prefix(recv *gotreesitter.Node, groups map[string]string) string {
	if p, ok := fc.groupPrefix(recv, groups); ok {
		return p
	}
	if recv.Type(fc.lang) == "call_expression" { // r.With(mw).Get(...)
		if fn := parser.Field(recv, fc.lang, "function"); fn.Type(fc.lang) == "selector_expression" {
			for _, fw := range fc.routers {
				if fw.inline[fc.text(parser.Field(fn, fc.lang, "field"))] {
					return fc.prefix(parser.Field(fn, fc.lang, "operand"), groups)
				}
			}
		}
	}
	return groups[fc.text(recv)]
}

// pathLiteral unquotes a string literal that is "" or starts with "/".
func (fc *file) pathLiteral(n *gotreesitter.Node) (string, bool) {
	if !strings.HasSuffix(n.Type(fc.lang), "string_literal") {
		return "", false
	}
	s, err := strconv.Unquote(fc.text(n))
	if err != nil || s != "" && s[0] != '/' {
		return "", false
	}
	return s, true
}

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
	pattern bool            // the path may be a Go 1.22 pattern: "GET /items/{id}", "host.com/x"
	types   map[string]bool // router types: a parameter, var or field of one is a router
	ctors   map[string]bool // package functions returning a router
}

var frameworks = []framework{
	{name: "echo", module: "github.com/labstack/echo", group: "Group", methods: map[string]int{
		"GET": 0, "POST": 0, "PUT": 0, "DELETE": 0, "PATCH": 0, "OPTIONS": 0, "HEAD": 0, "CONNECT": 0, "TRACE": 0,
		"Any": 0, "Match": 1, "Add": 1, "Static": 0, "File": 0,
	}, types: map[string]bool{"Echo": true, "Group": true}, ctors: map[string]bool{"New": true}},
	{name: "gin", module: "github.com/gin-gonic/gin", group: "Group", last: true, methods: map[string]int{
		"GET": 0, "POST": 0, "PUT": 0, "DELETE": 0, "PATCH": 0, "OPTIONS": 0, "HEAD": 0,
		"Any": 0, "Match": 1, "Handle": 1, "Static": 0, "StaticFile": 0, "StaticFS": 0,
	}, types: map[string]bool{"Engine": true, "RouterGroup": true, "IRouter": true, "IRoutes": true}, ctors: map[string]bool{"New": true, "Default": true}},
	{name: "chi", module: "github.com/go-chi/chi", group: "Route", inline: map[string]bool{"With": true, "Group": true}, methods: map[string]int{
		"Get": 0, "Post": 0, "Put": 0, "Delete": 0, "Patch": 0, "Options": 0, "Head": 0, "Connect": 0, "Trace": 0,
		"Handle": 0, "HandleFunc": 0, "Method": 1, "MethodFunc": 1, "Mount": 0,
	}, types: map[string]bool{"Router": true, "Mux": true}, ctors: map[string]bool{"NewRouter": true, "NewMux": true}},
	{name: "net/http", module: "net/http", pattern: true, methods: map[string]int{"Handle": 0, "HandleFunc": 0},
		types: map[string]bool{"ServeMux": true}, ctors: map[string]bool{"NewServeMux": true}},
}

// noHandler are registration methods whose argument after the path is not a
// handler (a directory or file name).
var noHandler = map[string]bool{"Static": true, "File": true, "StaticFile": true, "StaticFS": true}

// addFramework records the router an import path belongs to and returns it,
// nil for any other path.
func (fc *file) addFramework(path string) *framework {
	if i := strings.LastIndexByte(path, '/'); i > 0 && len(path) > i+2 && path[i+1] == 'v' && strings.Trim(path[i+2:], "0123456789") == "" {
		path = path[:i]
	}
	for i := range frameworks {
		switch {
		case frameworks[i].module != path:
		case frameworks[i].pattern: // net/http last: a chi or gin Handle in a file importing both is theirs
			fc.routers = append(fc.routers, &frameworks[i])
			return &frameworks[i]
		default:
			fc.routers = append([]*framework{&frameworks[i]}, fc.routers...)
			return &frameworks[i]
		}
	}
	return nil
}

// routerType is true for a type naming a router type of an imported
// framework: *echo.Group, chi.Router.
func (fc *file) routerType(t *gotreesitter.Node) bool {
	t = fc.bare(t)
	if t == nil || t.Type(fc.lang) != "qualified_type" {
		return false
	}
	fw := fc.fwPkgs[fc.text(parser.Field(t, fc.lang, "package"))]
	return fw != nil && fw.types[fc.text(parser.Field(t, fc.lang, "name"))]
}

// newRouter is true for a call of a framework's router constructor:
// echo.New(), chi.NewRouter(), http.NewServeMux().
func (fc *file) newRouter(e *gotreesitter.Node) bool {
	if e.Type(fc.lang) != "call_expression" {
		return false
	}
	fn := parser.Field(e, fc.lang, "function")
	if fn.Type(fc.lang) != "selector_expression" {
		return false
	}
	fw := fc.fwPkgs[fc.text(parser.Field(fn, fc.lang, "operand"))]
	return fw != nil && fw.ctors[fc.text(parser.Field(fn, fc.lang, "field"))]
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
		typed := fc.routerType(parser.Field(pd, fc.lang, "type"))
		for _, name := range fc.names(pd) {
			if typed {
				groups[name] = params[i] // "" when no call in the file shows its prefix
			}
			i++
		}
	}
	// declared holds the names declared with := in the scope n opens, so an
	// inner api := r.Group("/x") ends with its block.
	var walk func(n *gotreesitter.Node, groups map[string]string, declared map[string]bool)
	walk = func(n *gotreesitter.Node, groups map[string]string, declared map[string]bool) {
		parser.Walk(n, func(c *gotreesitter.Node) bool {
			switch c.Type(fc.lang) {
			case "block", "if_statement", "for_statement", "expression_switch_statement", "type_switch_statement", "select_statement":
				if c == n {
					return true
				}
				inner, innerDeclared := maps.Clone(groups), map[string]bool{}
				walk(c, inner, innerDeclared)
				for k, v := range inner { // an assignment to an outer variable outlives the block
					if !innerDeclared[k] {
						groups[k] = v
					}
				}
				return false
			case "short_var_declaration", "assignment_statement":
				short := c.Type(fc.lang) == "short_var_declaration"
				l := parser.NamedChildren(parser.Field(c, fc.lang, "left"))
				r := parser.NamedChildren(parser.Field(c, fc.lang, "right"))
				p, ok := "", false
				if len(l) == 1 && len(r) == 1 {
					p, ok = fc.router(r[0], groups)
				}
				for _, v := range l {
					switch name := fc.text(v); {
					case ok:
						groups[name] = p
					case short: // a new variable that is no group shadows an outer one
						delete(groups, name)
					}
					if short {
						declared[fc.text(v)] = true
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
						walk(body, inner, map[string]bool{})
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
	walk(parser.Field(decl, fc.lang, "body"), groups, map[string]bool{})
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
		if p, ok := fc.router(parser.Field(fn, fc.lang, "operand"), groups); ok && fw.inline[method] {
			return parser.Field(lit, fc.lang, "body"), param, p, true
		}
		if p, ok := fc.groupPrefix(call, groups); ok {
			return parser.Field(lit, fc.lang, "body"), param, p, true
		}
	}
	return nil, "", "", false
}

// route builds the symbol of "recv.Method(path, handler, ...)" when recv is
// a router the file shows, Method is a registration method of a router the
// file imports and path is a string literal that looks like one.
func (fc *file) route(call, fn *gotreesitter.Node, args []*gotreesitter.Node, groups map[string]string, sym *parser.Symbol) (parser.Symbol, bool) {
	prefix, ok := fc.router(parser.Field(fn, fc.lang, "operand"), groups)
	if !ok { // c.File(path) on an echo.Context, cache.Get(key, v)
		return parser.Symbol{}, false
	}
	method := fc.text(parser.Field(fn, fc.lang, "field"))
	for _, fw := range fc.routers {
		idx, ok := fw.methods[method]
		if !ok || idx >= len(args) {
			continue
		}
		path, ok := fc.pathLiteral(args[idx], fw.pattern)
		if !ok {
			continue
		}
		verb := method
		if v, rest, cut := strings.Cut(path, " "); cut && fw.pattern {
			verb, path = v, strings.TrimLeft(rest, " ")
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
		name := prefix + path
		if name == "" {
			name = "/"
		}
		s := fc.base(call, name, parser.KindRoute, sym.Name)
		s.Exported = !fc.test
		s.Metadata = map[string]string{"framework": fw.name, "method": verb}
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

// groupPrefix returns the full prefix of "recv.Group(path, ...)" on a router.
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
		if path, ok := fc.pathLiteral(args[0], false); ok {
			if p, ok := fc.router(parser.Field(fn, fc.lang, "operand"), groups); ok {
				return p + path, true
			}
		}
	}
	return "", false
}

// router returns the path prefix of a router expression and whether the
// file shows it is a router of an imported framework: a tracked group or
// router variable, a Group(...) or With(...) call on one, a constructor call,
// a top-level var or a struct field of a router type, the net/http package.
// The prefix is "" for the root or a router whose prefix the file does not
// show (a parameter, a field set elsewhere).
// shortcut: a local that is no router shadows a top-level router var or the
// http import of its name only in the tracked groups, not in that fallback.
func (fc *file) router(e *gotreesitter.Node, groups map[string]string) (string, bool) {
	if p, ok := fc.groupPrefix(e, groups); ok {
		return p, true
	}
	switch e.Type(fc.lang) {
	case "call_expression": // r.With(mw).Get(...)
		if fc.newRouter(e) {
			return "", true
		}
		if fn := parser.Field(e, fc.lang, "function"); fn.Type(fc.lang) == "selector_expression" {
			for _, fw := range fc.routers {
				if fw.inline[fc.text(parser.Field(fn, fc.lang, "field"))] {
					return fc.router(parser.Field(fn, fc.lang, "operand"), groups)
				}
			}
		}
	case "selector_expression":
		if p, ok := groups[fc.text(e)]; ok {
			return p, true
		}
		return "", fc.routerFields[fc.text(parser.Field(e, fc.lang, "field"))]
	case "identifier":
		name := fc.text(e)
		if p, ok := groups[name]; ok {
			return p, true
		}
		fw := fc.fwPkgs[name]
		return "", fc.routerVars[name] || fw != nil && fw.pattern // http.HandleFunc
	}
	return "", false
}

// prefix is the path prefix of a router expression, "" for anything else.
func (fc *file) prefix(recv *gotreesitter.Node, groups map[string]string) string {
	p, _ := fc.router(recv, groups)
	return p
}

// pathLiteral unquotes a string literal that is "" or starts with "/"; a
// pattern may also be "[METHOD ][host]/path" (net/http since Go 1.22).
func (fc *file) pathLiteral(n *gotreesitter.Node, pattern bool) (string, bool) {
	if !strings.HasSuffix(n.Type(fc.lang), "string_literal") {
		return "", false
	}
	s, err := strconv.Unquote(fc.text(n))
	if err != nil {
		return "", false
	}
	p := s
	if v, rest, cut := strings.Cut(s, " "); pattern && cut && v != "" && strings.ToUpper(v) == v {
		p = strings.TrimLeft(rest, " ")
	}
	if host, _, ok := strings.Cut(p, "/"); pattern && ok && strings.Contains(host, ".") {
		p = "/" // a host pattern: example.com/x
	}
	if s != "" && (p == "" || p[0] != '/') {
		return "", false
	}
	return s, true
}

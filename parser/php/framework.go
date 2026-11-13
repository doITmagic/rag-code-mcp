package php

import (
	"cmp"
	"strings"

	"github.com/doITmagic/rag-code-mcp/v2/parser"
	"github.com/odvcencio/gotreesitter"
)

var routeMethods = map[string]bool{
	"get": true, "post": true, "put": true, "patch": true, "delete": true, "options": true,
	"any": true, "match": true, "resource": true, "apiResource": true, "group": true,
	"view": true, "redirect": true,
}

// eloquentRelations are the Eloquent methods whose first argument (and second
// for the *Through ones) is a model class.
var eloquentRelations = map[string]bool{
	"hasOne": true, "hasMany": true, "belongsTo": true, "belongsToMany": true,
	"hasOneThrough": true, "hasManyThrough": true, "morphOne": true, "morphMany": true,
	"morphToMany": true, "morphedByMany": true,
}

func isRouteRecv(recv, recvFQN string) bool {
	return recv == "Route" || recvFQN == `Illuminate\Support\Facades\Route`
}

// frameworkSymbols derives Laravel route and WordPress hook symbols from the
// calls seen while walking. The call shape is the only evidence: no project
// detection.
func frameworkSymbols(calls []rawCall) []parser.Symbol {
	var groups []rawCall // Route groups adding a prefix or namespace, outer first (the walk is preorder)
	for _, call := range calls {
		if call.name == "group" && isRouteRecv(call.recv, call.recvFQN) && (call.route.prefix != "" || call.route.ns != "" || call.route.ctrlFQN != "" || call.route.name != "") {
			groups = append(groups, call)
		}
	}
	var out []parser.Symbol
	for _, call := range calls {
		if call.arg == "" {
			continue
		}
		s := parser.Symbol{
			Name: call.arg, Module: call.ns, Owner: call.owner, Content: call.content,
			StartLine: call.start, EndLine: call.end, Exported: true,
		}
		s.Signature, _, _ = strings.Cut(call.content, "\n")
		switch {
		case isRouteRecv(call.recv, call.recvFQN) && routeMethods[call.name]:
			s.Kind = parser.KindRoute
			s.Metadata = map[string]string{"framework": "laravel", "method": call.name}
			var name string
			s.Name, name, s.Relations = route(call, groups)
			if name != "" {
				s.Metadata["name"] = name
			}
		case call.recv == "" && (call.name == "add_action" || call.name == "add_filter"):
			s.Kind = parser.KindHook
			s.Metadata = map[string]string{"framework": "wordpress", "kind": strings.TrimPrefix(call.name, "add_")}
		default:
			continue
		}
		out = append(out, s)
	}
	return out
}

// route returns the route's URI with the prefixes of the groups around it,
// normalized as Laravel registers it (Route::get('users') is /users), its
// name with the groups' name prefixes ("" when it has none of its own)
// and the relation to its handler: a call to the controller method, a
// uses_type to a resource or invokable controller, an import of a view.
func route(call rawCall, groups []rawCall) (string, string, []parser.Relation) {
	var prefixes, nss []string
	var names string     // the groups' name prefixes, joined as Laravel does: admin. + posts.
	var groupCtrl string // the innermost Route::controller(C::class) group's C
	for _, g := range groups {
		if g.from <= call.from && call.to <= g.to && (g.from != call.from || g.to != call.to) {
			groupCtrl = cmp.Or(g.route.ctrlFQN, groupCtrl)
			names += g.route.name
			if p := strings.Trim(g.route.prefix, "/"); p != "" {
				prefixes = append(prefixes, p)
			}
			if ns := strings.Trim(g.route.ns, `\`); ns != "" {
				nss = append(nss, ns)
			}
		}
	}
	// As Laravel registers it: prefix and uri trimmed of slashes, joined, a leading slash.
	if u := strings.Trim(call.arg, "/"); u != "" {
		prefixes = append(prefixes, u)
	}
	uri := "/" + strings.Join(prefixes, "/")
	r, line := call.route, call.start
	name := ""
	if r.name != "" {
		name = names + r.name
	}
	if groupCtrl != "" && r.ctrl != "" && r.action == "" && !strings.Contains(r.ctrl, `\`) && call.name != "resource" && call.name != "apiResource" {
		// Inside a controller group a string action without @ is a method of it.
		r.ctrlFQN, r.ctrl, r.action = groupCtrl, "", r.ctrl
	}
	fqn := r.ctrlFQN
	switch {
	case r.view != "":
		return uri, name, []parser.Relation{{Kind: parser.RelImports, TargetName: r.view, TargetQualifiedName: r.view, Line: line}}
	case fqn == "" && r.ctrl == "":
		return uri, name, nil
	case fqn == "" && strings.HasPrefix(r.ctrl, `\`):
		fqn = r.ctrl[1:]
	case fqn == "":
		// The application's controller namespace (RouteServiceProvider) is in
		// another file, so the target is a qualified suffix the indexer matches.
		fqn = strings.Join(append(nss, r.ctrl), `\`)
	}
	if r.action == "" {
		return uri, name, []parser.Relation{{Kind: parser.RelUsesType, TargetName: short(fqn), TargetQualifiedName: qualified(fqn), Line: line}}
	}
	return uri, name, []parser.Relation{{Kind: parser.RelCall, TargetName: r.action, TargetQualifiedName: qualified(fqn) + "." + r.action, Line: line}}
}

// routeInfo reads a Route::<name>(...) call: the prefix and namespace of a
// group, the handler of a route (the argument after the URI, at pos+1).
func (c *collector) routeInfo(n *gotreesitter.Node, name string, pos int, owner string) routeInfo {
	var ri routeInfo
	args := c.args(n)
	switch {
	case !routeMethods[name] || name == "redirect":
	case name == "group":
		if len(args) > 0 && c.typ(args[0]) == "array_creation_expression" {
			keyed, _ := c.arrayElems(args[0])
			ri.prefix, ri.ns = c.literal(keyed["prefix"]), c.literal(keyed["namespace"])
			ri.ctrlFQN = c.controllerClass(keyed["controller"], owner)
			ri.name = c.literal(keyed["as"])
		}
	case pos+1 >= len(args):
	case name == "view":
		ri.view = c.literal(args[pos+1])
	default:
		c.handler(args[pos+1], owner, &ri)
	}
	return ri
}

// handler reads a route action: 'C@m', C::class, [C::class, 'm'] (also
// ['App\C', 'm']) or an
// array with 'uses' => one of those. A closure or anything else sets nothing.
func (c *collector) handler(h *gotreesitter.Node, owner string, ri *routeInfo) {
	switch c.typ(h) {
	case "string", "encapsed_string":
		ctrl, action, _ := strings.Cut(c.literal(h), "@")
		if className(ctrl) {
			ri.ctrl, ri.action = ctrl, action
		}
	case "class_constant_access_expression":
		ri.ctrlFQN = c.classConst(h, owner)
	case "array_creation_expression":
		keyed, list := c.arrayElems(h)
		ri.name = c.literal(keyed["as"])
		if u := keyed["uses"]; u != nil {
			c.handler(u, owner, ri)
		} else if len(list) == 2 {
			if fqn := c.controllerClass(list[0], owner); fqn != "" {
				ri.ctrlFQN, ri.action = fqn, c.literal(list[1])
			}
		}
	}
}

// classConst resolves C::class to the class's fully qualified name, "" for
// anything else.
func (c *collector) classConst(n *gotreesitter.Node, owner string) string {
	if c.typ(n) != "class_constant_access_expression" || n.NamedChildCount() != 2 || c.text(n.NamedChild(1)) != "class" {
		return ""
	}
	cls := n.NamedChild(0)
	if t := c.typ(cls); t != "name" && t != "qualified_name" {
		return ""
	}
	return c.resolve(c.text(cls), c.classes, owner)
}

// controllerClass is the class a controller value names: C::class resolved,
// or a class string ('App\Http\C'), always fully qualified at runtime.
func (c *collector) controllerClass(n *gotreesitter.Node, owner string) string {
	if fqn := c.classConst(n, owner); fqn != "" {
		return fqn
	}
	if lit := c.literal(n); className(lit) {
		return strings.TrimPrefix(lit, `\`)
	}
	return ""
}

// arrayElems splits an array literal into its string-keyed values and its
// unkeyed values, in order.
func (c *collector) arrayElems(arr *gotreesitter.Node) (map[string]*gotreesitter.Node, []*gotreesitter.Node) {
	keyed := map[string]*gotreesitter.Node{}
	var list []*gotreesitter.Node
	for _, el := range parser.NamedChildren(arr) {
		if el.Type(c.lang) != "array_element_initializer" {
			continue
		}
		switch el.NamedChildCount() {
		case 1:
			list = append(list, el.NamedChild(0))
		case 2:
			if k := c.literal(el.NamedChild(0)); k != "" {
				keyed[k] = el.NamedChild(1)
			}
		}
	}
	return keyed, list
}

// fluentGroup records Route::prefix('x')->namespace('N')->controller(C::class)
// ->...->group(fn) as a group rawCall spanning the closure, so the routes
// inside take its prefix and controller.
func (c *collector) fluentGroup(n *gotreesitter.Node) {
	var prefixes, nss []string
	var ctrl, names string
	for o := c.field(n, "object"); ; {
		t := c.typ(o)
		if t != "member_call_expression" && t != "scoped_call_expression" {
			return
		}
		switch arg := c.stringArg(o, 0); c.text(c.field(o, "name")) {
		case "prefix":
			prefixes = append([]string{arg}, prefixes...)
		case "namespace":
			nss = append([]string{arg}, nss...)
		case "name":
			names = arg + names
		case "controller":
			if args := c.args(o); len(args) > 0 {
				ctrl = c.controllerClass(args[0], "")
			}
		}
		if t == "scoped_call_expression" {
			recv := c.text(c.field(o, "scope"))
			if isRouteRecv(recv, c.resolve(recv, c.classes, "")) {
				c.calls = append(c.calls, rawCall{recv: "Route", name: "group", from: n.StartByte(), to: n.EndByte(),
					route: routeInfo{prefix: strings.Join(prefixes, "/"), ns: strings.Join(nss, `\`), ctrlFQN: ctrl, name: names}})
			}
			return
		}
		o = c.field(o, "object")
	}
}

// routeName records ->name('x') of a Route::get(...)->...->name('x') chain
// for the route call at its root, which the walk reaches after this call.
// Each ->name() appends to the name as Laravel's Route::name does; the walk
// is preorder, so the last call of the chain comes first and an earlier one
// goes in front: ->name('users.')->name('index') is users.index.
func (c *collector) routeName(n *gotreesitter.Node) {
	o := c.field(n, "object")
	for c.typ(o) == "member_call_expression" {
		o = c.field(o, "object")
	}
	if c.typ(o) == "scoped_call_expression" {
		c.names[o.StartByte()] = c.stringArg(n, 0) + c.names[o.StartByte()]
	}
}

// className reports whether s can be a class name as written in a route
// action string (App\Http\C), so a URI or a template name is not taken.
func className(s string) bool {
	for _, r := range s {
		if r != '\\' && r != '_' && !('a' <= r && r <= 'z' || 'A' <= r && r <= 'Z' || '0' <= r && r <= '9') {
			return false
		}
	}
	return s != ""
}

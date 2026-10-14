package laravel

import (
	"fmt"
	"os"
	"strings"

	"github.com/VKCOM/php-parser/pkg/ast"
	"github.com/VKCOM/php-parser/pkg/conf"
	"github.com/VKCOM/php-parser/pkg/errors"
	"github.com/VKCOM/php-parser/pkg/parser"
	"github.com/VKCOM/php-parser/pkg/version"
	"github.com/VKCOM/php-parser/pkg/visitor"

	"github.com/doITmagic/rag-code-mcp/internal/logger"
)

// RouteAnalyzer parses Laravel route files
type RouteAnalyzer struct {
	astHelper *ASTPropertyExtractor
}

// NewRouteAnalyzer creates a new route analyzer
func NewRouteAnalyzer() *RouteAnalyzer {
	return &RouteAnalyzer{
		astHelper: NewASTPropertyExtractor(),
	}
}

// Analyze parses the given route files and returns extracted routes
func (ra *RouteAnalyzer) Analyze(filePaths []string) ([]Route, error) {
	var allRoutes []Route

	for _, path := range filePaths {
		routes, err := ra.analyzeFile(path)
		if err != nil {
			// Log error but continue
			logger.Instance.Warn("[LARAVEL] Error analyzing route file %s: %v", path, err)
			continue
		}
		allRoutes = append(allRoutes, routes...)
	}

	return allRoutes, nil
}

func (ra *RouteAnalyzer) analyzeFile(filePath string) ([]Route, error) {
	content, err := os.ReadFile(filePath)
	if err != nil {
		return nil, err
	}

	// Parse PHP
	var parserErrors []*errors.Error
	rootNode, err := parser.Parse(content, conf.Config{
		Version: &version.Version{Major: 8, Minor: 0},
		ErrorHandlerFunc: func(e *errors.Error) {
			parserErrors = append(parserErrors, e)
		},
	})

	if err != nil {
		return nil, err
	}

	collector := &routeCollector{
		routes:    []Route{},
		filePath:  filePath,
		astHelper: ra.astHelper,
	}

	collector.walk(rootNode)

	return collector.routes, nil
}

// GroupContext holds state for nested route groups
type GroupContext struct {
	Prefix     string
	Namespace  string
	Middleware []string
	NamePrefix string
}

// routeCollector visits the AST to find Route definitions
type routeCollector struct {
	visitor.Null
	routes    []Route
	filePath  string
	astHelper *ASTPropertyExtractor

	groupStack []GroupContext
}

func (v *routeCollector) walk(node ast.Vertex) {
	if node == nil {
		return
	}
	v.EnterNode(node)
	defer v.LeaveNode(node)

	switch n := node.(type) {
	case *ast.Root:
		for _, stmt := range n.Stmts {
			v.walk(stmt)
		}
	case *ast.StmtStmtList:
		for _, stmt := range n.Stmts {
			v.walk(stmt)
		}
	case *ast.StmtNamespace:
		for _, stmt := range n.Stmts {
			v.walk(stmt)
		}
	case *ast.StmtExpression:
		v.walk(n.Expr)
	case *ast.StmtIf:
		v.walk(n.Stmt)
		for _, elseIf := range n.ElseIf {
			v.walk(elseIf)
		}
		v.walk(n.Else)
	case *ast.StmtElseIf:
		v.walk(n.Stmt)
	case *ast.StmtElse:
		v.walk(n.Stmt)
	case *ast.ExprStaticCall:
		v.ExprStaticCall(n)
		for _, arg := range n.Args {
			v.walk(arg)
		}
	case *ast.ExprMethodCall:
		v.ExprMethodCall(n)
		v.walk(n.Var)
		for _, arg := range n.Args {
			v.walk(arg)
		}
	case *ast.Argument:
		v.walk(n.Expr)
	case *ast.ExprClosure:
		for _, stmt := range n.Stmts {
			v.walk(stmt)
		}
	}
}

func (v *routeCollector) EnterNode(n ast.Vertex) bool {
	if call, ok := n.(*ast.ExprStaticCall); ok {
		className := v.extractName(call.Class)
		if className == "Route" || strings.HasSuffix(className, "\\Route") {
			methodName := v.extractIdentifier(call.Call)
			if methodName == "group" {
				v.pushGroupContext(call.Args)
				return true
			}
		}
	}
	if methodCall, ok := n.(*ast.ExprMethodCall); ok {
		if v.extractIdentifier(methodCall.Method) == "group" && v.isRouteFluentChain(methodCall) {
			v.pushFluentGroupContext(methodCall)
			return true
		}
	}
	return true
}

func (v *routeCollector) LeaveNode(n ast.Vertex) {
	if call, ok := n.(*ast.ExprStaticCall); ok {
		className := v.extractName(call.Class)
		if className == "Route" || strings.HasSuffix(className, "\\Route") {
			methodName := v.extractIdentifier(call.Call)
			if methodName == "group" {
				v.popGroupContext()
			}
		}
	}
	if methodCall, ok := n.(*ast.ExprMethodCall); ok {
		if v.extractIdentifier(methodCall.Method) == "group" && v.isRouteFluentChain(methodCall) {
			v.popGroupContext()
		}
	}
}

func (v *routeCollector) isRouteFluentChain(n ast.Vertex) bool {
	curr := n
	for curr != nil {
		switch c := curr.(type) {
		case *ast.ExprMethodCall:
			curr = c.Var
		case *ast.ExprStaticCall:
			className := v.extractName(c.Class)
			return className == "Route" || strings.HasSuffix(className, "\\Route")
		default:
			return false
		}
	}
	return false
}

type fluentStep struct {
	method string
	args   []ast.Vertex
}

func (v *routeCollector) pushFluentGroupContext(groupCall *ast.ExprMethodCall) {
	ctx := v.inheritGroupContext()

	var steps []fluentStep
	curr := groupCall.Var
	for curr != nil {
		switch c := curr.(type) {
		case *ast.ExprMethodCall:
			steps = append(steps, fluentStep{
				method: v.extractIdentifier(c.Method),
				args:   c.Args,
			})
			curr = c.Var
		case *ast.ExprStaticCall:
			steps = append(steps, fluentStep{
				method: v.extractIdentifier(c.Call),
				args:   c.Args,
			})
			curr = nil
		default:
			curr = nil
		}
	}

	// Apply steps from innermost (Route::...) to outermost before ->group(...)
	for i := len(steps) - 1; i >= 0; i-- {
		v.applyFluentStep(&ctx, steps[i].method, steps[i].args)
	}

	// Also check if ->group(...) itself received an attributes array as first argument
	v.applyGroupArrayArgs(&ctx, groupCall.Args)

	v.groupStack = append(v.groupStack, ctx)
}

func (v *routeCollector) applyFluentStep(ctx *GroupContext, method string, args []ast.Vertex) {
	if len(args) == 0 {
		return
	}
	switch method {
	case "prefix":
		val := v.extractString(args[0])
		if val != "" {
			if ctx.Prefix != "" {
				ctx.Prefix = strings.TrimRight(ctx.Prefix, "/") + "/" + strings.TrimLeft(val, "/")
			} else {
				ctx.Prefix = val
			}
		}
	case "name", "as":
		val := v.extractString(args[0])
		ctx.NamePrefix += val
	case "namespace":
		val := v.extractString(args[0])
		if val != "" {
			if ctx.Namespace != "" {
				ctx.Namespace += "\\" + val
			} else {
				ctx.Namespace = val
			}
		}
	case "middleware":
		if str := v.extractString(args[0]); str != "" {
			ctx.Middleware = append(ctx.Middleware, str)
		} else if arr := v.extractStringArray(args[0]); arr != nil {
			ctx.Middleware = append(ctx.Middleware, arr...)
		}
	}
}

func (v *routeCollector) inheritGroupContext() GroupContext {
	ctx := GroupContext{}
	if len(v.groupStack) > 0 {
		prev := v.groupStack[len(v.groupStack)-1]
		ctx.Prefix = prev.Prefix
		ctx.Namespace = prev.Namespace
		ctx.NamePrefix = prev.NamePrefix
		ctx.Middleware = append([]string(nil), prev.Middleware...)
	}
	return ctx
}

func (v *routeCollector) applyGroupArrayArgs(ctx *GroupContext, args []ast.Vertex) {
	if len(args) > 0 {
		if arg, ok := args[0].(*ast.Argument); ok {
			if exprArray, ok := arg.Expr.(*ast.ExprArray); ok {
				for _, item := range exprArray.Items {
					if arrayItem, ok := item.(*ast.ExprArrayItem); ok {
						key := v.extractString(arrayItem.Key)
						switch key {
						case "prefix":
							val := v.extractString(arrayItem.Val)
							if ctx.Prefix != "" {
								ctx.Prefix = strings.TrimRight(ctx.Prefix, "/") + "/" + strings.TrimLeft(val, "/")
							} else {
								ctx.Prefix = val
							}
						case "as":
							val := v.extractString(arrayItem.Val)
							ctx.NamePrefix += val
						case "namespace":
							val := v.extractString(arrayItem.Val)
							if ctx.Namespace != "" {
								ctx.Namespace += "\\" + val
							} else {
								ctx.Namespace = val
							}
						} else if key == "middleware" {
							if str := v.extractString(arrayItem.Val); str != "" {
								ctx.Middleware = append(ctx.Middleware, str)
							} else if arr := v.extractStringArray(arrayItem.Val); arr != nil {
								ctx.Middleware = append(ctx.Middleware, arr...)
							}
						}
					}
				}
			}
		}
	}
}

func (v *routeCollector) pushGroupContext(args []ast.Vertex) {
	ctx := v.inheritGroupContext()
	v.applyGroupArrayArgs(&ctx, args)
	v.groupStack = append(v.groupStack, ctx)
}

func (v *routeCollector) popGroupContext() {
	if len(v.groupStack) > 0 {
		v.groupStack = v.groupStack[:len(v.groupStack)-1]
	}
}

func (v *routeCollector) applyGroupContext(route *Route) {
	if len(v.groupStack) == 0 {
		return
	}
	ctx := v.groupStack[len(v.groupStack)-1]

	if ctx.Prefix != "" {
		route.URI = strings.TrimRight(ctx.Prefix, "/") + "/" + strings.TrimLeft(route.URI, "/")
		if route.URI == "" {
			route.URI = "/"
		}
	}
	if ctx.Namespace != "" && route.Controller != "" && route.Controller != "Closure" {
		if !strings.HasPrefix(route.Controller, "\\") {
			route.Controller = strings.TrimRight(ctx.Namespace, "\\") + "\\" + route.Controller
		}
	}
	if ctx.NamePrefix != "" {
		route.Name = ctx.NamePrefix + route.Name
	}
	if len(ctx.Middleware) > 0 {
		route.Middleware = append(route.Middleware, ctx.Middleware...)
	}
}

// ExprStaticCall handles Route::get(), Route::post(), etc.
func (v *routeCollector) ExprStaticCall(n *ast.ExprStaticCall) {
	className := v.extractName(n.Class)
	if className != "Route" && !strings.HasSuffix(className, "\\Route") {
		return
	}

	methodName := v.extractIdentifier(n.Call)
	v.dispatchRouteMethod(methodName, n.Args, n.Position.StartLine)
}

// ExprMethodCall handles fluent route definitions like Route::middleware('auth')->get(...)
func (v *routeCollector) ExprMethodCall(n *ast.ExprMethodCall) {
	if !v.isRouteFluentChain(n) {
		return
	}
	methodName := v.extractIdentifier(n.Method)
	switch methodName {
	case "get", "post", "put", "patch", "delete", "options", "any", "match", "resource", "apiResource":
		v.pushFluentGroupContext(n)
		v.dispatchRouteMethod(methodName, n.Args, n.Position.StartLine)
		v.popGroupContext()
	}
}

func (v *routeCollector) dispatchRouteMethod(methodName string, args []ast.Vertex, line int) {
	switch methodName {
	case "get", "post", "put", "patch", "delete", "options", "any":
		v.extractRoute(methodName, args, line)
	case "match":
		v.extractMatchRoute(args, line)
	case "resource":
		v.extractResourceRoute(args, line)
	case "apiResource":
		v.extractApiResourceRoute(args, line)
	}
}

func (v *routeCollector) extractRoute(method string, args []ast.Vertex, line int) {
	if len(args) < 2 {
		return
	}

	uri := v.extractString(args[0])
	actionArg := args[1]
	controller, action := v.extractAction(actionArg)

	route := Route{
		Method:     strings.ToUpper(method),
		URI:        uri,
		Controller: controller,
		Action:     action,
		FilePath:   v.filePath,
		Line:       line,
	}

	v.applyGroupContext(&route)
	v.routes = append(v.routes, route)
}

func (v *routeCollector) extractMatchRoute(args []ast.Vertex, line int) {
	if len(args) < 3 {
		return
	}

	methods := v.extractStringArray(args[0])
	uri := v.extractString(args[1])
	controller, action := v.extractAction(args[2])

	for _, method := range methods {
		route := Route{
			Method:     strings.ToUpper(method),
			URI:        uri,
			Controller: controller,
			Action:     action,
			FilePath:   v.filePath,
			Line:       line,
		}
		v.applyGroupContext(&route)
		v.routes = append(v.routes, route)
	}
}

func (v *routeCollector) extractResourceRoute(args []ast.Vertex, line int) {
	actions := []struct {
		action string
		method string
	}{
		{"index", "GET"},
		{"create", "GET"},
		{"store", "POST"},
		{"show", "GET"},
		{"edit", "GET"},
		{"update", "PUT/PATCH"},
		{"destroy", "DELETE"},
	}
	v.extractResourceActions(args, line, actions, "Resource")
}

func (v *routeCollector) extractApiResourceRoute(args []ast.Vertex, line int) {
	actions := []struct {
		action string
		method string
	}{
		{"index", "GET"},
		{"store", "POST"},
		{"show", "GET"},
		{"update", "PUT/PATCH"},
		{"destroy", "DELETE"},
	}
	v.extractResourceActions(args, line, actions, "API resource")
}

func (v *routeCollector) extractResourceActions(args []ast.Vertex, line int, actions []struct {
	action string
	method string
}, label string) {
	if len(args) < 2 {
		return
	}

	name := v.extractString(args[0])

	var controller string
	if arg, ok := args[1].(*ast.Argument); ok {
		controller = v.extractControllerName(arg.Expr)
	}

	if controller == "" {
		return
	}

	for _, item := range actions {
		action := item.action
		method := item.method
		uri := name
		if action == "show" || action == "edit" || action == "update" || action == "destroy" {
			uri += "/{id}"
		}
		if action == "edit" {
			uri += "/edit"
		}
		if action == "create" {
			uri += "/create"
		}

		route := Route{
			Method:      method,
			URI:         uri,
			Controller:  controller,
			Action:      action,
			FilePath:    v.filePath,
			Line:        line,
			Description: fmt.Sprintf("%s route for %s.%s", label, name, action),
		}
		v.applyGroupContext(&route)
		v.routes = append(v.routes, route)
	}
}

func (v *routeCollector) extractControllerName(expr ast.Vertex) string {
	if classConst, ok := expr.(*ast.ExprClassConstFetch); ok {
		if name, ok := classConst.Class.(*ast.Name); ok {
			return v.extractName(name)
		} else if ident, ok := classConst.Class.(*ast.Identifier); ok {
			return string(ident.Value)
		}
	} else if str, ok := expr.(*ast.ScalarString); ok {
		val := string(str.Value)
		return strings.Trim(val, "'\"")
	}
	return ""
}

func (v *routeCollector) extractAction(arg ast.Vertex) (string, string) {
	if array, ok := arg.(*ast.Argument); ok {
		if exprArray, ok := array.Expr.(*ast.ExprArray); ok {
			if len(exprArray.Items) >= 2 {
				var controller string
				item0 := exprArray.Items[0].(*ast.ExprArrayItem).Val
				if classConst, ok := item0.(*ast.ExprClassConstFetch); ok {
					if name, ok := classConst.Class.(*ast.Name); ok {
						controller = v.extractName(name)
					} else if ident, ok := classConst.Class.(*ast.Identifier); ok {
						controller = string(ident.Value)
					}
				} else if str, ok := item0.(*ast.ScalarString); ok {
					controller = string(str.Value)
					controller = strings.Trim(controller, "'\"")
				}

				var action string
				item1 := exprArray.Items[1].(*ast.ExprArrayItem).Val
				if str, ok := item1.(*ast.ScalarString); ok {
					action = string(str.Value)
					action = strings.Trim(action, "'\"")
				}

				return controller, action
			}
		}

		if str, ok := array.Expr.(*ast.ScalarString); ok {
			val := string(str.Value)
			val = strings.Trim(val, "'\"")
			parts := strings.Split(val, "@")
			if len(parts) == 2 {
				return parts[0], parts[1]
			}
		}

		if _, ok := array.Expr.(*ast.ExprClosure); ok {
			return "Closure", ""
		}
	}

	return "", ""
}

func (v *routeCollector) extractName(node ast.Vertex) string {
	if node == nil {
		return ""
	}
	switch n := node.(type) {
	case *ast.Name:
		parts := make([]string, 0, len(n.Parts))
		for _, part := range n.Parts {
			if namePart, ok := part.(*ast.NamePart); ok {
				parts = append(parts, string(namePart.Value))
			}
		}
		return strings.Join(parts, "\\")
	case *ast.NameFullyQualified:
		parts := make([]string, 0, len(n.Parts))
		for _, part := range n.Parts {
			if namePart, ok := part.(*ast.NamePart); ok {
				parts = append(parts, string(namePart.Value))
			}
		}
		return "\\" + strings.Join(parts, "\\")
	}
	return ""
}

func (v *routeCollector) extractIdentifier(node ast.Vertex) string {
	if ident, ok := node.(*ast.Identifier); ok {
		return string(ident.Value)
	}
	return ""
}

func (v *routeCollector) extractString(node ast.Vertex) string {
	if node == nil {
		return ""
	}
	if arg, ok := node.(*ast.Argument); ok {
		return v.astHelper.extractStringFromExpr(arg.Expr)
	}
	return v.astHelper.extractStringFromExpr(node)
}

func (v *routeCollector) extractStringArray(node ast.Vertex) []string {
	if node == nil {
		return nil
	}
	if arg, ok := node.(*ast.Argument); ok {
		return v.astHelper.extractStringArrayFromExpr(arg.Expr)
	}
	return v.astHelper.extractStringArrayFromExpr(node)
}

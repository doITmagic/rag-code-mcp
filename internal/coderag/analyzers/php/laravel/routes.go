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
	"github.com/VKCOM/php-parser/pkg/visitor/traverser"
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
			fmt.Fprintf(os.Stderr, "Error analyzing route file %s: %v\n", path, err)
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

	traverser.NewTraverser(collector).Traverse(rootNode)

	return collector.routes, nil
}

// routeCollector visits the AST to find Route definitions
type routeCollector struct {
	visitor.Null
	routes    []Route
	filePath  string
	astHelper *ASTPropertyExtractor

	// Context for groups (prefix, middleware, etc.)
	// For now, we'll implement basic flat route extraction
}

// ExprStaticCall handles Route::get(), Route::post(), etc.
func (v *routeCollector) ExprStaticCall(n *ast.ExprStaticCall) {
	// Check if class is "Route"
	className := v.extractName(n.Class)
	if className != "Route" && !strings.HasSuffix(className, "\\Route") {
		return
	}

	methodName := v.extractIdentifier(n.Call)

	// Handle standard HTTP methods
	switch methodName {
	case "get", "post", "put", "patch", "delete", "options", "any":
		v.extractRoute(methodName, n.Args, n.Position.StartLine)
	case "match":
		// Route::match(['get', 'post'], '/uri', ...)
		v.extractMatchRoute(n.Args, n.Position.StartLine)
	case "resource":
		v.extractResourceRoute(n.Args, n.Position.StartLine)
	case "group":
		// TODO: Handle groups
	}
}

// ExprMethodCall handles chained methods like ->name() or ->middleware()
// Note: The parser visits the AST bottom-up for expressions usually?
// Actually traverser visits top-down.
// For $route->name('foo'), we have a MethodCall where Var is the StaticCall.
// We might need to handle this differently.
// A simpler approach for now is to just catch the creation. Chained attributes are harder in a single pass
// without keeping track of the "current route" object.
// For this iteration, we will focus on the main definition (Method, URI, Controller).

func (v *routeCollector) extractRoute(method string, args []ast.Vertex, line int) {
	if len(args) < 2 {
		return
	}

	// Arg 0: URI
	uri := v.extractString(args[0])

	// Arg 1: Action (Closure or Controller)
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

	v.routes = append(v.routes, route)
}

func (v *routeCollector) extractMatchRoute(args []ast.Vertex, line int) {
	if len(args) < 3 {
		return
	}

	// Arg 0: Methods array
	methods := v.extractStringArray(args[0])
	// Arg 1: URI
	uri := v.extractString(args[1])
	// Arg 2: Action
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
		v.routes = append(v.routes, route)
	}
}

func (v *routeCollector) extractResourceRoute(args []ast.Vertex, line int) {
	if len(args) < 2 {
		return
	}

	name := v.extractString(args[0])

	// Extract controller from second argument
	var controller string
	if arg, ok := args[1].(*ast.Argument); ok {
		controller = v.extractControllerName(arg.Expr)
	}

	if controller == "" {
		return
	}

	// Resource routes create multiple actual routes.
	// For high-level understanding, we can represent it as a special "RESOURCE" method or expand it.
	// Let's expand it to standard REST actions for better searchability.

	actions := map[string]string{
		"index":   "GET",
		"create":  "GET",
		"store":   "POST",
		"show":    "GET",
		"edit":    "GET",
		"update":  "PUT/PATCH",
		"destroy": "DELETE",
	}

	for action, method := range actions {
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
			Description: fmt.Sprintf("Resource route for %s.%s", name, action),
		}
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
	// Handle [Controller::class, 'action']
	if array, ok := arg.(*ast.Argument); ok {
		if exprArray, ok := array.Expr.(*ast.ExprArray); ok {
			if len(exprArray.Items) >= 2 {
				// Item 0: Controller
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

				// Item 1: Action
				var action string
				item1 := exprArray.Items[1].(*ast.ExprArrayItem).Val
				if str, ok := item1.(*ast.ScalarString); ok {
					action = string(str.Value)
					action = strings.Trim(action, "'\"")
				}

				return controller, action
			}
		}

		// Handle 'Controller@action' string
		if str, ok := array.Expr.(*ast.ScalarString); ok {
			val := string(str.Value)
			val = strings.Trim(val, "'\"")
			parts := strings.Split(val, "@")
			if len(parts) == 2 {
				return parts[0], parts[1]
			}
		}

		// Handle Closure
		if _, ok := array.Expr.(*ast.ExprClosure); ok {
			return "Closure", ""
		}
	}

	return "", ""
}

// Helpers (duplicated from analyzer.go or reused if possible, but simpler to duplicate for now)

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
	if arg, ok := node.(*ast.Argument); ok {
		return v.astHelper.extractStringFromExpr(arg.Expr)
	}
	return ""
}

func (v *routeCollector) extractStringArray(node ast.Vertex) []string {
	if arg, ok := node.(*ast.Argument); ok {
		return v.astHelper.extractStringArrayFromExpr(arg.Expr)
	}
	return nil
}

package laravel

import (
	"strings"

	"github.com/VKCOM/php-parser/pkg/ast"
)

// RouteInfo represents extracted Laravel route information
type RouteInfo struct {
	Method     string
	URI        string
	Controller string
	Action     string
}

// ExtractRouteInfo extracts route details from a Route::method static call
func (d *Detector) ExtractRouteInfo(n *ast.ExprStaticCall) *RouteInfo {
	methodName := d.astHelper.ExtractIdentifier(n.Call)
	if methodName == "" {
		return nil
	}

	if len(n.Args) < 2 {
		// Basic check for URI at least
		if len(n.Args) == 1 {
			if arg, ok := n.Args[0].(*ast.Argument); ok {
				return &RouteInfo{
					Method: strings.ToUpper(methodName),
					URI:    d.astHelper.ExtractStringFromExpr(arg.Expr),
				}
			}
		}
		return nil
	}

	// Arg 0: URI
	uri := ""
	if arg0, ok := n.Args[0].(*ast.Argument); ok {
		uri = d.astHelper.ExtractStringFromExpr(arg0.Expr)
	}

	// Arg 1: Action (Closure or Controller)
	controller := ""
	action := ""

	if arg1, ok := n.Args[1].(*ast.Argument); ok {
		controller, action = d.extractAction(arg1.Expr)
	}

	return &RouteInfo{
		Method:     strings.ToUpper(methodName),
		URI:        uri,
		Controller: controller,
		Action:     action,
	}
}

func (d *Detector) extractAction(expr ast.Vertex) (string, string) {
	// Handle [Controller::class, 'action']
	if exprArray, ok := expr.(*ast.ExprArray); ok {
		return d.extractArrayAction(exprArray.Items)
	}

	// Handle 'Controller@action' string
	if str, ok := expr.(*ast.ScalarString); ok {
		val := strings.Trim(string(str.Value), "'\"")
		parts := strings.Split(val, "@")
		if len(parts) == 2 {
			return parts[0], parts[1]
		}
		return "", ""
	}

	return "", ""
}

func (d *Detector) extractArrayAction(items []ast.Vertex) (string, string) {
	if len(items) < 2 {
		return "", ""
	}

	// Item 0: Controller
	controller := ""
	item0 := items[0].(*ast.ExprArrayItem).Val
	if classConst, ok := item0.(*ast.ExprClassConstFetch); ok {
		controller = d.astHelper.ExtractNodeName(classConst.Class)
	} else if str, ok := item0.(*ast.ScalarString); ok {
		controller = strings.Trim(string(str.Value), "'\"")
	}

	// Item 1: Action
	action := ""
	item1 := items[1].(*ast.ExprArrayItem).Val
	if str, ok := item1.(*ast.ScalarString); ok {
		action = strings.Trim(string(str.Value), "'\"")
	}

	return controller, action
}

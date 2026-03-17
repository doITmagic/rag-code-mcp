package woocommerce

import (
	"strconv"
	"strings"

	"github.com/VKCOM/php-parser/pkg/ast"
)

// WPHookInput is a local mirror of wordpress.WPHook to avoid import cycles.
// The wordpress/analyzer.go converts WPHook to WPHookInput before calling AnalyzeHooksFromWP.
type WPHookInput struct {
	Type      string
	Name      string
	Callback  string
	Priority  int
	FilePath  string
	StartLine int
	EndLine   int
}

// areaRule maps a keyword to a WC functional area
type areaRule struct {
	keyword string
	area    WCHookArea
}

// areaRules are checked in order — more specific keywords first to avoid
// ambiguity (e.g., "email" before "order" so "woocommerce_email_order_details"
// correctly maps to email, not order)
var areaRules = []areaRule{
	{"email", WCAreaEmail},
	{"my_account", WCAreaAccount},
	{"account", WCAreaAccount},
	{"shop_order", WCAreaOrder},
	{"checkout", WCAreaCheckout},
	{"cart", WCAreaCart},
	{"product", WCAreaProduct},
	{"admin", WCAreaAdmin},
	{"shipping", WCAreaShipping},
	{"payment", WCAreaPayment},
	{"order", WCAreaOrder},
}

// wcApiFunctions maps WooCommerce API functions to their category
var wcApiFunctions = map[string]string{
	"wc_get_product":           "product",
	"wc_get_products":          "product",
	"wc_get_product_id_by_sku": "product",
	"wc_get_order":             "order",
	"wc_get_orders":            "order",
	"wc_create_order":          "order",
	"wc_get_customer":          "customer",
	"WC":                       "core",
	"wc_add_to_cart_message":   "cart",
	"wc_get_cart_url":          "cart",
	"wc_get_checkout_url":      "checkout",
	"wc_price":                 "formatting",
	"wc_format_decimal":        "formatting",
	"wc_get_template":          "template",
	"wc_get_template_part":     "template",
	"wc_get_page_id":           "page",
	"wc_add_notice":            "notice",
	"wc_print_notices":         "notice",
	"wc_has_notice":            "notice",
	"wc_get_endpoint_url":      "endpoint",
}

// Analyzer detects WooCommerce-specific patterns in WordPress code
type Analyzer struct{}

// NewAnalyzer creates a new WooCommerce analyzer
func NewAnalyzer() *Analyzer {
	return &Analyzer{}
}

// Analyze performs WooCommerce-specific analysis on AST
func (a *Analyzer) Analyze(root ast.Vertex, filePath string) *WooCommerceInfo {
	info := &WooCommerceInfo{}
	a.walk(root, filePath, info)
	return info
}

// AnalyzeHooksFromWP classifies existing WordPress hooks as WooCommerce hooks.
// Uses WPHookInput to avoid import cycle with the wordpress parent package.
func (a *Analyzer) AnalyzeHooksFromWP(wpHooks []WPHookInput) []WCHook {
	var wcHooks []WCHook

	for _, h := range wpHooks {
		if !strings.HasPrefix(h.Name, "woocommerce_") {
			continue
		}

		wcHook := WCHook{
			HookName:  h.Name,
			Area:      classifyHookArea(h.Name),
			HookType:  h.Type,
			Callback:  h.Callback,
			Priority:  h.Priority,
			FilePath:  h.FilePath,
			StartLine: h.StartLine,
			EndLine:   h.EndLine,
		}
		wcHooks = append(wcHooks, wcHook)
	}

	return wcHooks
}

// walk recursively traverses the AST looking for WC-specific patterns
func (a *Analyzer) walk(node ast.Vertex, filePath string, info *WooCommerceInfo) {
	if node == nil {
		return
	}

	switch n := node.(type) {
	case *ast.Root:
		for _, stmt := range n.Stmts {
			a.walk(stmt, filePath, info)
		}
	case *ast.StmtStmtList:
		for _, stmt := range n.Stmts {
			a.walk(stmt, filePath, info)
		}
	case *ast.StmtExpression:
		a.walk(n.Expr, filePath, info)
	case *ast.StmtReturn:
		if n.Expr != nil {
			a.walk(n.Expr, filePath, info)
		}
	case *ast.StmtNamespace:
		for _, stmt := range n.Stmts {
			a.walk(stmt, filePath, info)
		}
	case *ast.StmtClass:
		for _, stmt := range n.Stmts {
			a.walk(stmt, filePath, info)
		}
	case *ast.StmtClassMethod:
		if n.Stmt != nil {
			a.walk(n.Stmt, filePath, info)
		}
	case *ast.StmtFunction:
		if n.Stmts != nil {
			for _, s := range n.Stmts {
				a.walk(s, filePath, info)
			}
		}
	case *ast.StmtIf:
		if n.Stmt != nil {
			a.walk(n.Stmt, filePath, info)
		}
		for _, elseIf := range n.ElseIf {
			a.walk(elseIf, filePath, info)
		}
		if n.Else != nil {
			a.walk(n.Else, filePath, info)
		}
	case *ast.StmtElseIf:
		if n.Stmt != nil {
			a.walk(n.Stmt, filePath, info)
		}
	case *ast.StmtElse:
		if n.Stmt != nil {
			a.walk(n.Stmt, filePath, info)
		}
	case *ast.ExprAssign:
		a.walk(n.Expr, filePath, info)
	case *ast.ExprFunctionCall:
		// Check for WC hooks
		hook := extractHookFromFunctionCall(n, filePath)
		if hook != nil && strings.HasPrefix(hook.HookName, "woocommerce_") {
			info.Hooks = append(info.Hooks, *hook)
		}

		// Check for WC API calls
		funcName := extractFuncName(n.Function)
		if category, ok := wcApiFunctions[funcName]; ok {
			info.APICalls = append(info.APICalls, WCAPICall{
				Function:  funcName,
				Category:  category,
				FilePath:  filePath,
				StartLine: n.Position.StartLine,
				EndLine:   n.Position.EndLine,
			})
		}

	case *ast.ExprStaticCall:
		// Check for WC()->... static calls
		className := extractClassName(n.Class)
		if className == "WC" {
			info.APICalls = append(info.APICalls, WCAPICall{
				Function:  "WC::" + extractMethodName(n.Call),
				Category:  "core",
				FilePath:  filePath,
				StartLine: n.Position.StartLine,
				EndLine:   n.Position.EndLine,
			})
		}
	}
}

// classifyHookArea determines the functional area of a WooCommerce hook
func classifyHookArea(hookName string) WCHookArea {
	// Remove "woocommerce_" prefix for analysis
	name := strings.TrimPrefix(hookName, "woocommerce_")

	for _, rule := range areaRules {
		if strings.Contains(name, rule.keyword) {
			return rule.area
		}
	}

	return WCAreaGeneral
}

// --- Local AST helpers (avoid importing wordpress parent package) ---

// hookFuncMap maps WordPress hook function names to their HookType string
var hookFuncMap = map[string]string{
	"add_action":    "action",
	"add_filter":    "filter",
	"do_action":     "action_trigger",
	"apply_filters": "filter_trigger",
	"remove_action": "action_removal",
	"remove_filter": "filter_removal",
	"has_filter":    "filter_check",
	"has_action":    "action_check",
}

// extractHookFromFunctionCall checks if a function call is a WordPress hook and returns a WCHook if it's WC-related
func extractHookFromFunctionCall(call *ast.ExprFunctionCall, filePath string) *WCHook {
	funcName := extractFuncName(call.Function)
	if funcName == "" {
		return nil
	}

	hookType, ok := hookFuncMap[funcName]
	if !ok {
		return nil
	}

	// Extract arguments
	args := extractCallArgs(call.Args)
	if len(args) == 0 {
		return nil
	}

	hookName := args[0]

	hook := &WCHook{
		HookName:  hookName,
		Area:      classifyHookArea(hookName),
		HookType:  hookType,
		FilePath:  filePath,
		StartLine: call.Position.StartLine,
		EndLine:   call.Position.EndLine,
	}

	// For add/remove hooks: callback, priority
	if hookType == "action" || hookType == "filter" ||
		hookType == "action_removal" || hookType == "filter_removal" {
		if len(args) > 1 {
			hook.Callback = args[1]
		}
		if len(args) > 2 {
			if p, err := strconv.Atoi(args[2]); err == nil {
				hook.Priority = p
			}
		}
	}

	return hook
}

// extractFuncName extracts function name from AST node
func extractFuncName(node ast.Vertex) string {
	if node == nil {
		return ""
	}
	switch n := node.(type) {
	case *ast.Name:
		var parts []string
		for _, part := range n.Parts {
			if namePart, ok := part.(*ast.NamePart); ok {
				parts = append(parts, string(namePart.Value))
			}
		}
		return strings.Join(parts, "\\")
	case *ast.NameFullyQualified:
		var parts []string
		for _, part := range n.Parts {
			if namePart, ok := part.(*ast.NamePart); ok {
				parts = append(parts, string(namePart.Value))
			}
		}
		return strings.Join(parts, "\\")
	}
	return ""
}

// extractClassName extracts class name from AST node
func extractClassName(node ast.Vertex) string {
	return extractFuncName(node)
}

// extractMethodName extracts method name from AST node
func extractMethodName(node ast.Vertex) string {
	if node == nil {
		return ""
	}
	if ident, ok := node.(*ast.Identifier); ok {
		return string(ident.Value)
	}
	return ""
}

// extractCallArgs extracts string arguments from a function call
func extractCallArgs(args []ast.Vertex) []string {
	var result []string
	for _, arg := range args {
		if argNode, ok := arg.(*ast.Argument); ok {
			val := extractExprValue(argNode.Expr)
			result = append(result, val)
		}
	}
	return result
}

// extractExprValue extracts a string representation from an expression
func extractExprValue(expr ast.Vertex) string {
	if expr == nil {
		return ""
	}
	switch n := expr.(type) {
	case *ast.ScalarString:
		val := string(n.Value)
		if len(val) >= 2 {
			val = val[1 : len(val)-1] // Remove quotes
		}
		return val
	case *ast.ScalarLnumber:
		return string(n.Value)
	case *ast.ScalarDnumber:
		return string(n.Value)
	case *ast.Name:
		var parts []string
		for _, part := range n.Parts {
			if namePart, ok := part.(*ast.NamePart); ok {
				parts = append(parts, string(namePart.Value))
			}
		}
		return strings.Join(parts, "\\")
	case *ast.ExprConstFetch:
		return extractExprValue(n.Const)
	case *ast.ExprClassConstFetch:
		if constName, ok := n.Const.(*ast.Identifier); ok {
			if string(constName.Value) == "class" {
				return extractExprValue(n.Class) + "::class"
			}
		}
	case *ast.ExprVariable:
		if nameNode, ok := n.Name.(*ast.Identifier); ok {
			name := string(nameNode.Value)
			if strings.HasPrefix(name, "$") {
				return name
			}
			return "$" + name
		}
	case *ast.ExprArray:
		if len(n.Items) == 2 {
			items := make([]string, 0, 2)
			for _, item := range n.Items {
				if arrayItem, ok := item.(*ast.ExprArrayItem); ok {
					items = append(items, extractExprValue(arrayItem.Val))
				}
			}
			if len(items) == 2 {
				return items[0] + "::" + items[1]
			}
		}
		return "[array]"
	case *ast.ExprClosure:
		return "[closure]"
	case *ast.ExprArrowFunction:
		return "[arrow_fn]"
	case *ast.Identifier:
		return string(n.Value)
	}
	return ""
}

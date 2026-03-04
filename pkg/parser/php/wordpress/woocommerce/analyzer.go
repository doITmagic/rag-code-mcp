package woocommerce

import (
	"strings"

	"github.com/VKCOM/php-parser/pkg/ast"
	"github.com/doITmagic/rag-code-mcp/pkg/parser/php/wordpress"
)

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
type Analyzer struct {
	astHelper *wordpress.ASTHelper
}

// NewAnalyzer creates a new WooCommerce analyzer
func NewAnalyzer() *Analyzer {
	return &Analyzer{
		astHelper: wordpress.NewASTHelper(),
	}
}

// Analyze performs WooCommerce-specific analysis on AST
func (a *Analyzer) Analyze(root ast.Vertex, filePath string) *WooCommerceInfo {
	info := &WooCommerceInfo{}
	a.walk(root, filePath, info)
	return info
}

// AnalyzeHooksFromWP classifies existing WordPress hooks as WooCommerce hooks
// This takes already-detected WP hooks and enriches them with WC area info
func (a *Analyzer) AnalyzeHooksFromWP(wpHooks []wordpress.WPHook) []WCHook {
	var wcHooks []WCHook

	for _, h := range wpHooks {
		if !strings.HasPrefix(h.Name, "woocommerce_") {
			continue
		}

		wcHook := WCHook{
			HookName:  h.Name,
			Area:      classifyHookArea(h.Name),
			HookType:  string(h.Type),
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
		hook := a.astHelper.ExtractHookFromFunctionCall(n, filePath)
		if hook != nil && strings.HasPrefix(hook.Name, "woocommerce_") {
			wcHook := WCHook{
				HookName:  hook.Name,
				Area:      classifyHookArea(hook.Name),
				HookType:  string(hook.Type),
				Callback:  hook.Callback,
				Priority:  hook.Priority,
				FilePath:  hook.FilePath,
				StartLine: hook.StartLine,
				EndLine:   hook.EndLine,
			}
			info.Hooks = append(info.Hooks, wcHook)
		}

		// Check for WC API calls
		funcName := a.extractFuncName(n.Function)
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
		className := a.extractClassName(n.Class)
		if className == "WC" {
			info.APICalls = append(info.APICalls, WCAPICall{
				Function:  "WC::" + a.extractMethodName(n.Call),
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

// extractFuncName extracts function name from AST node
func (a *Analyzer) extractFuncName(node ast.Vertex) string {
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
	}
	return ""
}

// extractClassName extracts class name from AST node
func (a *Analyzer) extractClassName(node ast.Vertex) string {
	return a.extractFuncName(node)
}

// extractMethodName extracts method name from AST node
func (a *Analyzer) extractMethodName(node ast.Vertex) string {
	if node == nil {
		return ""
	}
	if ident, ok := node.(*ast.Identifier); ok {
		return string(ident.Value)
	}
	return ""
}

package wordpress

import (
	"strconv"
	"strings"

	"github.com/VKCOM/php-parser/pkg/ast"
	"github.com/doITmagic/rag-code-mcp/pkg/parser/php"
)

// hookFuncMap maps WordPress hook function names to their HookType
var hookFuncMap = map[string]HookType{
	"add_action":    HookAction,
	"add_filter":    HookFilter,
	"do_action":     HookActionTrigger,
	"apply_filters": HookFilterTrigger,
	"remove_action": HookActionRemoval,
	"remove_filter": HookFilterRemoval,
	"has_filter":    HookFilterCheck,
	"has_action":    HookActionCheck,
}

// HookAnalyzer extracts WordPress hooks from PHP package info
type HookAnalyzer struct {
	astHelper *ASTHelper
}

// NewHookAnalyzer creates a new hook analyzer
func NewHookAnalyzer() *HookAnalyzer {
	return &HookAnalyzer{
		astHelper: NewASTHelper(),
	}
}

// AnalyzeHooks detects WordPress hooks in all packages
func (a *HookAnalyzer) AnalyzeHooks(packages []*php.PackageInfo) []WPHook {
	var hooks []WPHook

	for _, pkg := range packages {
		// Scan global functions for hook calls
		for _, fn := range pkg.Functions {
			hooks = append(hooks, a.extractHooksFromCalls(fn.Calls, fn.FilePath, fn.StartLine)...)
		}

		// Scan class methods for hook calls
		for _, class := range pkg.Classes {
			for _, method := range class.Methods {
				hooks = append(hooks, a.extractHooksFromCalls(method.Calls, method.FilePath, method.StartLine)...)
			}
		}

		// Scan AST nodes for top-level function calls (outside functions/methods)
		for _, class := range pkg.Classes {
			if classNode, ok := pkg.ClassNodes[class.FullName]; ok {
				hooks = append(hooks, a.walkClassStmtsForHooks(classNode, class.FilePath)...)
			}
		}
	}

	return hooks
}

// AnalyzeHooksFromAST detects WordPress hooks directly from parsed AST root
func (a *HookAnalyzer) AnalyzeHooksFromAST(root ast.Vertex, filePath string) []WPHook {
	collector := &hookCollector{
		astHelper: a.astHelper,
		filePath:  filePath,
	}
	a.walkVertex(root, collector)
	return collector.hooks
}

// hookCollector collects hooks during AST traversal
type hookCollector struct {
	astHelper *ASTHelper
	filePath  string
	hooks     []WPHook
}

// walkVertex recursively walks AST to find hook function calls
func (a *HookAnalyzer) walkVertex(node ast.Vertex, collector *hookCollector) {
	if node == nil {
		return
	}

	switch n := node.(type) {
	case *ast.Root:
		for _, stmt := range n.Stmts {
			a.walkVertex(stmt, collector)
		}
	case *ast.StmtStmtList:
		for _, stmt := range n.Stmts {
			a.walkVertex(stmt, collector)
		}
	case *ast.StmtExpression:
		a.walkVertex(n.Expr, collector)
	case *ast.StmtReturn:
		if n.Expr != nil {
			a.walkVertex(n.Expr, collector)
		}
	case *ast.StmtNamespace:
		for _, stmt := range n.Stmts {
			a.walkVertex(stmt, collector)
		}
	case *ast.StmtClass:
		for _, stmt := range n.Stmts {
			a.walkVertex(stmt, collector)
		}
	case *ast.StmtClassMethod:
		if n.Stmt != nil {
			a.walkVertex(n.Stmt, collector)
		}
	case *ast.StmtFunction:
		if n.Stmts != nil {
			for _, s := range n.Stmts {
				a.walkVertex(s, collector)
			}
		}
	case *ast.StmtIf:
		if n.Stmt != nil {
			a.walkVertex(n.Stmt, collector)
		}
		for _, elseIf := range n.ElseIf {
			a.walkVertex(elseIf, collector)
		}
		if n.Else != nil {
			a.walkVertex(n.Else, collector)
		}
	case *ast.StmtElseIf:
		if n.Stmt != nil {
			a.walkVertex(n.Stmt, collector)
		}
	case *ast.StmtElse:
		if n.Stmt != nil {
			a.walkVertex(n.Stmt, collector)
		}
	case *ast.ExprAssign:
		// Handle $x = apply_filters(...) pattern
		a.walkVertex(n.Expr, collector)
	case *ast.ExprFunctionCall:
		hook := collector.astHelper.ExtractHookFromFunctionCall(n, collector.filePath)
		if hook != nil {
			collector.hooks = append(collector.hooks, *hook)
		}
	}
}

// extractHooksFromCalls extracts hooks from method/function call info (php.MethodCall)
func (a *HookAnalyzer) extractHooksFromCalls(calls []php.MethodCall, filePath string, startLine int) []WPHook {
	var hooks []WPHook

	for _, call := range calls {
		// Only global function calls (no object)
		if call.Object != "" {
			continue
		}

		hookType, ok := hookFuncMap[call.Method]
		if !ok {
			continue
		}

		hook := WPHook{
			Type:     hookType,
			FilePath: filePath,
		}

		// First arg is always the hook name
		if len(call.Args) > 0 {
			hook.Name = call.Args[0]
		}

		// For add_action/add_filter: second arg is callback
		if hookType == HookAction || hookType == HookFilter ||
			hookType == HookActionRemoval || hookType == HookFilterRemoval {
			if len(call.Args) > 1 {
				hook.Callback = call.Args[1]
			}
			// Third arg is priority
			if len(call.Args) > 2 {
				if p, err := strconv.Atoi(call.Args[2]); err == nil {
					hook.Priority = p
				}
			}
			// Fourth arg is accepted_args
			if len(call.Args) > 3 {
				if a, err := strconv.Atoi(call.Args[3]); err == nil {
					hook.AcceptedArgs = a
				}
			}
		}

		hooks = append(hooks, hook)
	}

	return hooks
}

// walkClassStmtsForHooks walks class AST statements looking for hook function calls in constructor etc.
func (a *HookAnalyzer) walkClassStmtsForHooks(classNode *ast.StmtClass, filePath string) []WPHook {
	var hooks []WPHook

	for _, stmt := range classNode.Stmts {
		if methodNode, ok := stmt.(*ast.StmtClassMethod); ok {
			// Look at method name — constructor and init methods are common places for hooks
			if methodNode.Stmt != nil {
				collector := &hookCollector{
					astHelper: a.astHelper,
					filePath:  filePath,
				}
				a.walkVertex(methodNode.Stmt, collector)
				hooks = append(hooks, collector.hooks...)
			}
		}
	}

	return hooks
}

// ASTHelper provides helper methods for extracting data from PHP AST nodes
type ASTHelper struct{}

// NewASTHelper creates a new AST helper
func NewASTHelper() *ASTHelper {
	return &ASTHelper{}
}

// ExtractHookFromFunctionCall checks if a function call is a WordPress hook and extracts info
func (h *ASTHelper) ExtractHookFromFunctionCall(call *ast.ExprFunctionCall, filePath string) *WPHook {
	funcName := h.extractFunctionName(call.Function)
	if funcName == "" {
		return nil
	}

	hookType, ok := hookFuncMap[funcName]
	if !ok {
		return nil
	}

	hook := &WPHook{
		Type:      hookType,
		FilePath:  filePath,
		StartLine: call.Position.StartLine,
		EndLine:   call.Position.EndLine,
	}

	// Extract arguments
	args := h.extractCallArgs(call.Args)

	// First arg is always the hook name
	if len(args) > 0 {
		hook.Name = args[0]
	}

	// For add/remove hooks: callback, priority, accepted_args
	if hookType == HookAction || hookType == HookFilter ||
		hookType == HookActionRemoval || hookType == HookFilterRemoval {
		if len(args) > 1 {
			hook.Callback = args[1]
		}
		if len(args) > 2 {
			if p, err := strconv.Atoi(args[2]); err == nil {
				hook.Priority = p
			}
		}
		if len(args) > 3 {
			if a, err := strconv.Atoi(args[3]); err == nil {
				hook.AcceptedArgs = a
			}
		}
	}

	return hook
}

// extractFunctionName gets the function name from a call node
func (h *ASTHelper) extractFunctionName(node ast.Vertex) string {
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

// extractCallArgs extracts string arguments from a function call
func (h *ASTHelper) extractCallArgs(args []ast.Vertex) []string {
	var result []string

	for _, arg := range args {
		if argNode, ok := arg.(*ast.Argument); ok {
			val := h.extractExprValue(argNode.Expr)
			result = append(result, val)
		}
	}

	return result
}

// extractExprValue extracts a string representation from an expression
func (h *ASTHelper) extractExprValue(expr ast.Vertex) string {
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
		return h.extractExprValue(n.Const)
	case *ast.ExprClassConstFetch:
		if constName, ok := n.Const.(*ast.Identifier); ok {
			if string(constName.Value) == "class" {
				return h.extractExprValue(n.Class) + "::class"
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
		// For array callbacks like [$this, 'method']
		if len(n.Items) == 2 {
			items := make([]string, 0, 2)
			for _, item := range n.Items {
				if arrayItem, ok := item.(*ast.ExprArrayItem); ok {
					items = append(items, h.extractExprValue(arrayItem.Val))
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

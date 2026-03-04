package wordpress

import (
	"github.com/VKCOM/php-parser/pkg/ast"
)

// ShortcodeAnalyzer extracts WordPress shortcode registrations
type ShortcodeAnalyzer struct {
	astHelper *ASTHelper
}

// NewShortcodeAnalyzer creates a new shortcode analyzer
func NewShortcodeAnalyzer() *ShortcodeAnalyzer {
	return &ShortcodeAnalyzer{
		astHelper: NewASTHelper(),
	}
}

// AnalyzeShortcodes detects add_shortcode calls from AST
func (a *ShortcodeAnalyzer) AnalyzeShortcodes(root ast.Vertex, filePath string) []Shortcode {
	var shortcodes []Shortcode
	a.walkForShortcodes(root, filePath, &shortcodes)
	return shortcodes
}

func (a *ShortcodeAnalyzer) walkForShortcodes(node ast.Vertex, filePath string, shortcodes *[]Shortcode) {
	if node == nil {
		return
	}

	switch n := node.(type) {
	case *ast.Root:
		for _, stmt := range n.Stmts {
			a.walkForShortcodes(stmt, filePath, shortcodes)
		}
	case *ast.StmtStmtList:
		for _, stmt := range n.Stmts {
			a.walkForShortcodes(stmt, filePath, shortcodes)
		}
	case *ast.StmtExpression:
		a.walkForShortcodes(n.Expr, filePath, shortcodes)
	case *ast.StmtNamespace:
		for _, stmt := range n.Stmts {
			a.walkForShortcodes(stmt, filePath, shortcodes)
		}
	case *ast.StmtFunction:
		if n.Stmts != nil {
			for _, s := range n.Stmts {
				a.walkForShortcodes(s, filePath, shortcodes)
			}
		}
	case *ast.StmtClass:
		for _, stmt := range n.Stmts {
			a.walkForShortcodes(stmt, filePath, shortcodes)
		}
	case *ast.StmtClassMethod:
		if n.Stmt != nil {
			a.walkForShortcodes(n.Stmt, filePath, shortcodes)
		}
	case *ast.ExprFunctionCall:
		funcName := a.astHelper.extractFunctionName(n.Function)
		if funcName == "add_shortcode" {
			args := a.astHelper.extractCallArgs(n.Args)
			if len(args) > 0 {
				sc := Shortcode{
					Tag:       args[0],
					FilePath:  filePath,
					StartLine: n.Position.StartLine,
					EndLine:   n.Position.EndLine,
				}
				if len(args) > 1 {
					sc.Callback = args[1]
				}
				*shortcodes = append(*shortcodes, sc)
			}
		}
	}
}

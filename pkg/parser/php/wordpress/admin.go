package wordpress

import (
	"github.com/VKCOM/php-parser/pkg/ast"
)

// AdminAnalyzer extracts WordPress admin page and settings registrations
type AdminAnalyzer struct {
	astHelper *ASTHelper
}

// NewAdminAnalyzer creates a new admin page analyzer
func NewAdminAnalyzer() *AdminAnalyzer {
	return &AdminAnalyzer{
		astHelper: NewASTHelper(),
	}
}

// AnalyzeAdminPages detects add_menu_page and add_submenu_page calls from AST
func (a *AdminAnalyzer) AnalyzeAdminPages(root ast.Vertex, filePath string) []AdminPage {
	var pages []AdminPage
	a.walkForAdminPages(root, filePath, &pages)
	return pages
}

// AnalyzeSettings detects register_setting calls from AST
func (a *AdminAnalyzer) AnalyzeSettings(root ast.Vertex, filePath string) []Setting {
	var settings []Setting
	a.walkForSettings(root, filePath, &settings)
	return settings
}

func (a *AdminAnalyzer) walkForAdminPages(node ast.Vertex, filePath string, pages *[]AdminPage) {
	if node == nil {
		return
	}

	switch n := node.(type) {
	case *ast.Root:
		for _, stmt := range n.Stmts {
			a.walkForAdminPages(stmt, filePath, pages)
		}
	case *ast.StmtStmtList:
		for _, stmt := range n.Stmts {
			a.walkForAdminPages(stmt, filePath, pages)
		}
	case *ast.StmtExpression:
		a.walkForAdminPages(n.Expr, filePath, pages)
	case *ast.StmtNamespace:
		for _, stmt := range n.Stmts {
			a.walkForAdminPages(stmt, filePath, pages)
		}
	case *ast.StmtFunction:
		if n.Stmts != nil {
			for _, s := range n.Stmts {
				a.walkForAdminPages(s, filePath, pages)
			}
		}
	case *ast.StmtClass:
		for _, stmt := range n.Stmts {
			a.walkForAdminPages(stmt, filePath, pages)
		}
	case *ast.StmtClassMethod:
		if n.Stmt != nil {
			a.walkForAdminPages(n.Stmt, filePath, pages)
		}
	case *ast.ExprFunctionCall:
		funcName := a.astHelper.extractFunctionName(n.Function)
		args := a.astHelper.extractCallArgs(n.Args)

		switch funcName {
		case "add_menu_page":
			// add_menu_page($page_title, $menu_title, $capability, $menu_slug, $callback, $icon, $position)
			page := AdminPage{
				FilePath:  filePath,
				StartLine: n.Position.StartLine,
				EndLine:   n.Position.EndLine,
			}
			if len(args) > 0 {
				page.PageTitle = args[0]
			}
			if len(args) > 1 {
				page.MenuTitle = args[1]
			}
			if len(args) > 2 {
				page.Capability = args[2]
			}
			if len(args) > 3 {
				page.MenuSlug = args[3]
			}
			if len(args) > 4 {
				page.Callback = args[4]
			}
			*pages = append(*pages, page)

		case "add_submenu_page":
			// add_submenu_page($parent_slug, $page_title, $menu_title, $capability, $menu_slug, $callback)
			page := AdminPage{
				IsSubmenu: true,
				FilePath:  filePath,
				StartLine: n.Position.StartLine,
				EndLine:   n.Position.EndLine,
			}
			if len(args) > 0 {
				page.Parent = args[0]
			}
			if len(args) > 1 {
				page.PageTitle = args[1]
			}
			if len(args) > 2 {
				page.MenuTitle = args[2]
			}
			if len(args) > 3 {
				page.Capability = args[3]
			}
			if len(args) > 4 {
				page.MenuSlug = args[4]
			}
			if len(args) > 5 {
				page.Callback = args[5]
			}
			*pages = append(*pages, page)
		}
	}
}

func (a *AdminAnalyzer) walkForSettings(node ast.Vertex, filePath string, settings *[]Setting) {
	if node == nil {
		return
	}

	switch n := node.(type) {
	case *ast.Root:
		for _, stmt := range n.Stmts {
			a.walkForSettings(stmt, filePath, settings)
		}
	case *ast.StmtStmtList:
		for _, stmt := range n.Stmts {
			a.walkForSettings(stmt, filePath, settings)
		}
	case *ast.StmtExpression:
		a.walkForSettings(n.Expr, filePath, settings)
	case *ast.StmtNamespace:
		for _, stmt := range n.Stmts {
			a.walkForSettings(stmt, filePath, settings)
		}
	case *ast.StmtFunction:
		if n.Stmts != nil {
			for _, s := range n.Stmts {
				a.walkForSettings(s, filePath, settings)
			}
		}
	case *ast.StmtClass:
		for _, stmt := range n.Stmts {
			a.walkForSettings(stmt, filePath, settings)
		}
	case *ast.StmtClassMethod:
		if n.Stmt != nil {
			a.walkForSettings(n.Stmt, filePath, settings)
		}
	case *ast.ExprFunctionCall:
		funcName := a.astHelper.extractFunctionName(n.Function)
		if funcName == "register_setting" {
			args := a.astHelper.extractCallArgs(n.Args)
			setting := Setting{
				FilePath: filePath,
				Line:     n.Position.StartLine,
			}
			if len(args) > 0 {
				setting.Group = args[0]
			}
			if len(args) > 1 {
				setting.Option = args[1]
			}
			*settings = append(*settings, setting)
		}
	}
}

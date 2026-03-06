package wordpress

import (
	"github.com/VKCOM/php-parser/pkg/ast"
	"github.com/doITmagic/rag-code-mcp/pkg/parser/php"
)

// PostTypeAnalyzer extracts WordPress custom post types and taxonomies
type PostTypeAnalyzer struct {
	astHelper *ASTHelper
}

// NewPostTypeAnalyzer creates a new post type analyzer
func NewPostTypeAnalyzer() *PostTypeAnalyzer {
	return &PostTypeAnalyzer{
		astHelper: NewASTHelper(),
	}
}

// AnalyzePostTypes detects register_post_type calls from AST
func (a *PostTypeAnalyzer) AnalyzePostTypes(root ast.Vertex, filePath string) []PostType {
	collector := &postTypeCollector{
		astHelper: a.astHelper,
		filePath:  filePath,
	}
	a.walkForPostTypes(root, collector)
	return collector.postTypes
}

// AnalyzePostTypesFromPackages detects post types from parsed packages
func (a *PostTypeAnalyzer) AnalyzePostTypesFromPackages(packages []*php.PackageInfo) []PostType {
	var result []PostType

	for _, pkg := range packages {
		for _, fn := range pkg.Functions {
			for _, call := range fn.Calls {
				if call.Object == "" && call.Method == "register_post_type" && len(call.Args) > 0 {
					result = append(result, PostType{
						Name:      call.Args[0],
						FilePath:  fn.FilePath,
						StartLine: fn.StartLine,
					})
				}
			}
		}
		for _, class := range pkg.Classes {
			for _, method := range class.Methods {
				for _, call := range method.Calls {
					if call.Object == "" && call.Method == "register_post_type" && len(call.Args) > 0 {
						result = append(result, PostType{
							Name:      call.Args[0],
							FilePath:  method.FilePath,
							StartLine: method.StartLine,
						})
					}
				}
			}
		}
	}

	return result
}

// AnalyzeTaxonomies detects register_taxonomy calls from AST
func (a *PostTypeAnalyzer) AnalyzeTaxonomies(root ast.Vertex, filePath string) []Taxonomy {
	collector := &taxonomyCollector{
		astHelper: a.astHelper,
		filePath:  filePath,
	}
	a.walkForTaxonomies(root, collector)
	return collector.taxonomies
}

// AnalyzeTaxonomiesFromPackages detects taxonomies from parsed packages
func (a *PostTypeAnalyzer) AnalyzeTaxonomiesFromPackages(packages []*php.PackageInfo) []Taxonomy {
	var result []Taxonomy

	for _, pkg := range packages {
		for _, fn := range pkg.Functions {
			for _, call := range fn.Calls {
				if call.Object == "" && call.Method == "register_taxonomy" && len(call.Args) > 0 {
					tax := Taxonomy{
						Name:     call.Args[0],
						FilePath: fn.FilePath,
					}
					if len(call.Args) > 1 {
						tax.PostTypes = []string{call.Args[1]}
					}
					result = append(result, tax)
				}
			}
		}
		for _, class := range pkg.Classes {
			for _, method := range class.Methods {
				for _, call := range method.Calls {
					if call.Object == "" && call.Method == "register_taxonomy" && len(call.Args) > 0 {
						tax := Taxonomy{
							Name:     call.Args[0],
							FilePath: method.FilePath,
						}
						if len(call.Args) > 1 {
							tax.PostTypes = []string{call.Args[1]}
						}
						result = append(result, tax)
					}
				}
			}
		}
	}

	return result
}

type postTypeCollector struct {
	astHelper *ASTHelper
	filePath  string
	postTypes []PostType
}

type taxonomyCollector struct {
	astHelper  *ASTHelper
	filePath   string
	taxonomies []Taxonomy
}

func (a *PostTypeAnalyzer) walkForPostTypes(node ast.Vertex, collector *postTypeCollector) {
	if node == nil {
		return
	}

	switch n := node.(type) {
	case *ast.Root:
		for _, stmt := range n.Stmts {
			a.walkForPostTypes(stmt, collector)
		}
	case *ast.StmtStmtList:
		for _, stmt := range n.Stmts {
			a.walkForPostTypes(stmt, collector)
		}
	case *ast.StmtExpression:
		a.walkForPostTypes(n.Expr, collector)
	case *ast.StmtNamespace:
		for _, stmt := range n.Stmts {
			a.walkForPostTypes(stmt, collector)
		}
	case *ast.StmtFunction:
		if n.Stmts != nil {
			for _, s := range n.Stmts {
				a.walkForPostTypes(s, collector)
			}
		}
	case *ast.StmtClass:
		for _, stmt := range n.Stmts {
			a.walkForPostTypes(stmt, collector)
		}
	case *ast.StmtClassMethod:
		if n.Stmt != nil {
			a.walkForPostTypes(n.Stmt, collector)
		}
	case *ast.StmtIf:
		if n.Stmt != nil {
			a.walkForPostTypes(n.Stmt, collector)
		}
	case *ast.StmtReturn:
		if n.Expr != nil {
			a.walkForPostTypes(n.Expr, collector)
		}
	case *ast.ExprFunctionCall:
		funcName := collector.astHelper.extractFunctionName(n.Function)
		if funcName == "register_post_type" {
			args := collector.astHelper.extractCallArgs(n.Args)
			if len(args) > 0 {
				pt := PostType{
					Name:      args[0],
					FilePath:  collector.filePath,
					StartLine: n.Position.StartLine,
					EndLine:   n.Position.EndLine,
				}
				collector.postTypes = append(collector.postTypes, pt)
			}
		}
	}
}

func (a *PostTypeAnalyzer) walkForTaxonomies(node ast.Vertex, collector *taxonomyCollector) {
	if node == nil {
		return
	}

	switch n := node.(type) {
	case *ast.Root:
		for _, stmt := range n.Stmts {
			a.walkForTaxonomies(stmt, collector)
		}
	case *ast.StmtStmtList:
		for _, stmt := range n.Stmts {
			a.walkForTaxonomies(stmt, collector)
		}
	case *ast.StmtExpression:
		a.walkForTaxonomies(n.Expr, collector)
	case *ast.StmtNamespace:
		for _, stmt := range n.Stmts {
			a.walkForTaxonomies(stmt, collector)
		}
	case *ast.StmtFunction:
		if n.Stmts != nil {
			for _, s := range n.Stmts {
				a.walkForTaxonomies(s, collector)
			}
		}
	case *ast.StmtClass:
		for _, stmt := range n.Stmts {
			a.walkForTaxonomies(stmt, collector)
		}
	case *ast.StmtClassMethod:
		if n.Stmt != nil {
			a.walkForTaxonomies(n.Stmt, collector)
		}
	case *ast.StmtIf:
		if n.Stmt != nil {
			a.walkForTaxonomies(n.Stmt, collector)
		}
	case *ast.StmtReturn:
		if n.Expr != nil {
			a.walkForTaxonomies(n.Expr, collector)
		}
	case *ast.ExprFunctionCall:
		funcName := collector.astHelper.extractFunctionName(n.Function)
		if funcName == "register_taxonomy" {
			args := collector.astHelper.extractCallArgs(n.Args)
			if len(args) > 0 {
				tax := Taxonomy{
					Name:      args[0],
					FilePath:  collector.filePath,
					StartLine: n.Position.StartLine,
					EndLine:   n.Position.EndLine,
				}
				if len(args) > 1 {
					tax.PostTypes = []string{args[1]}
				}
				collector.taxonomies = append(collector.taxonomies, tax)
			}
		}
	}
}

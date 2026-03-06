package oxygen

import (
	"strings"

	"github.com/VKCOM/php-parser/pkg/ast"
	"github.com/doITmagic/rag-code-mcp/pkg/parser/php"
)

// oxyElBaseClasses are the known Oxygen element base classes
var oxyElBaseClasses = map[string]bool{
	"OxyEl":         true,
	"OxyElShadow":   true,
	"OxygenElement": true,
	"OxyEl\\OxyEl":  true,
}

// oxygenPostTypes are Oxygen-specific custom post types
var oxygenPostTypes = map[string]bool{
	"ct_template":      true,
	"oxy_user_library": true,
}

// Analyzer detects Oxygen Builder-specific patterns in WordPress code
type Analyzer struct{}

// NewAnalyzer creates a new Oxygen Builder analyzer
func NewAnalyzer() *Analyzer {
	return &Analyzer{}
}

// Analyze performs Oxygen Builder-specific analysis
func (a *Analyzer) Analyze(root ast.Vertex, filePath string) *OxygenInfo {
	info := &OxygenInfo{}
	a.walkForOxygenTemplates(root, filePath, info)
	return info
}

// AnalyzeFromPackages detects Oxygen elements and templates from parsed packages
func (a *Analyzer) AnalyzeFromPackages(packages []*php.PackageInfo) *OxygenInfo {
	info := &OxygenInfo{}

	for _, pkg := range packages {
		// Detect OxyEl classes
		for _, class := range pkg.Classes {
			if a.isOxygenElement(class) {
				element := a.extractOxygenElement(class)
				info.Elements = append(info.Elements, element)
			}
		}

		// Detect Oxygen post type registrations from function calls
		for _, fn := range pkg.Functions {
			for _, call := range fn.Calls {
				if call.Object == "" && call.Method == "register_post_type" && len(call.Args) > 0 {
					if oxygenPostTypes[call.Args[0]] {
						info.Templates = append(info.Templates, OxygenTemplate{
							PostType: call.Args[0],
							FilePath: fn.FilePath,
						})
					}
				}
			}
		}
		for _, class := range pkg.Classes {
			for _, method := range class.Methods {
				for _, call := range method.Calls {
					if call.Object == "" && call.Method == "register_post_type" && len(call.Args) > 0 {
						if oxygenPostTypes[call.Args[0]] {
							info.Templates = append(info.Templates, OxygenTemplate{
								PostType: call.Args[0],
								FilePath: method.FilePath,
							})
						}
					}
				}
			}
		}
	}

	return info
}

// isOxygenElement checks if a class extends an Oxygen base element class
func (a *Analyzer) isOxygenElement(class php.ClassInfo) bool {
	if class.Extends == "" {
		return false
	}

	extends := class.Extends
	// Check direct match
	if oxyElBaseClasses[extends] {
		return true
	}
	// Check with namespace suffix
	for base := range oxyElBaseClasses {
		if strings.HasSuffix(extends, "\\"+base) {
			return true
		}
	}
	return false
}

// extractOxygenElement builds an OxygenElement from class info
func (a *Analyzer) extractOxygenElement(class php.ClassInfo) OxygenElement {
	element := OxygenElement{
		ClassName: class.Name,
		Namespace: class.Namespace,
		FullName:  class.FullName,
		FilePath:  class.FilePath,
		StartLine: class.StartLine,
		EndLine:   class.EndLine,
	}

	// Check which OxyEl methods are implemented
	for _, method := range class.Methods {
		for _, required := range OxyElRequiredMethods {
			if method.Name == required {
				element.Methods = append(element.Methods, method.Name)
				if method.Name == "slug" {
					element.SlugMethod = true
				}
			}
		}
	}

	return element
}

// walkForOxygenTemplates walks AST for Oxygen-specific post type registrations
func (a *Analyzer) walkForOxygenTemplates(node ast.Vertex, filePath string, info *OxygenInfo) {
	if node == nil {
		return
	}

	switch n := node.(type) {
	case *ast.Root:
		for _, stmt := range n.Stmts {
			a.walkForOxygenTemplates(stmt, filePath, info)
		}
	case *ast.StmtStmtList:
		for _, stmt := range n.Stmts {
			a.walkForOxygenTemplates(stmt, filePath, info)
		}
	case *ast.StmtExpression:
		a.walkForOxygenTemplates(n.Expr, filePath, info)
	case *ast.StmtNamespace:
		for _, stmt := range n.Stmts {
			a.walkForOxygenTemplates(stmt, filePath, info)
		}
	case *ast.StmtFunction:
		if n.Stmts != nil {
			for _, s := range n.Stmts {
				a.walkForOxygenTemplates(s, filePath, info)
			}
		}
	case *ast.StmtClass:
		for _, stmt := range n.Stmts {
			a.walkForOxygenTemplates(stmt, filePath, info)
		}
	case *ast.StmtClassMethod:
		if n.Stmt != nil {
			a.walkForOxygenTemplates(n.Stmt, filePath, info)
		}
	case *ast.ExprFunctionCall:
		funcName := extractFuncName(n.Function)
		if funcName == "register_post_type" {
			args := extractCallArgs(n.Args)
			if len(args) > 0 && oxygenPostTypes[args[0]] {
				info.Templates = append(info.Templates, OxygenTemplate{
					PostType: args[0],
					FilePath: filePath,
					Line:     n.Position.StartLine,
				})
			}
		}
	}
}

// extractFuncName extracts function name from AST node
func extractFuncName(node ast.Vertex) string {
	if node == nil {
		return ""
	}
	if name, ok := node.(*ast.Name); ok {
		var parts []string
		for _, part := range name.Parts {
			if namePart, ok := part.(*ast.NamePart); ok {
				parts = append(parts, string(namePart.Value))
			}
		}
		return strings.Join(parts, "\\")
	}
	return ""
}

// extractCallArgs extracts string arguments from function call
func extractCallArgs(args []ast.Vertex) []string {
	var result []string
	for _, arg := range args {
		if argNode, ok := arg.(*ast.Argument); ok {
			val := extractStringExpr(argNode.Expr)
			result = append(result, val)
		}
	}
	return result
}

// extractStringExpr extracts a string value from an expression
func extractStringExpr(expr ast.Vertex) string {
	if expr == nil {
		return ""
	}
	if str, ok := expr.(*ast.ScalarString); ok {
		val := string(str.Value)
		if len(val) >= 2 {
			return val[1 : len(val)-1]
		}
		return val
	}
	return ""
}

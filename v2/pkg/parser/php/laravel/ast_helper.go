package laravel

import (
	"strings"

	"github.com/VKCOM/php-parser/pkg/ast"
)

// ASTHelper helps extract property values from PHP AST nodes
type ASTHelper struct{}

// NewASTHelper creates a new AST helper
func NewASTHelper() *ASTHelper {
	return &ASTHelper{}
}

// ExtractStringProperty extracts a simple string property value
func (h *ASTHelper) ExtractStringProperty(n *ast.StmtClass, propertyName string) string {
	if n == nil {
		return ""
	}

	for _, stmt := range n.Stmts {
		if propList, ok := stmt.(*ast.StmtPropertyList); ok {
			for _, prop := range propList.Props {
				if propNode, ok := prop.(*ast.StmtProperty); ok {
					if varNode, ok := propNode.Var.(*ast.ExprVariable); ok {
						if nameNode, ok := varNode.Name.(*ast.Identifier); ok {
							propName := strings.TrimPrefix(string(nameNode.Value), "$")
							if propName == propertyName {
								if propNode.Expr != nil {
									return h.ExtractStringFromExpr(propNode.Expr)
								}
							}
						}
					}
				}
			}
		}
	}

	return ""
}

// ExtractStringArray extracts a string array property from a class node
func (h *ASTHelper) ExtractStringArray(n *ast.StmtClass, propertyName string) []string {
	if n == nil {
		return nil
	}

	for _, stmt := range n.Stmts {
		if propList, ok := stmt.(*ast.StmtPropertyList); ok {
			for _, prop := range propList.Props {
				if propNode, ok := prop.(*ast.StmtProperty); ok {
					if varNode, ok := propNode.Var.(*ast.ExprVariable); ok {
						if nameNode, ok := varNode.Name.(*ast.Identifier); ok {
							propName := strings.TrimPrefix(string(nameNode.Value), "$")
							if propName == propertyName {
								if propNode.Expr != nil {
									return h.ExtractStringArrayFromExpr(propNode.Expr)
								}
							}
						}
					}
				}
			}
		}
	}

	return nil
}

// ExtractStringFromExpr extracts a string value from an expression
func (h *ASTHelper) ExtractStringFromExpr(n ast.Vertex) string {
	if n == nil {
		return ""
	}
	switch expr := n.(type) {
	case *ast.ScalarString:
		return strings.Trim(string(expr.Value), "'\"")
	case *ast.Identifier:
		return string(expr.Value)
	}
	return ""
}

// ExtractIdentifier extracts a string from an Identifier node
func (h *ASTHelper) ExtractIdentifier(n ast.Vertex) string {
	if ident, ok := n.(*ast.Identifier); ok {
		return string(ident.Value)
	}
	return ""
}

// ExtractNodeName extracts a string representation of a Name node (fqn)
func (h *ASTHelper) ExtractNodeName(n ast.Vertex) string {
	if n == nil {
		return ""
	}
	switch node := n.(type) {
	case *ast.Name:
		var parts []string
		for _, p := range node.Parts {
			if np, ok := p.(*ast.NamePart); ok {
				parts = append(parts, string(np.Value))
			}
		}
		return strings.Join(parts, "\\")
	case *ast.NameFullyQualified:
		var parts []string
		for _, p := range node.Parts {
			if np, ok := p.(*ast.NamePart); ok {
				parts = append(parts, string(np.Value))
			}
		}
		return "\\" + strings.Join(parts, "\\")
	case *ast.Identifier:
		return string(node.Value)
	}
	return ""
}

// ExtractStringArrayFromExpr extracts strings from an array expression
func (h *ASTHelper) ExtractStringArrayFromExpr(n ast.Vertex) []string {
	if n == nil {
		return nil
	}

	if a, ok := n.(*ast.ExprArray); ok {
		var result []string
		for _, item := range a.Items {
			if entry, ok := item.(*ast.ExprArrayItem); ok {
				if val := h.ExtractStringFromExpr(entry.Val); val != "" {
					result = append(result, val)
				}
			}
		}
		return result
	}

	return nil
}

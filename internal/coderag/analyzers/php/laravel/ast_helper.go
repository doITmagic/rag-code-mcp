package laravel

import (
	"strings"

	"github.com/VKCOM/php-parser/pkg/ast"
)

// ASTPropertyExtractor helps extract property values from PHP AST nodes
type ASTPropertyExtractor struct{}

// NewASTPropertyExtractor creates a new AST property extractor
func NewASTPropertyExtractor() *ASTPropertyExtractor {
	return &ASTPropertyExtractor{}
}

// ExtractStringArrayFromClass extracts a protected/private string array property from a class node
// Example: protected $fillable = ['name', 'email'];
func (e *ASTPropertyExtractor) ExtractStringArrayFromClass(classNode *ast.StmtClass, propertyName string) []string {
	if classNode == nil {
		return nil
	}

	for _, stmt := range classNode.Stmts {
		if propList, ok := stmt.(*ast.StmtPropertyList); ok {
			// Check each property in the list
			for _, prop := range propList.Props {
				if propNode, ok := prop.(*ast.StmtProperty); ok {
					// Get property name
					if varNode, ok := propNode.Var.(*ast.ExprVariable); ok {
						if nameNode, ok := varNode.Name.(*ast.Identifier); ok {
							propName := string(nameNode.Value)
							// Remove $ prefix if present
							propName = strings.TrimPrefix(propName, "$")
							if propName == propertyName {
								// Found the property, extract array values
								if propNode.Expr != nil {
									return e.extractStringArrayFromExpr(propNode.Expr)
								}
								return nil
							}
						}
					}
				}
			}
		}
	}

	return nil
}

// ExtractMapFromClass extracts an associative array (map) from a class property
// Example: protected $casts = ['is_admin' => 'boolean', 'age' => 'integer'];
func (e *ASTPropertyExtractor) ExtractMapFromClass(classNode *ast.StmtClass, propertyName string) map[string]string {
	if classNode == nil {
		return nil
	}

	for _, stmt := range classNode.Stmts {
		if propList, ok := stmt.(*ast.StmtPropertyList); ok {
			for _, prop := range propList.Props {
				if propNode, ok := prop.(*ast.StmtProperty); ok {
					if varNode, ok := propNode.Var.(*ast.ExprVariable); ok {
						if nameNode, ok := varNode.Name.(*ast.Identifier); ok {
							propName := string(nameNode.Value)
							propName = strings.TrimPrefix(propName, "$")
							if propName == propertyName {
								if propNode.Expr != nil {
									return e.extractMapFromExpr(propNode.Expr)
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

// ExtractStringPropertyFromClass extracts a simple string property value
// Example: protected $table = 'users';
func (e *ASTPropertyExtractor) ExtractStringPropertyFromClass(classNode *ast.StmtClass, propertyName string) string {
	if classNode == nil {
		return ""
	}

	for _, stmt := range classNode.Stmts {
		if propList, ok := stmt.(*ast.StmtPropertyList); ok {
			for _, prop := range propList.Props {
				if propNode, ok := prop.(*ast.StmtProperty); ok {
					if varNode, ok := propNode.Var.(*ast.ExprVariable); ok {
						if nameNode, ok := varNode.Name.(*ast.Identifier); ok {
							propName := string(nameNode.Value)
							propName = strings.TrimPrefix(propName, "$")
							if propName == propertyName {
								if propNode.Expr != nil {
									return e.extractStringFromExpr(propNode.Expr)
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

// extractStringArrayFromExpr extracts string array elements from an expression node
func (e *ASTPropertyExtractor) extractStringArrayFromExpr(expr ast.Vertex) []string {
	var result []string

	switch node := expr.(type) {
	case *ast.ExprArray:
		// Handle both array(...) and [...] syntax
		for _, item := range node.Items {
			if arrayItem, ok := item.(*ast.ExprArrayItem); ok {
				if strVal := e.extractStringFromExpr(arrayItem.Val); strVal != "" {
					result = append(result, strVal)
				}
			}
		}
	}

	return result
}

// extractMapFromExpr extracts associative array (map) from an expression node
func (e *ASTPropertyExtractor) extractMapFromExpr(expr ast.Vertex) map[string]string {
	result := make(map[string]string)

	switch node := expr.(type) {
	case *ast.ExprArray:
		// Handle both array(...) and [...] associative arrays
		for _, item := range node.Items {
			if arrayItem, ok := item.(*ast.ExprArrayItem); ok {
				key := e.extractStringFromExpr(arrayItem.Key)
				val := e.extractStringFromExpr(arrayItem.Val)
				if key != "" && val != "" {
					result[key] = val
				}
			}
		}
	}

	return result
}

// extractStringFromExpr extracts a string value from an expression node
func (e *ASTPropertyExtractor) extractStringFromExpr(expr ast.Vertex) string {
	if expr == nil {
		return ""
	}

	switch node := expr.(type) {
	case *ast.ScalarString:
		// Remove quotes from string
		val := string(node.Value)
		if len(val) >= 2 {
			// Remove surrounding quotes
			val = val[1 : len(val)-1]
		}
		return val

	case *ast.ScalarEncapsed:
		// Handle string interpolation - concatenate parts
		var result strings.Builder
		for _, part := range node.Parts {
			if strPart, ok := part.(*ast.ScalarEncapsedStringPart); ok {
				result.WriteString(string(strPart.Value))
			}
		}
		return result.String()

	case *ast.Identifier:
		// Handle identifiers (like class names)
		return string(node.Value)

	case *ast.Name:
		// Handle qualified names
		var parts []string
		for _, part := range node.Parts {
			if id, ok := part.(*ast.NamePart); ok {
				parts = append(parts, string(id.Value))
			}
		}
		return strings.Join(parts, "\\")

	case *ast.ExprConstFetch:
		// Handle constants (true, false, null)
		if name, ok := node.Const.(*ast.Name); ok {
			return e.extractStringFromExpr(name)
		}

	case *ast.ExprClassConstFetch:
		// Handle Class::class syntax
		if constName, ok := node.Const.(*ast.Identifier); ok {
			if string(constName.Value) == "class" {
				// Extract class name
				return e.extractStringFromExpr(node.Class)
			}
		}
	}

	return ""
}

// ExtractMethodCalls extracts method calls from a method body
// Example: return $this->hasMany(Post::class);
func (e *ASTPropertyExtractor) ExtractMethodCalls(methodNode *ast.StmtClassMethod) []MethodCall {
	var calls []MethodCall

	if methodNode == nil || methodNode.Stmt == nil {
		return calls
	}

	// Walk the method body
	e.walkStmts(methodNode.Stmt, &calls)

	return calls
}

// MethodCall represents a method call found in code
type MethodCall struct {
	Object string   // Variable name ($this, $variable)
	Method string   // Method name (hasMany, belongsTo)
	Args   []string // Arguments
}

// walkStmts recursively walks statements to find method calls
func (e *ASTPropertyExtractor) walkStmts(stmt ast.Vertex, calls *[]MethodCall) {
	if stmt == nil {
		return
	}

	switch node := stmt.(type) {
	case *ast.StmtStmtList:
		for _, s := range node.Stmts {
			e.walkStmts(s, calls)
		}

	case *ast.StmtReturn:
		if node.Expr != nil {
			e.walkExpr(node.Expr, calls)
		}

	case *ast.StmtExpression:
		if node.Expr != nil {
			e.walkExpr(node.Expr, calls)
		}
	}
}

// walkExpr recursively walks expressions to find method calls
func (e *ASTPropertyExtractor) walkExpr(expr ast.Vertex, calls *[]MethodCall) {
	if expr == nil {
		return
	}

	switch node := expr.(type) {
	case *ast.ExprMethodCall:
		// Found a method call
		call := MethodCall{}

		// Extract object variable name
		if varNode, ok := node.Var.(*ast.ExprVariable); ok {
			if nameNode, ok := varNode.Name.(*ast.Identifier); ok {
				varName := string(nameNode.Value)
				// Remove $ prefix from variable names
				call.Object = strings.TrimPrefix(varName, "$")
			}
		}

		// Extract method name
		if methodNode, ok := node.Method.(*ast.Identifier); ok {
			call.Method = string(methodNode.Value)
		}

		// Extract arguments
		for _, arg := range node.Args {
			if argNode, ok := arg.(*ast.Argument); ok {
				argStr := e.extractStringFromExpr(argNode.Expr)
				if argStr != "" {
					call.Args = append(call.Args, argStr)
				}
			}
		}

		if call.Method != "" {
			*calls = append(*calls, call)
		}

		// Recursively walk the Var in case it's a chained method call
		e.walkExpr(node.Var, calls)
	}
}

package node

import (
	"strings"

	"github.com/odvcencio/gotreesitter"
	"github.com/odvcencio/gotreesitter/grammars"
)

// TreeSitterAnalyzer uses tree-sitter AST for Node.js/Express pattern detection
type TreeSitterAnalyzer struct{}

// NewTreeSitterAnalyzer creates a new tree-sitter based Node.js analyzer
func NewTreeSitterAnalyzer() *TreeSitterAnalyzer {
	return &TreeSitterAnalyzer{}
}

// Analyze parses source with tree-sitter and extracts Node.js/Express patterns
func (t *TreeSitterAnalyzer) Analyze(source []byte, filePath string) *NodeInfo {
	lang := grammars.DetectLanguage(filePath)
	if lang == nil {
		return nil
	}

	parser := gotreesitter.NewParser(lang.Language())
	tree, err := parser.Parse(source)
	if err != nil {
		return nil
	}

	root := tree.RootNode()
	langObj := lang.Language()

	info := &NodeInfo{}

	info.Routes = t.detectRoutes(root, source, langObj, filePath)
	info.Middleware = t.detectMiddleware(root, source, langObj, filePath)
	info.Requires = t.detectRequires(root, source, langObj)
	info.ModuleExports = t.detectModuleExports(root, source, langObj, filePath)

	return info
}

// detectRoutes finds Express route definitions: app.get('/path', handler)
func (t *TreeSitterAnalyzer) detectRoutes(root *gotreesitter.Node, source []byte, lang *gotreesitter.Language, filePath string) []Route {
	var routes []Route

	t.walkTree(root, lang, func(node *gotreesitter.Node) {
		if node.Type(lang) != "call_expression" {
			return
		}

		callee := node.Child(0)
		if callee == nil || callee.Type(lang) != "member_expression" {
			return
		}

		calleeText := callee.Text(source)
		parts := strings.Split(calleeText, ".")
		if len(parts) != 2 {
			return
		}

		obj := parts[0]
		method := parts[1]

		// Check if it's a route-defining call
		validMethods := map[string]bool{
			"get": true, "post": true, "put": true, "delete": true,
			"patch": true, "all": true, "use": true,
		}
		validObjects := map[string]bool{
			"app": true, "router": true, "server": true,
		}

		if !validMethods[method] || !validObjects[obj] {
			return
		}

		// Extract path and handler from arguments
		args := node.Child(1)
		if args == nil || args.Type(lang) != "arguments" {
			return
		}

		var path, handler string
		for i := 0; i < args.ChildCount(); i++ {
			arg := args.Child(i)
			argType := arg.Type(lang)
			if (argType == "string" || argType == "template_string") && path == "" {
				path = strings.Trim(arg.Text(source), "'\"` ")
			}
			if argType == "identifier" {
				handler = arg.Text(source)
			}
		}

		if path == "" && method == "use" {
			return // middleware without path handled separately
		}

		routes = append(routes, Route{
			Method:   method,
			Path:     path,
			Handler:  handler,
			IsRouter: obj == "router",
			FilePath: filePath,
			Line:     int(node.StartPoint().Row) + 1,
		})
	})

	return routes
}

// detectMiddleware finds Express middleware usage
func (t *TreeSitterAnalyzer) detectMiddleware(root *gotreesitter.Node, source []byte, lang *gotreesitter.Language, filePath string) []Middleware {
	var middleware []Middleware
	seen := make(map[string]bool)

	t.walkTree(root, lang, func(node *gotreesitter.Node) {
		if node.Type(lang) != "call_expression" {
			return
		}

		callee := node.Child(0)
		if callee == nil || callee.Type(lang) != "member_expression" {
			return
		}

		calleeText := callee.Text(source)
		if !strings.HasSuffix(calleeText, ".use") {
			return
		}

		// Extract middleware from arguments
		args := node.Child(1)
		if args == nil || args.Type(lang) != "arguments" {
			return
		}

		for i := 0; i < args.ChildCount(); i++ {
			arg := args.Child(i)
			argType := arg.Type(lang)

			var name string
			if argType == "identifier" {
				name = arg.Text(source)
			} else if argType == "call_expression" {
				// e.g., express.json(), cors()
				fc := arg.Child(0)
				if fc != nil {
					name = fc.Text(source)
				}
			}

			if name == "" || seen[name] {
				continue
			}
			seen[name] = true

			middleware = append(middleware, Middleware{
				Name:     name,
				IsCustom: !builtinMiddleware[name],
				FilePath: filePath,
				Line:     int(node.StartPoint().Row) + 1,
			})
		}
	})

	return middleware
}

// detectRequires finds require() calls in the AST
func (t *TreeSitterAnalyzer) detectRequires(root *gotreesitter.Node, source []byte, lang *gotreesitter.Language) []Require {
	var requires []Require

	for i := 0; i < root.ChildCount(); i++ {
		child := root.Child(i)
		nodeType := child.Type(lang)

		if nodeType != "lexical_declaration" && nodeType != "variable_declaration" {
			continue
		}

		for j := 0; j < child.ChildCount(); j++ {
			decl := child.Child(j)
			if decl.Type(lang) != "variable_declarator" {
				continue
			}

			var binding string
			var module string

			for k := 0; k < decl.ChildCount(); k++ {
				gc := decl.Child(k)
				gcType := gc.Type(lang)

				switch gcType {
				case "identifier":
					binding = gc.Text(source)
				case "object_pattern":
					// Destructured: const { x, y } = require('...')
					binding = gc.Text(source)
				case "call_expression":
					// Check if it's require()
					callee := gc.Child(0)
					if callee != nil && callee.Text(source) == "require" {
						args := gc.Child(1)
						if args != nil && args.Type(lang) == "arguments" {
							for m := 0; m < args.ChildCount(); m++ {
								arg := args.Child(m)
								if arg.Type(lang) == "string" {
									module = strings.Trim(arg.Text(source), "'\" ")
								}
							}
						}
					}
				}
			}

			if module != "" {
				requires = append(requires, Require{
					Module:  module,
					Binding: binding,
					IsLocal: strings.HasPrefix(module, "."),
					Line:    int(child.StartPoint().Row) + 1,
				})
			}
		}
	}

	return requires
}

// detectModuleExports finds module.exports and exports.X patterns
func (t *TreeSitterAnalyzer) detectModuleExports(root *gotreesitter.Node, source []byte, lang *gotreesitter.Language, filePath string) []ModuleExport {
	var exports []ModuleExport

	for i := 0; i < root.ChildCount(); i++ {
		child := root.Child(i)
		if child.Type(lang) != "expression_statement" {
			continue
		}

		for j := 0; j < child.ChildCount(); j++ {
			expr := child.Child(j)
			if expr.Type(lang) != "assignment_expression" {
				continue
			}

			left := expr.Child(0)
			if left == nil {
				continue
			}

			leftText := left.Text(source)

			if leftText == "module.exports" {
				// module.exports = X
				right := expr.Child(2) // skip "="
				if right == nil {
					continue
				}

				exportType := "value"
				rightType := right.Type(lang)
				rightText := right.Text(source)
				switch {
				case rightType == "function_expression" || rightType == "arrow_function":
					exportType = "function"
				case rightType == "class":
					exportType = "class"
				case rightType == "object":
					exportType = "object"
					rightText = "{"
				case rightType == "identifier":
					exportType = "value"
				}

				exports = append(exports, ModuleExport{
					Name:     rightText,
					Type:     exportType,
					FilePath: filePath,
					Line:     int(child.StartPoint().Row) + 1,
				})

			} else if strings.HasPrefix(leftText, "exports.") {
				// exports.X = ...
				name := strings.TrimPrefix(leftText, "exports.")
				exports = append(exports, ModuleExport{
					Name:     name,
					Type:     "property",
					FilePath: filePath,
					Line:     int(child.StartPoint().Row) + 1,
				})
			}
		}
	}

	return exports
}

// walkTree recursively visits all nodes
func (t *TreeSitterAnalyzer) walkTree(node *gotreesitter.Node, lang *gotreesitter.Language, fn func(*gotreesitter.Node)) {
	if node == nil {
		return
	}
	fn(node)
	for i := 0; i < node.ChildCount(); i++ {
		t.walkTree(node.Child(i), lang, fn)
	}
}

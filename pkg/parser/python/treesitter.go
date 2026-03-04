package python

import (
	"strings"

	"github.com/odvcencio/gotreesitter"
	"github.com/odvcencio/gotreesitter/grammars"
)

// TreeSitterParser uses tree-sitter AST for Python parsing
// Primary engine, falls back to regex-based CodeAnalyzer when needed
type TreeSitterParser struct{}

// NewTreeSitterParser creates a new tree-sitter Python parser
func NewTreeSitterParser() *TreeSitterParser {
	return &TreeSitterParser{}
}

// PyFileAnalysis holds the parsed results from tree-sitter
type PyFileAnalysis struct {
	Functions []FunctionInfo
	Classes   []ClassInfo
	Imports   []ImportInfo
	FilePath  string
}

// Parse parses a Python source file with tree-sitter
func (p *TreeSitterParser) Parse(source []byte, filePath string) (*PyFileAnalysis, error) {
	lang := grammars.DetectLanguage(filePath)
	if lang == nil {
		return nil, nil
	}

	parser := gotreesitter.NewParser(lang.Language())
	tree, err := parser.Parse(source)
	if err != nil {
		return nil, err
	}

	root := tree.RootNode()
	langObj := lang.Language()

	fa := &PyFileAnalysis{FilePath: filePath}

	for i := 0; i < root.ChildCount(); i++ {
		child := root.Child(i)
		nodeType := child.Type(langObj)

		switch nodeType {
		case "function_definition":
			if fn := p.extractFunction(child, source, langObj, filePath); fn != nil {
				fa.Functions = append(fa.Functions, *fn)
			}
		case "class_definition":
			if cls := p.extractClass(child, source, langObj, filePath); cls != nil {
				fa.Classes = append(fa.Classes, *cls)
			}
		case "decorated_definition":
			p.extractDecorated(child, source, langObj, filePath, fa)
		case "import_statement":
			if imp := p.extractImport(child, source, langObj); imp != nil {
				fa.Imports = append(fa.Imports, *imp)
			}
		case "import_from_statement":
			imps := p.extractFromImport(child, source, langObj)
			fa.Imports = append(fa.Imports, imps...)
		}
	}

	return fa, nil
}

// extractFunction extracts a function_definition node
func (p *TreeSitterParser) extractFunction(node *gotreesitter.Node, source []byte, lang *gotreesitter.Language, filePath string) *FunctionInfo {
	fn := &FunctionInfo{
		FilePath:  filePath,
		StartLine: int(node.StartPoint().Row) + 1,
		EndLine:   int(node.EndPoint().Row) + 1,
	}

	for i := 0; i < node.ChildCount(); i++ {
		child := node.Child(i)
		ct := child.Type(lang)
		switch ct {
		case "identifier":
			if fn.Name == "" {
				fn.Name = child.Text(source)
			}
		case "parameters":
			fn.Parameters = p.extractParams(child, source, lang)
		case "type":
			fn.ReturnType = child.Text(source)
		case "async":
			fn.IsAsync = true
		}
	}

	fn.Description = p.extractDocstringFromBody(node, source, lang)
	fn.Signature = p.buildFuncSignature(fn.Name, fn.Parameters, fn.ReturnType, fn.IsAsync)

	return fn
}

// extractClass extracts a class_definition node
func (p *TreeSitterParser) extractClass(node *gotreesitter.Node, source []byte, lang *gotreesitter.Language, filePath string) *ClassInfo {
	cls := &ClassInfo{
		FilePath:  filePath,
		StartLine: int(node.StartPoint().Row) + 1,
		EndLine:   int(node.EndPoint().Row) + 1,
	}

	for i := 0; i < node.ChildCount(); i++ {
		child := node.Child(i)
		ct := child.Type(lang)
		switch ct {
		case "identifier":
			if cls.Name == "" {
				cls.Name = child.Text(source)
			}
		case "argument_list":
			// Base classes: class Foo(Bar, Mixin):
			for j := 0; j < child.ChildCount(); j++ {
				arg := child.Child(j)
				at := arg.Type(lang)
				if at == "identifier" || at == "attribute" {
					cls.Bases = append(cls.Bases, arg.Text(source))
				}
			}
		case "block":
			cls.Description = p.extractDocstringFromBody(node, source, lang)
			cls.Methods = p.extractClassMethods(child, source, lang, cls.Name, filePath)
		}
	}

	// Detect special class types from bases
	for _, base := range cls.Bases {
		switch base {
		case "Enum", "IntEnum", "StrEnum":
			cls.IsEnum = true
		case "Protocol":
			cls.IsProtocol = true
		case "ABC", "ABCMeta":
			cls.IsAbstract = true
		}
		if strings.HasSuffix(cls.Name, "Mixin") {
			cls.IsMixin = true
		}
	}

	// IsPublic is not a stored field; IsEnum/IsProtocol/IsMixin are handled above

	return cls
}

// extractClassMethods extracts methods from a class body block
func (p *TreeSitterParser) extractClassMethods(blockNode *gotreesitter.Node, source []byte, lang *gotreesitter.Language, className, filePath string) []MethodInfo {
	var methods []MethodInfo

	for i := 0; i < blockNode.ChildCount(); i++ {
		child := blockNode.Child(i)
		ct := child.Type(lang)

		var fnNode *gotreesitter.Node
		var decorators []string

		if ct == "function_definition" {
			fnNode = child
		} else if ct == "decorated_definition" {
			for j := 0; j < child.ChildCount(); j++ {
				gc := child.Child(j)
				gct := gc.Type(lang)
				if gct == "decorator" {
					dec := strings.TrimPrefix(gc.Text(source), "@")
					dec = strings.SplitN(dec, "\n", 2)[0]
					decorators = append(decorators, dec)
				} else if gct == "function_definition" {
					fnNode = gc
				}
			}
		}

		if fnNode == nil {
			continue
		}

		method := MethodInfo{
			ClassName: className,
			FilePath:  filePath,
			StartLine: int(fnNode.StartPoint().Row) + 1,
			EndLine:   int(fnNode.EndPoint().Row) + 1,
		}

		for j := 0; j < fnNode.ChildCount(); j++ {
			fc := fnNode.Child(j)
			fct := fc.Type(lang)
			switch fct {
			case "identifier":
				if method.Name == "" {
					method.Name = fc.Text(source)
				}
			case "parameters":
				method.Parameters = p.extractParams(fc, source, lang)
			case "type":
				method.ReturnType = fc.Text(source)
			case "async":
				method.IsAsync = true
			}
		}

		method.Description = p.extractDocstringFromBody(fnNode, source, lang)
		method.Decorators = decorators

		for _, d := range decorators {
			switch d {
			case "classmethod":
				method.IsClassMethod = true
			case "staticmethod":
				method.IsStatic = true
			case "property":
				method.IsProperty = true
			case "abstractmethod":
				method.IsAbstract = true
			}
		}

		if method.Name == "__init__" {
			method.IsClassMethod = false
		}

		method.Signature = p.buildMethodSignature(method.Name, method.Parameters, method.ReturnType, method.IsAsync)
		methods = append(methods, method)
	}

	return methods
}

// extractDecorated extracts a decorated function or class at module level
func (p *TreeSitterParser) extractDecorated(node *gotreesitter.Node, source []byte, lang *gotreesitter.Language, filePath string, fa *PyFileAnalysis) {
	var decorators []string

	for i := 0; i < node.ChildCount(); i++ {
		child := node.Child(i)
		ct := child.Type(lang)
		switch ct {
		case "decorator":
			dec := strings.TrimPrefix(child.Text(source), "@")
			dec = strings.SplitN(dec, "\n", 2)[0]
			decorators = append(decorators, dec)
		case "function_definition":
			if fn := p.extractFunction(child, source, lang, filePath); fn != nil {
				fn.Decorators = decorators
				fa.Functions = append(fa.Functions, *fn)
			}
		case "class_definition":
			if cls := p.extractClass(child, source, lang, filePath); cls != nil {
				cls.Decorators = decorators
				for _, d := range decorators {
					if d == "dataclass" || strings.Contains(d, "dataclass") {
						cls.IsDataclass = true
					}
				}
				fa.Classes = append(fa.Classes, *cls)
			}
		}
	}
}

// extractParams extracts function parameters from a parameters node
func (p *TreeSitterParser) extractParams(node *gotreesitter.Node, source []byte, lang *gotreesitter.Language) []ParamInfo {
	var params []ParamInfo

	for i := 0; i < node.ChildCount(); i++ {
		child := node.Child(i)
		ct := child.Type(lang)

		switch ct {
		case "identifier":
			params = append(params, ParamInfo{Name: child.Text(source)})

		case "typed_parameter":
			param := ParamInfo{}
			for j := 0; j < child.ChildCount(); j++ {
				gc := child.Child(j)
				gct := gc.Type(lang)
				switch gct {
				case "identifier":
					if param.Name == "" {
						param.Name = gc.Text(source)
					}
				case "type", "generic_type", "union_type", "attribute":
					param.Type = gc.Text(source)
				}
			}
			params = append(params, param)

		case "default_parameter", "typed_default_parameter":
			param := ParamInfo{}
			for j := 0; j < child.ChildCount(); j++ {
				gc := child.Child(j)
				gct := gc.Type(lang)
				if gct == "identifier" && param.Name == "" {
					param.Name = gc.Text(source)
				} else if gct == "type" || gct == "generic_type" {
					param.Type = gc.Text(source)
				}
			}
			params = append(params, param)

		case "list_splat_pattern": // *args
			params = append(params, ParamInfo{Name: child.Text(source)})

		case "dictionary_splat_pattern": // **kwargs
			params = append(params, ParamInfo{Name: child.Text(source)})
		}
	}

	return params
}

// extractImport extracts a simple import statement
func (p *TreeSitterParser) extractImport(node *gotreesitter.Node, source []byte, lang *gotreesitter.Language) *ImportInfo {
	imp := &ImportInfo{}
	for i := 0; i < node.ChildCount(); i++ {
		child := node.Child(i)
		ct := child.Type(lang)
		if ct == "dotted_name" || ct == "identifier" {
			if imp.Module == "" {
				imp.Module = child.Text(source)
			}
		}
		if ct == "aliased_import" {
			for j := 0; j < child.ChildCount(); j++ {
				gc := child.Child(j)
				gct := gc.Type(lang)
				if (gct == "dotted_name" || gct == "identifier") && imp.Module == "" {
					imp.Module = gc.Text(source)
				} else if gct == "identifier" {
					imp.Alias = gc.Text(source)
				}
			}
		}
	}
	if imp.Module == "" {
		return nil
	}
	imp.StartLine = int(node.StartPoint().Row) + 1
	return imp
}

// extractFromImport extracts from X import Y, Z statements
func (p *TreeSitterParser) extractFromImport(node *gotreesitter.Node, source []byte, lang *gotreesitter.Language) []ImportInfo {
	var imports []ImportInfo

	// node: from <module> import <names>
	module := ""
	importSeen := false

	for i := 0; i < node.ChildCount(); i++ {
		child := node.Child(i)
		ct := child.Type(lang)

		switch ct {
		case "dotted_name", "relative_import":
			if module == "" {
				module = child.Text(source)
			}
		case "import":
			importSeen = true
		case "identifier":
			if importSeen && module != "" {
				imports = append(imports, ImportInfo{
					Module:    module,
					Names:     []string{child.Text(source)},
					IsFrom:    true,
					StartLine: int(node.StartPoint().Row) + 1,
				})
			}
		case "aliased_import":
			if importSeen {
				name := ""
				alias := ""
				for j := 0; j < child.ChildCount(); j++ {
					gc := child.Child(j)
					gct := gc.Type(lang)
					if (gct == "identifier" || gct == "dotted_name") && name == "" {
						name = gc.Text(source)
					} else if gct == "identifier" {
						alias = gc.Text(source)
					}
				}
				imports = append(imports, ImportInfo{
					Module:    module,
					Names:     []string{name},
					Alias:     alias,
					IsFrom:    true,
					StartLine: int(node.StartPoint().Row) + 1,
				})
			}
		case "wildcard_import":
			imports = append(imports, ImportInfo{
				Module:    module,
				Names:     []string{"*"},
				IsFrom:    true,
				StartLine: int(node.StartPoint().Row) + 1,
			})
		}
	}

	return imports
}

// extractDocstringFromBody extracts the first docstring from a function/class body
func (p *TreeSitterParser) extractDocstringFromBody(fnNode *gotreesitter.Node, source []byte, lang *gotreesitter.Language) string {
	for i := 0; i < fnNode.ChildCount(); i++ {
		child := fnNode.Child(i)
		if child.Type(lang) != "block" {
			continue
		}
		for j := 0; j < child.ChildCount(); j++ {
			stmt := child.Child(j)
			if stmt.Type(lang) != "expression_statement" {
				break
			}
			for k := 0; k < stmt.ChildCount(); k++ {
				expr := stmt.Child(k)
				if expr.Type(lang) == "string" {
					text := expr.Text(source)
					// Strip triple quotes
					for _, q := range []string{`"""`, `'''`} {
						text = strings.TrimPrefix(text, q)
						text = strings.TrimSuffix(text, q)
					}
					text = strings.Trim(text, `"'`)
					return strings.TrimSpace(text)
				}
			}
			break
		}
	}
	return ""
}

// buildFuncSignature builds a function signature string
func (p *TreeSitterParser) buildFuncSignature(name string, params []ParamInfo, returnType string, isAsync bool) string {
	var sb strings.Builder
	if isAsync {
		sb.WriteString("async ")
	}
	sb.WriteString("def ")
	sb.WriteString(name)
	sb.WriteString("(")
	for i, param := range params {
		if i > 0 {
			sb.WriteString(", ")
		}
		sb.WriteString(param.Name)
		if param.Type != "" {
			sb.WriteString(": ")
			sb.WriteString(param.Type)
		}
	}
	sb.WriteString(")")
	if returnType != "" {
		sb.WriteString(" -> ")
		sb.WriteString(returnType)
	}
	return sb.String()
}

// buildMethodSignature is the same as buildFuncSignature
func (p *TreeSitterParser) buildMethodSignature(name string, params []ParamInfo, returnType string, isAsync bool) string {
	return p.buildFuncSignature(name, params, returnType, isAsync)
}

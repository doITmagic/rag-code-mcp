package typescript

import (
	"strings"
	"sync"

	"github.com/odvcencio/gotreesitter"
	"github.com/odvcencio/gotreesitter/grammars"
)

// Utility types recognized
var utilityTypes = map[string]bool{
	"Partial": true, "Required": true, "Readonly": true,
	"Pick": true, "Omit": true, "Record": true,
	"Exclude": true, "Extract": true, "NonNullable": true,
	"ReturnType": true, "InstanceType": true, "Parameters": true,
	"ConstructorParameters": true, "Awaited": true,
	"ThisParameterType": true, "OmitThisParameter": true,
}

// Analyzer detects TypeScript-specific patterns using tree-sitter.
// Caches Parser instances per language to avoid re-allocating expensive lookup tables.
type Analyzer struct {
	mu      sync.Mutex
	parsers map[string]*gotreesitter.Parser
}

// NewAnalyzer creates a new TypeScript analyzer
func NewAnalyzer() *Analyzer {
	return &Analyzer{
		parsers: make(map[string]*gotreesitter.Parser),
	}
}

func (a *Analyzer) getOrCreateParser(lang *grammars.LangEntry) *gotreesitter.Parser {
	a.mu.Lock()
	defer a.mu.Unlock()
	if cached, ok := a.parsers[lang.Name]; ok {
		return cached
	}
	p := gotreesitter.NewParser(lang.Language())
	a.parsers[lang.Name] = p
	return p
}

// IsTypeScriptFile checks if a file is TypeScript
func IsTypeScriptFile(filePath string) bool {
	return strings.HasSuffix(filePath, ".ts") || strings.HasSuffix(filePath, ".tsx") ||
		strings.HasSuffix(filePath, ".d.ts") || strings.HasSuffix(filePath, ".mts") ||
		strings.HasSuffix(filePath, ".cts")
}

// Analyze performs TypeScript-specific analysis using tree-sitter AST
func (a *Analyzer) Analyze(source string, filePath string) *TypeScriptInfo {
	if !IsTypeScriptFile(filePath) {
		return &TypeScriptInfo{}
	}

	lang := grammars.DetectLanguage(filePath)
	if lang == nil {
		return &TypeScriptInfo{}
	}

	src := []byte(source)
	parser := a.getOrCreateParser(lang)
	tree, err := parser.Parse(src)
	if err != nil {
		return &TypeScriptInfo{}
	}
	defer tree.Release()

	root := tree.RootNode()
	langObj := lang.Language()

	info := &TypeScriptInfo{}

	info.Generics = a.detectGenerics(root, src, langObj, filePath)
	info.Decorators = a.detectDecorators(root, src, langObj, filePath)
	info.TypeGuards = a.detectTypeGuards(root, src, langObj, filePath)
	info.Namespaces = a.detectNamespaces(root, src, langObj, filePath)
	info.MappedTypes = a.detectMappedTypes(root, src, langObj, filePath)
	info.Overloads = a.detectOverloads(root, src, langObj, filePath)

	// Declaration files
	if strings.HasSuffix(filePath, ".d.ts") {
		info.DeclFiles = a.detectDeclFile(root, src, langObj, filePath)
	}

	return info
}

// detectGenerics finds generic type parameters on functions, classes, interfaces
func (a *Analyzer) detectGenerics(root *gotreesitter.Node, source []byte, lang *gotreesitter.Language, filePath string) []Generic {
	var generics []Generic

	a.walkTree(root, lang, func(node *gotreesitter.Node) {
		nodeType := node.Type(lang)

		// Look for type_parameters node
		if nodeType != "type_parameters" {
			return
		}

		parent := node.Parent()
		if parent == nil {
			return
		}

		// Get the name from the parent declaration
		var name string
		parentType := parent.Type(lang)
		switch parentType {
		case "function_declaration", "class_declaration", "interface_declaration",
			"type_alias_declaration", "method_definition":
			for i := 0; i < parent.ChildCount(); i++ {
				child := parent.Child(i)
				ct := child.Type(lang)
				if ct == "identifier" || ct == "type_identifier" {
					name = child.Text(source)
					break
				}
			}
		}

		if name == "" {
			return
		}

		// Extract type parameters
		var params []string
		for i := 0; i < node.ChildCount(); i++ {
			child := node.Child(i)
			ct := child.Type(lang)
			if ct == "type_parameter" {
				params = append(params, child.Text(source))
			}
		}

		if len(params) > 0 {
			generics = append(generics, Generic{
				Name:       name,
				TypeParams: params,
				FilePath:   filePath,
				Line:       int(node.StartPoint().Row) + 1,
			})
		}
	})

	return generics
}

// detectDecorators finds TypeScript decorators (@xxx)
func (a *Analyzer) detectDecorators(root *gotreesitter.Node, source []byte, lang *gotreesitter.Language, filePath string) []Decorator {
	var decorators []Decorator

	a.walkTree(root, lang, func(node *gotreesitter.Node) {
		nodeType := node.Type(lang)
		if nodeType != "decorator" {
			return
		}

		text := node.Text(source)
		name := strings.TrimPrefix(text, "@")

		// Extract decorator name (before parentheses)
		if idx := strings.Index(name, "("); idx != -1 {
			name = name[:idx]
		}

		// Determine target
		parent := node.Parent()
		target := "unknown"
		targetName := ""

		if parent != nil {
			switch parent.Type(lang) {
			case "class_declaration":
				target = "class"
				for i := 0; i < parent.ChildCount(); i++ {
					child := parent.Child(i)
					if child.Type(lang) == "identifier" || child.Type(lang) == "type_identifier" {
						targetName = child.Text(source)
						break
					}
				}
			case "method_definition":
				target = "method"
				for i := 0; i < parent.ChildCount(); i++ {
					child := parent.Child(i)
					if child.Type(lang) == "property_identifier" {
						targetName = child.Text(source)
						break
					}
				}
			case "public_field_definition", "property_declaration":
				target = "property"
			}
		}

		// Extract args
		args := ""
		if idx := strings.Index(text, "("); idx != -1 {
			end := strings.LastIndex(text, ")")
			if end > idx {
				args = text[idx+1 : end]
			}
		}

		decorators = append(decorators, Decorator{
			Name:       name,
			Target:     target,
			TargetName: targetName,
			Args:       args,
			FilePath:   filePath,
			Line:       int(node.StartPoint().Row) + 1,
		})
	})

	return decorators
}

// detectTypeGuards finds functions with "x is Type" return type
func (a *Analyzer) detectTypeGuards(root *gotreesitter.Node, source []byte, lang *gotreesitter.Language, filePath string) []TypeGuard {
	var guards []TypeGuard

	a.walkTree(root, lang, func(node *gotreesitter.Node) {
		nodeType := node.Type(lang)
		if nodeType != "type_predicate" {
			return
		}

		text := node.Text(source)
		// Parse "x is Type"
		parts := strings.SplitN(text, " is ", 2)
		if len(parts) != 2 {
			return
		}

		paramName := strings.TrimSpace(parts[0])
		guardType := strings.TrimSpace(parts[1])

		// Find the function name
		funcName := ""
		parent := node.Parent()
		for parent != nil {
			pt := parent.Type(lang)
			if pt == "function_declaration" || pt == "method_definition" {
				for i := 0; i < parent.ChildCount(); i++ {
					child := parent.Child(i)
					ct := child.Type(lang)
					if ct == "identifier" || ct == "property_identifier" {
						funcName = child.Text(source)
						break
					}
				}
				break
			}
			parent = parent.Parent()
		}

		guards = append(guards, TypeGuard{
			Name:      funcName,
			ParamName: paramName,
			GuardType: guardType,
			FilePath:  filePath,
			Line:      int(node.StartPoint().Row) + 1,
		})
	})

	return guards
}

// detectNamespaces finds TypeScript namespace/module declarations
func (a *Analyzer) detectNamespaces(root *gotreesitter.Node, source []byte, lang *gotreesitter.Language, filePath string) []Namespace {
	var namespaces []Namespace

	for i := 0; i < root.ChildCount(); i++ {
		child := root.Child(i)
		nodeType := child.Type(lang)

		isExported := false
		var targetNode *gotreesitter.Node

		if nodeType == "export_statement" {
			isExported = true
			for j := 0; j < child.ChildCount(); j++ {
				gc := child.Child(j)
				if gc.Type(lang) == "module" || gc.Type(lang) == "internal_module" {
					targetNode = gc
				}
			}
		} else if nodeType == "module" || nodeType == "internal_module" {
			targetNode = child
		}

		if targetNode == nil {
			continue
		}

		text := targetNode.Text(source)
		isModule := strings.HasPrefix(text, "module ")

		var name string
		for j := 0; j < targetNode.ChildCount(); j++ {
			gc := targetNode.Child(j)
			ct := gc.Type(lang)
			if ct == "identifier" || ct == "string" {
				name = strings.Trim(gc.Text(source), "'\"")
				break
			}
		}

		if name != "" {
			namespaces = append(namespaces, Namespace{
				Name:       name,
				IsModule:   isModule,
				IsExported: isExported,
				FilePath:   filePath,
				StartLine:  int(targetNode.StartPoint().Row) + 1,
				EndLine:    int(targetNode.EndPoint().Row) + 1,
			})
		}
	}

	return namespaces
}

// detectMappedTypes finds usage of TypeScript utility types (Partial<T>, Pick<T, K>, etc.)
func (a *Analyzer) detectMappedTypes(root *gotreesitter.Node, source []byte, lang *gotreesitter.Language, filePath string) []MappedType {
	var mapped []MappedType
	seen := make(map[string]bool)

	a.walkTree(root, lang, func(node *gotreesitter.Node) {
		if node.Type(lang) != "generic_type" {
			return
		}

		text := node.Text(source)

		// Extract the base type name
		firstChild := node.Child(0)
		if firstChild == nil {
			return
		}

		name := firstChild.Text(source)
		if !utilityTypes[name] {
			return
		}

		key := name + ":" + text
		if seen[key] {
			return
		}
		seen[key] = true

		// Extract the base type argument
		baseType := ""
		for i := 0; i < node.ChildCount(); i++ {
			child := node.Child(i)
			if child.Type(lang) == "type_arguments" {
				baseType = child.Text(source)
				// Clean up angle brackets
				baseType = strings.TrimPrefix(baseType, "<")
				baseType = strings.TrimSuffix(baseType, ">")
				break
			}
		}

		mapped = append(mapped, MappedType{
			Name:     name,
			BaseType: baseType,
			FilePath: filePath,
			Line:     int(node.StartPoint().Row) + 1,
		})
	})

	return mapped
}

// detectOverloads finds function overload signatures
func (a *Analyzer) detectOverloads(root *gotreesitter.Node, source []byte, lang *gotreesitter.Language, filePath string) []Overload {
	var overloads []Overload

	// Overloads in TS are function declarations without a body
	// followed by the implementation
	a.walkTree(root, lang, func(node *gotreesitter.Node) {
		nodeType := node.Type(lang)
		if nodeType != "function_signature" && nodeType != "method_signature" {
			return
		}

		var name string
		var params []string
		var returnType string

		for i := 0; i < node.ChildCount(); i++ {
			child := node.Child(i)
			ct := child.Type(lang)
			switch ct {
			case "identifier", "property_identifier":
				if name == "" {
					name = child.Text(source)
				}
			case "formal_parameters", "call_signature":
				for j := 0; j < child.ChildCount(); j++ {
					param := child.Child(j)
					pt := param.Type(lang)
					if pt == "required_parameter" || pt == "optional_parameter" {
						params = append(params, param.Text(source))
					}
				}
			case "type_annotation":
				returnType = strings.TrimPrefix(child.Text(source), ": ")
			}
		}

		if name != "" {
			overloads = append(overloads, Overload{
				Name:       name,
				Params:     params,
				ReturnType: returnType,
				FilePath:   filePath,
				Line:       int(node.StartPoint().Row) + 1,
			})
		}
	})

	return overloads
}

// detectDeclFile analyzes .d.ts declaration files
func (a *Analyzer) detectDeclFile(root *gotreesitter.Node, source []byte, lang *gotreesitter.Language, filePath string) []DeclFile {
	declFile := DeclFile{
		FilePath: filePath,
	}

	for i := 0; i < root.ChildCount(); i++ {
		child := root.Child(i)
		text := child.Text(source)

		// Look for declare module 'xxx'
		if strings.Contains(text, "declare module") {
			if start := strings.IndexAny(text, "'\""); start != -1 {
				end := strings.LastIndexAny(text, "'\"")
				if end > start {
					declFile.ModuleName = text[start+1 : end]
				}
			}
		}

		// Collect top-level declarations
		nodeType := child.Type(lang)
		switch nodeType {
		case "interface_declaration", "type_alias_declaration", "enum_declaration",
			"class_declaration", "function_declaration":
			for j := 0; j < child.ChildCount(); j++ {
				gc := child.Child(j)
				ct := gc.Type(lang)
				if ct == "identifier" || ct == "type_identifier" {
					declFile.Declarations = append(declFile.Declarations, gc.Text(source))
					break
				}
			}
		case "export_statement":
			for j := 0; j < child.ChildCount(); j++ {
				gc := child.Child(j)
				gcType := gc.Type(lang)
				if gcType == "interface_declaration" || gcType == "type_alias_declaration" ||
					gcType == "enum_declaration" || gcType == "class_declaration" {
					for k := 0; k < gc.ChildCount(); k++ {
						gk := gc.Child(k)
						if gk.Type(lang) == "identifier" || gk.Type(lang) == "type_identifier" {
							declFile.Declarations = append(declFile.Declarations, gk.Text(source))
							break
						}
					}
				}
			}
		}
	}

	if declFile.ModuleName != "" || len(declFile.Declarations) > 0 {
		return []DeclFile{declFile}
	}
	return nil
}

// walkTree recursively visits all nodes
func (a *Analyzer) walkTree(node *gotreesitter.Node, lang *gotreesitter.Language, fn func(*gotreesitter.Node)) {
	if node == nil {
		return
	}
	fn(node)
	for i := 0; i < node.ChildCount(); i++ {
		a.walkTree(node.Child(i), lang, fn)
	}
}

`package javascript

import (
	"strings"

	"github.com/odvcencio/gotreesitter"
	"github.com/odvcencio/gotreesitter/grammars"
)

// TreeSitterParser uses gotreesitter (pure Go, zero CGO) for accurate JS/TS AST parsing
type TreeSitterParser struct{}

// NewTreeSitterParser creates a new tree-sitter based parser
func NewTreeSitterParser() *TreeSitterParser {
	return &TreeSitterParser{}
}

// ParseFile parses a JS/TS file using tree-sitter and returns extracted info
func (p *TreeSitterParser) ParseFile(source []byte, filePath string) (*fileAnalysis, error) {
	lang := grammars.DetectLanguage(filePath)
	if lang == nil {
		return nil, nil // unsupported extension
	}

	parser := gotreesitter.NewParser(lang.Language())
	tree, err := parser.Parse(source)
	if err != nil {
		return nil, err
	}

	root := tree.RootNode()
	langObj := lang.Language()

	fa := &fileAnalysis{
		FilePath: filePath,
		Language: detectLanguage(filePath),
	}

	// Walk the AST top-level nodes
	for i := 0; i < root.ChildCount(); i++ {
		child := root.Child(i)
		nodeType := child.Type(langObj)

		switch nodeType {
		case "function_declaration":
			fn := p.extractFunction(child, source, langObj, filePath)
			if fn != nil {
				fa.Functions = append(fa.Functions, *fn)
			}

		case "export_statement":
			p.processExportStatement(child, source, langObj, filePath, fa)

		case "class_declaration":
			cls := p.extractClass(child, source, langObj, filePath)
			if cls != nil {
				fa.Classes = append(fa.Classes, *cls)
			}

		case "lexical_declaration", "variable_declaration":
			fns := p.extractArrowFromDeclaration(child, source, langObj, filePath)
			fa.Functions = append(fa.Functions, fns...)

		case "import_statement":
			imp := p.extractImport(child, source, langObj)
			if imp != nil {
				fa.Imports = append(fa.Imports, *imp)
			}

		case "interface_declaration":
			iface := p.extractInterface(child, source, langObj, filePath)
			if iface != nil {
				fa.Interfaces = append(fa.Interfaces, *iface)
			}

		case "type_alias_declaration":
			ta := p.extractTypeAlias(child, source, langObj, filePath)
			if ta != nil {
				fa.Types = append(fa.Types, *ta)
			}

		case "enum_declaration":
			en := p.extractEnum(child, source, langObj, filePath)
			if en != nil {
				fa.Enums = append(fa.Enums, *en)
			}
		}
	}

	return fa, nil
}

// --- Extraction helpers ---

func (p *TreeSitterParser) extractFunction(node *gotreesitter.Node, source []byte, lang *gotreesitter.Language, filePath string) *JSFunction {
	fn := &JSFunction{
		FilePath:  filePath,
		StartLine: int(node.StartPoint().Row) + 1,
		EndLine:   int(node.EndPoint().Row) + 1,
	}

	for i := 0; i < node.ChildCount(); i++ {
		child := node.Child(i)
		switch child.Type(lang) {
		case "identifier":
			fn.Name = child.Text(source)
		case "formal_parameters":
			fn.Params = p.extractParams(child, source, lang)
		case "async":
			fn.IsAsync = true
		case "type_annotation":
			fn.ReturnType = child.Text(source)
		}
	}

	return fn
}

func (p *TreeSitterParser) extractClass(node *gotreesitter.Node, source []byte, lang *gotreesitter.Language, filePath string) *JSClass {
	cls := &JSClass{
		FilePath:  filePath,
		StartLine: int(node.StartPoint().Row) + 1,
		EndLine:   int(node.EndPoint().Row) + 1,
	}

	for i := 0; i < node.ChildCount(); i++ {
		child := node.Child(i)
		childType := child.Type(lang)

		switch childType {
		case "type_identifier", "identifier":
			if cls.Name == "" {
				cls.Name = child.Text(source)
			}
		case "class_heritage":
			p.extractClassHeritage(child, source, lang, cls)
		case "class_body":
			cls.Methods = p.extractClassMethods(child, source, lang)
		case "abstract":
			cls.IsAbstract = true
		}
	}

	return cls
}

func (p *TreeSitterParser) extractClassHeritage(node *gotreesitter.Node, source []byte, lang *gotreesitter.Language, cls *JSClass) {
	// In gotreesitter, class_heritage contains direct children:
	// - "extends" keyword + identifier (for extends)
	// - extends_clause wrapper (for some grammars)
	// - implements_clause (for TS)
	inExtends := false
	for i := 0; i < node.ChildCount(); i++ {
		child := node.Child(i)
		childType := child.Type(lang)

		switch childType {
		case "extends":
			inExtends = true
		case "identifier", "member_expression":
			if inExtends && cls.Extends == "" {
				cls.Extends = child.Text(source)
				inExtends = false
			}
		case "extends_clause":
			// Some grammars wrap in extends_clause
			for j := 0; j < child.ChildCount(); j++ {
				gc := child.Child(j)
				gcType := gc.Type(lang)
				if gcType == "identifier" || gcType == "member_expression" {
					cls.Extends = gc.Text(source)
				}
			}
		case "implements_clause":
			for j := 0; j < child.ChildCount(); j++ {
				gc := child.Child(j)
				gcType := gc.Type(lang)
				if gcType == "type_identifier" || gcType == "identifier" || gcType == "generic_type" {
					cls.Implements = append(cls.Implements, gc.Text(source))
				}
			}
		}
	}
}

func (p *TreeSitterParser) extractClassMethods(bodyNode *gotreesitter.Node, source []byte, lang *gotreesitter.Language) []JSMethod {
	var methods []JSMethod

	for i := 0; i < bodyNode.ChildCount(); i++ {
		child := bodyNode.Child(i)
		childType := child.Type(lang)

		if childType != "method_definition" {
			continue
		}

		method := JSMethod{
			StartLine:  int(child.StartPoint().Row) + 1,
			EndLine:    int(child.EndPoint().Row) + 1,
			Visibility: "public",
		}

		for j := 0; j < child.ChildCount(); j++ {
			gc := child.Child(j)
			gcType := gc.Type(lang)

			switch gcType {
			case "property_identifier":
				method.Name = gc.Text(source)
			case "formal_parameters":
				method.Params = p.extractParams(gc, source, lang)
			case "async":
				method.IsAsync = true
			case "static":
				method.IsStatic = true
			case "accessibility_modifier":
				method.Visibility = gc.Text(source)
				if method.Visibility == "private" {
					method.IsPrivate = true
				}
			case "type_annotation":
				method.ReturnType = gc.Text(source)
			case "private_property_identifier":
				method.Name = gc.Text(source)
				method.IsPrivate = true
				method.Visibility = "private"
			}
		}

		if method.Name == "" {
			continue
		}

		methods = append(methods, method)
	}

	return methods
}

func (p *TreeSitterParser) extractParams(node *gotreesitter.Node, source []byte, lang *gotreesitter.Language) []string {
	var params []string

	for i := 0; i < node.ChildCount(); i++ {
		child := node.Child(i)
		childType := child.Type(lang)

		switch childType {
		case "identifier", "shorthand_property_identifier_pattern":
			params = append(params, child.Text(source))
		case "required_parameter", "optional_parameter":
			// Extract just the name part
			for j := 0; j < child.ChildCount(); j++ {
				gc := child.Child(j)
				gcType := gc.Type(lang)
				if gcType == "identifier" {
					params = append(params, gc.Text(source))
					break
				}
			}
		case "rest_pattern":
			for j := 0; j < child.ChildCount(); j++ {
				gc := child.Child(j)
				if gc.Type(lang) == "identifier" {
					params = append(params, gc.Text(source))
				}
			}
		case "object_pattern":
			// Destructured params: { name, age }
			for j := 0; j < child.ChildCount(); j++ {
				gc := child.Child(j)
				gcType := gc.Type(lang)
				if gcType == "shorthand_property_identifier_pattern" || gcType == "identifier" {
					params = append(params, gc.Text(source))
				} else if gcType == "pair_pattern" {
					// name: value
					for k := 0; k < gc.ChildCount(); k++ {
						gk := gc.Child(k)
						if gk.Type(lang) == "property_identifier" {
							params = append(params, gk.Text(source))
							break
						}
					}
				}
			}
		}
	}

	return params
}

func (p *TreeSitterParser) processExportStatement(node *gotreesitter.Node, source []byte, lang *gotreesitter.Language, filePath string, fa *fileAnalysis) {
	isDefault := false

	for i := 0; i < node.ChildCount(); i++ {
		child := node.Child(i)
		childType := child.Type(lang)

		if childType == "default" {
			isDefault = true
			continue
		}

		switch childType {
		case "function_declaration":
			fn := p.extractFunction(child, source, lang, filePath)
			if fn != nil {
				fn.IsExported = true
				fn.IsDefault = isDefault
				fa.Functions = append(fa.Functions, *fn)
			}

		case "class_declaration":
			cls := p.extractClass(child, source, lang, filePath)
			if cls != nil {
				cls.IsExported = true
				cls.IsDefault = isDefault
				fa.Classes = append(fa.Classes, *cls)
			}

		case "lexical_declaration":
			fns := p.extractArrowFromDeclaration(child, source, lang, filePath)
			for j := range fns {
				fns[j].IsExported = true
				fns[j].IsDefault = isDefault
			}
			fa.Functions = append(fa.Functions, fns...)

		case "interface_declaration":
			iface := p.extractInterface(child, source, lang, filePath)
			if iface != nil {
				iface.IsExported = true
				fa.Interfaces = append(fa.Interfaces, *iface)
			}

		case "type_alias_declaration":
			ta := p.extractTypeAlias(child, source, lang, filePath)
			if ta != nil {
				ta.IsExported = true
				fa.Types = append(fa.Types, *ta)
			}

		case "enum_declaration":
			en := p.extractEnum(child, source, lang, filePath)
			if en != nil {
				en.IsExported = true
				fa.Enums = append(fa.Enums, *en)
			}

		case "export_clause":
			// export { X, Y }
			for j := 0; j < child.ChildCount(); j++ {
				spec := child.Child(j)
				if spec.Type(lang) == "export_specifier" {
					name := spec.Child(0)
					if name != nil {
						fa.Exports = append(fa.Exports, JSExport{
							Name: name.Text(source),
							Line: int(spec.StartPoint().Row) + 1,
						})
					}
				}
			}

		case "identifier":
			fa.Exports = append(fa.Exports, JSExport{
				Name:      child.Text(source),
				IsDefault: isDefault,
				Line:      int(child.StartPoint().Row) + 1,
			})
		}
	}
}

func (p *TreeSitterParser) extractArrowFromDeclaration(node *gotreesitter.Node, source []byte, lang *gotreesitter.Language, filePath string) []JSFunction {
	var fns []JSFunction

	for i := 0; i < node.ChildCount(); i++ {
		child := node.Child(i)
		if child.Type(lang) != "variable_declarator" {
			continue
		}

		var name string
		var isArrow, isAsync bool
		var params []string

		for j := 0; j < child.ChildCount(); j++ {
			gc := child.Child(j)
			gcType := gc.Type(lang)

			switch gcType {
			case "identifier":
				name = gc.Text(source)
			case "arrow_function":
				isArrow = true
				for k := 0; k < gc.ChildCount(); k++ {
					gk := gc.Child(k)
					gkType := gk.Type(lang)
					switch gkType {
					case "formal_parameters":
						params = p.extractParams(gk, source, lang)
					case "async":
						isAsync = true
					case "identifier":
						// Single param arrow: x => ...
						if len(params) == 0 {
							params = []string{gk.Text(source)}
						}
					}
				}
			case "function_expression", "function":
				// const fn = function() {}
				for k := 0; k < gc.ChildCount(); k++ {
					gk := gc.Child(k)
					gkType := gk.Type(lang)
					switch gkType {
					case "formal_parameters":
						params = p.extractParams(gk, source, lang)
					case "async":
						isAsync = true
					}
				}
			}
		}

		if name != "" && (isArrow || child.ChildCount() > 1) {
			// Only add if it looks like a function assignment
			text := child.Text(source)
			if strings.Contains(text, "=>") || strings.Contains(text, "function") {
				fns = append(fns, JSFunction{
					Name:      name,
					IsArrow:   isArrow,
					IsAsync:   isAsync,
					Params:    params,
					FilePath:  filePath,
					StartLine: int(node.StartPoint().Row) + 1,
					EndLine:   int(node.EndPoint().Row) + 1,
				})
			}
		}
	}

	return fns
}

func (p *TreeSitterParser) extractImport(node *gotreesitter.Node, source []byte, lang *gotreesitter.Language) *JSImport {
	imp := &JSImport{
		Line: int(node.StartPoint().Row) + 1,
	}

	for i := 0; i < node.ChildCount(); i++ {
		child := node.Child(i)
		childType := child.Type(lang)

		switch childType {
		case "import_clause":
			for j := 0; j < child.ChildCount(); j++ {
				gc := child.Child(j)
				gcType := gc.Type(lang)
				switch gcType {
				case "identifier":
					imp.Default = gc.Text(source)
				case "named_imports":
					for k := 0; k < gc.ChildCount(); k++ {
						spec := gc.Child(k)
						if spec.Type(lang) == "import_specifier" {
							name := spec.Child(0)
							if name != nil {
								imp.Named = append(imp.Named, name.Text(source))
							}
						}
					}
				case "namespace_import":
					for k := 0; k < gc.ChildCount(); k++ {
						ns := gc.Child(k)
						if ns.Type(lang) == "identifier" {
							imp.Namespace = ns.Text(source)
						}
					}
				}
			}
		case "string", "template_string":
			imp.Source = strings.Trim(child.Text(source), "'\"` ")
		}
	}

	if imp.Source == "" {
		return nil
	}

	// Detect type imports
	text := node.Text(source)
	if strings.Contains(text, "import type") {
		imp.IsType = true
	}

	return imp
}

func (p *TreeSitterParser) extractInterface(node *gotreesitter.Node, source []byte, lang *gotreesitter.Language, filePath string) *TSInterface {
	iface := &TSInterface{
		FilePath:  filePath,
		StartLine: int(node.StartPoint().Row) + 1,
		EndLine:   int(node.EndPoint().Row) + 1,
	}

	for i := 0; i < node.ChildCount(); i++ {
		child := node.Child(i)
		childType := child.Type(lang)

		switch childType {
		case "type_identifier":
			iface.Name = child.Text(source)
		case "extends_type_clause":
			for j := 0; j < child.ChildCount(); j++ {
				gc := child.Child(j)
				gcType := gc.Type(lang)
				if gcType == "type_identifier" || gcType == "generic_type" {
					iface.Extends = append(iface.Extends, gc.Text(source))
				}
			}
		case "object_type", "interface_body":
			iface.Properties = p.extractTSObjectProperties(child, source, lang)
		}
	}

	return iface
}

func (p *TreeSitterParser) extractTSObjectProperties(node *gotreesitter.Node, source []byte, lang *gotreesitter.Language) []TSProperty {
	var props []TSProperty

	for i := 0; i < node.ChildCount(); i++ {
		child := node.Child(i)
		childType := child.Type(lang)

		if childType != "property_signature" {
			continue
		}

		prop := TSProperty{}
		for j := 0; j < child.ChildCount(); j++ {
			gc := child.Child(j)
			gcType := gc.Type(lang)
			switch gcType {
			case "property_identifier":
				prop.Name = gc.Text(source)
			case "type_annotation":
				prop.Type = gc.Text(source)
				// Clean up ": string" -> "string"
				prop.Type = strings.TrimPrefix(prop.Type, ": ")
			case "?":
				prop.Optional = true
			}
		}
		if prop.Name != "" {
			props = append(props, prop)
		}
	}

	return props
}

func (p *TreeSitterParser) extractTypeAlias(node *gotreesitter.Node, source []byte, lang *gotreesitter.Language, filePath string) *TSTypeAlias {
	ta := &TSTypeAlias{
		FilePath:  filePath,
		StartLine: int(node.StartPoint().Row) + 1,
		EndLine:   int(node.EndPoint().Row) + 1,
	}

	for i := 0; i < node.ChildCount(); i++ {
		child := node.Child(i)
		childType := child.Type(lang)

		switch childType {
		case "type_identifier":
			ta.Name = child.Text(source)
		case "type", "union_type", "intersection_type", "function_type",
			"object_type", "predefined_type", "literal_type", "template_literal_type":
			ta.Definition = child.Text(source)
		}
	}

	return ta
}

func (p *TreeSitterParser) extractEnum(node *gotreesitter.Node, source []byte, lang *gotreesitter.Language, filePath string) *TSEnum {
	en := &TSEnum{
		FilePath:  filePath,
		StartLine: int(node.StartPoint().Row) + 1,
		EndLine:   int(node.EndPoint().Row) + 1,
	}

	text := node.Text(source)
	en.IsConst = strings.HasPrefix(strings.TrimSpace(text), "const enum") ||
		strings.Contains(text, "const enum")

	for i := 0; i < node.ChildCount(); i++ {
		child := node.Child(i)
		childType := child.Type(lang)

		switch childType {
		case "identifier":
			en.Name = child.Text(source)
		case "enum_body":
			for j := 0; j < child.ChildCount(); j++ {
				gc := child.Child(j)
				if gc.Type(lang) == "enum_member" || gc.Type(lang) == "property_identifier" {
					memberName := ""
					for k := 0; k < gc.ChildCount(); k++ {
						gk := gc.Child(k)
						if gk.Type(lang) == "property_identifier" {
							memberName = gk.Text(source)
							break
						}
					}
					if memberName == "" {
						memberName = gc.Text(source)
					}
					// Clean up
					memberName = strings.TrimSuffix(memberName, ",")
					memberName = strings.TrimSpace(memberName)
					if memberName != "" && memberName != "," && memberName != "{" && memberName != "}" {
						en.Members = append(en.Members, memberName)
					}
				}
			}
		}
	}

	return en
}

// detectLanguage maps file extension to language name
func detectLanguage(filePath string) string {
	ext := strings.ToLower(filePath)
	if strings.HasSuffix(ext, ".ts") || strings.HasSuffix(ext, ".tsx") {
		return "typescript"
	}
	return "javascript"
}

package php

import (
	"strings"

	"github.com/VKCOM/php-parser/pkg/ast"
	"github.com/VKCOM/php-parser/pkg/token"
	"github.com/VKCOM/php-parser/pkg/visitor"
	"github.com/VKCOM/php-parser/pkg/visitor/traverser"

	pkgParser "github.com/doITmagic/rag-code-mcp/v2/pkg/parser"
	"github.com/doITmagic/rag-code-mcp/v2/pkg/parser/php/laravel"
)

type symbolCollector struct {
	visitor.Null
	filePath         string
	fileContent      []byte
	currentNamespace string
	currentClass     string
	symbols          []pkgParser.Symbol
	laravelDetector  *laravel.Detector
	imports          map[string]string // alias -> full name
}

func (v *symbolCollector) StmtNamespace(n *ast.StmtNamespace) {
	if n.Name != nil {
		v.currentNamespace = v.extractNodeName(n.Name)
	}
	if n.Stmts != nil {
		for _, s := range n.Stmts {
			traverser.NewTraverser(v).Traverse(s)
		}
	}
}

func (v *symbolCollector) StmtUseList(n *ast.StmtUseList) {
	if v.imports == nil {
		v.imports = make(map[string]string)
	}

	for _, use := range n.Uses {
		if useNode, ok := use.(*ast.StmtUse); ok {
			name := v.extractNodeName(useNode.Use)
			var alias string
			if useNode.Alias != nil {
				if aliasIdent, ok := useNode.Alias.(*ast.Identifier); ok {
					alias = string(aliasIdent.Value)
				}
			} else {
				// If no alias, use the last part of the name
				parts := strings.Split(name, "\\")
				if len(parts) > 0 {
					alias = parts[len(parts)-1]
				}
			}
			if alias != "" {
				v.imports[alias] = name
			}
		}
	}
}

func (v *symbolCollector) StmtClass(n *ast.StmtClass) {
	name := v.extractNodeName(n.Name)
	sym := v.addSymbol(name, pkgParser.Class, n, "class", n.ClassTkn)

	extends := ""
	if n.Extends != nil {
		extends = v.extractNodeName(n.Extends)
		sym.Metadata["extends"] = extends
	}

	// Laravel enrichment
	if v.laravelDetector != nil && v.laravelDetector.IsEloquentModel(n, extends) {
		laravelMeta := v.laravelDetector.ExtractEloquentMetadata(n)
		for k, val := range laravelMeta {
			sym.Metadata[k] = val
		}
		sym.Metadata["framework"] = "laravel"
	}

	if n.Implements != nil {
		var impls []string
		for _, impl := range n.Implements {
			impls = append(impls, v.extractNodeName(impl))
		}
		sym.Metadata["implements"] = impls
	}

	oldClass := v.currentClass
	v.currentClass = name
	if n.Stmts != nil {
		for _, s := range n.Stmts {
			traverser.NewTraverser(v).Traverse(s)
		}
	}
	v.currentClass = oldClass
}

func (v *symbolCollector) StmtTraitUse(n *ast.StmtTraitUse) {
	if v.currentClass == "" {
		return
	}
	// We need to find the parent symbol to add "uses" trait
	for i := len(v.symbols) - 1; i >= 0; i-- {
		if v.symbols[i].Name == v.currentClass && v.symbols[i].Type == pkgParser.Class {
			var uses []string
			if existing, ok := v.symbols[i].Metadata["uses"].([]string); ok {
				uses = existing
			}
			for _, trait := range n.Traits {
				uses = append(uses, v.extractNodeName(trait))
			}
			v.symbols[i].Metadata["uses"] = uses
			break
		}
	}
}

func (v *symbolCollector) StmtPropertyList(n *ast.StmtPropertyList) {
	if v.currentClass == "" {
		return
	}

	visibility := v.extractVisibility(n.Modifiers)
	isStatic := v.hasModifier(n.Modifiers, "static")

	for _, prop := range n.Props {
		if stmtProp, ok := prop.(*ast.StmtProperty); ok {
			propName := v.extractVariableName(stmtProp.Var)
			sym := v.addSymbol(propName, pkgParser.Var, stmtProp, "property", nil)
			sym.Metadata["visibility"] = visibility
			sym.Metadata["static"] = isStatic
			sym.Metadata["class"] = v.currentClass
		}
	}
}

func (v *symbolCollector) StmtClassConstList(n *ast.StmtClassConstList) {
	if v.currentClass == "" {
		return
	}

	visibility := v.extractVisibility(n.Modifiers)

	for _, constVertex := range n.Consts {
		if stmtConst, ok := constVertex.(*ast.StmtConstant); ok {
			constName := v.extractNodeName(stmtConst.Name)
			if constName == "" {
				continue
			}

			sym := v.addSymbol(constName, pkgParser.Const, stmtConst, "constant", nil)
			sym.Metadata["visibility"] = visibility
			sym.Metadata["value"] = v.extractConstValue(stmtConst.Expr)
			sym.Metadata["class"] = v.currentClass
		}
	}
}

func (v *symbolCollector) StmtInterface(n *ast.StmtInterface) {
	name := v.extractNodeName(n.Name)
	sym := v.addSymbol(name, pkgParser.Interface, n, "interface", n.InterfaceTkn)

	if n.Extends != nil {
		var exts []string
		for _, ext := range n.Extends {
			exts = append(exts, v.extractNodeName(ext))
		}
		sym.Metadata["extends"] = exts
	}
}

func (v *symbolCollector) StmtTrait(n *ast.StmtTrait) {
	name := v.extractNodeName(n.Name)
	v.addSymbol(name, pkgParser.Class, n, "trait", n.TraitTkn)
}

func (v *symbolCollector) StmtConstList(n *ast.StmtConstList) {
	for _, constVertex := range n.Consts {
		if stmtConst, ok := constVertex.(*ast.StmtConstant); ok {
			constName := v.extractNodeName(stmtConst.Name)
			if constName == "" {
				continue
			}

			sym := v.addSymbol(constName, pkgParser.Const, stmtConst, "constant", nil)
			sym.Metadata["value"] = v.extractConstValue(stmtConst.Expr)
		}
	}
}

func (v *symbolCollector) StmtClassMethod(n *ast.StmtClassMethod) {
	if nameIdent, ok := n.Name.(*ast.Identifier); ok {
		methodName := string(nameIdent.Value)
		// PHPDoc for methods is often on the first modifier
		var tok *token.Token
		for _, mod := range n.Modifiers {
			if ident, ok := mod.(*ast.Identifier); ok {
				if ident.IdentifierTkn != nil && ident.IdentifierTkn.FreeFloating != nil {
					tok = ident.IdentifierTkn
					break
				}
			}
		}
		if tok == nil {
			tok = n.FunctionTkn
		}
		sym := v.addSymbol(methodName, pkgParser.Method, n, "method", tok)
		sym.Metadata["visibility"] = v.extractVisibility(n.Modifiers)
		sym.Metadata["static"] = v.hasModifier(n.Modifiers, "static")
		sym.Metadata["abstract"] = v.hasModifier(n.Modifiers, "abstract")
		sym.Metadata["final"] = v.hasModifier(n.Modifiers, "final")
		sym.Metadata["parameters"] = v.extractParameters(n.Params)
		sym.Metadata["return_type"] = v.extractTypeName(n.ReturnType)
		sym.Signature = v.buildMethodSignature(methodName, n.Params, n.ReturnType, sym.Metadata["visibility"].(string))
	}
}

func (v *symbolCollector) StmtFunction(n *ast.StmtFunction) {
	if nameIdent, ok := n.Name.(*ast.Identifier); ok {
		funcName := string(nameIdent.Value)
		sym := v.addSymbol(funcName, pkgParser.Function, n, "function", n.FunctionTkn)
		sym.Metadata["parameters"] = v.extractParameters(n.Params)
		sym.Metadata["return_type"] = v.extractTypeName(n.ReturnType)
		sym.Signature = v.buildMethodSignature(funcName, n.Params, n.ReturnType, "")
	}
}

func (v *symbolCollector) ExprStaticCall(n *ast.ExprStaticCall) {
	className := v.extractNodeName(n.Class)
	if className == "Route" || strings.HasSuffix(className, "\\Route") {
		if v.laravelDetector != nil {
			info := v.laravelDetector.ExtractRouteInfo(n)
			if info != nil {
				sym := v.addSymbol(info.Uri, pkgParser.Var, n, "route", nil)
				sym.Metadata["method"] = info.Method
				sym.Metadata["controller"] = info.Controller
				sym.Metadata["action"] = info.Action
				sym.Metadata["framework"] = "laravel"
				// Add a useful docstring for search
				sym.Docstring = "Laravel Route [" + info.Method + "] " + info.Uri + " -> " + info.Action
			}
		} else {
			// Fallback for non-laravel detection
			methodName := v.extractNodeName(n.Call)
			switch methodName {
			case "get", "post", "put", "patch", "delete", "any":
				if len(n.Args) > 0 {
					if argRaw, ok := n.Args[0].(*ast.Argument); ok {
						if routePathNode, ok := argRaw.Expr.(*ast.ScalarString); ok {
							routePath := strings.Trim(string(routePathNode.Value), "'\"")
							v.addSymbol(routePath, pkgParser.Var, n, "route", nil)
						}
					}
				}
			}
		}
	}
}

func (v *symbolCollector) addSymbol(name string, symbolType pkgParser.SymbolType, n ast.Vertex, phpKind string, tok *token.Token) *pkgParser.Symbol {
	pos := n.GetPosition()
	if pos == nil {
		return &pkgParser.Symbol{}
	}

	sym := pkgParser.Symbol{
		Name:      name,
		Type:      symbolType,
		StartLine: pos.StartLine,
		EndLine:   pos.EndLine,
		Package:   v.currentNamespace,
		FilePath:  v.filePath,
		Language:  "php",
		Metadata:  make(map[string]any),
	}

	// Extract PHPDoc if token provided
	if tok != nil {
		doc := extractPHPDocFromToken(tok)
		sym.Docstring = doc.Description
		if doc.Deprecated != "" {
			sym.Metadata["deprecated"] = doc.Deprecated
		}
		if len(doc.Params) > 0 {
			sym.Metadata["phpdoc_params"] = doc.Params
		}
		if len(doc.Returns) > 0 {
			sym.Metadata["phpdoc_returns"] = doc.Returns
		}
		if len(doc.Throws) > 0 {
			sym.Metadata["phpdoc_throws"] = doc.Throws
		}
	}

	sym.Metadata["php_kind"] = phpKind
	if v.currentClass != "" && (phpKind == "method") {
		sym.Metadata["class"] = v.currentClass
	}

	// Add imports
	if len(v.imports) > 0 {
		importsCopy := make(map[string]string)
		for k, val := range v.imports {
			importsCopy[k] = val
		}
		sym.Metadata["imports"] = importsCopy
	}

	v.symbols = append(v.symbols, sym)
	return &v.symbols[len(v.symbols)-1]
}

func (v *symbolCollector) extractNodeName(n ast.Vertex) string {
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

func (v *symbolCollector) extractConstValue(node ast.Vertex) string {
	if node == nil {
		return ""
	}

	switch n := node.(type) {
	case *ast.ScalarString:
		return strings.Trim(string(n.Value), "'\"")
	case *ast.ScalarLnumber:
		return string(n.Value)
	case *ast.ScalarDnumber:
		return string(n.Value)
	case *ast.ExprConstFetch:
		return v.extractNodeName(n.Const)
	}

	return "" // For complex expressions, return empty
}

func (v *symbolCollector) extractVisibility(modifiers []ast.Vertex) string {
	for _, mod := range modifiers {
		if ident, ok := mod.(*ast.Identifier); ok {
			modStr := string(ident.Value)
			if modStr == "public" || modStr == "protected" || modStr == "private" {
				return modStr
			}
		}
	}
	return "public" // Default visibility in PHP
}

func (v *symbolCollector) hasModifier(modifiers []ast.Vertex, target string) bool {
	for _, mod := range modifiers {
		if ident, ok := mod.(*ast.Identifier); ok {
			if string(ident.Value) == target {
				return true
			}
		}
	}
	return false
}

func (v *symbolCollector) extractVariableName(node ast.Vertex) string {
	if node == nil {
		return ""
	}

	if exprVar, ok := node.(*ast.ExprVariable); ok {
		return v.extractNodeName(exprVar.Name)
	}
	return ""
}

func (v *symbolCollector) extractTypeName(node ast.Vertex) string {
	if node == nil {
		return ""
	}

	switch n := node.(type) {
	case *ast.Name:
		return v.extractNodeName(n)
	case *ast.NameFullyQualified:
		return "\\" + v.extractNodeName(n)
	case *ast.Identifier:
		return string(n.Value)
	case *ast.Nullable:
		return "?" + v.extractTypeName(n.Expr)
	}
	return ""
}

func (v *symbolCollector) extractParameters(params []ast.Vertex) []map[string]any {
	var result []map[string]any

	for _, param := range params {
		if p, ok := param.(*ast.Parameter); ok {
			paramInfo := map[string]any{
				"name": v.extractVariableName(p.Var),
				"type": v.extractTypeName(p.Type),
			}
			result = append(result, paramInfo)
		}
	}

	return result
}

func (v *symbolCollector) buildMethodSignature(name string, params []ast.Vertex, returnType ast.Vertex, visibility string) string {
	sig := visibility + " function " + name + "("

	// Add parameters
	paramStrs := make([]string, 0, len(params))
	for _, param := range params {
		if p, ok := param.(*ast.Parameter); ok {
			paramStr := ""

			// Add type
			if p.Type != nil {
				paramStr += v.extractTypeName(p.Type) + " "
			}

			// Add name
			paramStr += "$" + v.extractVariableName(p.Var)

			paramStrs = append(paramStrs, paramStr)
		}
	}
	sig += strings.Join(paramStrs, ", ")
	sig += ")"

	// Add return type
	if returnType != nil {
		sig += ": " + v.extractTypeName(returnType)
	}

	return sig
}

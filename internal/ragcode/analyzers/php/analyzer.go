package php

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/VKCOM/php-parser/pkg/ast"
	"github.com/VKCOM/php-parser/pkg/conf"
	"github.com/VKCOM/php-parser/pkg/errors"
	"github.com/VKCOM/php-parser/pkg/parser"
	"github.com/VKCOM/php-parser/pkg/version"
	"github.com/VKCOM/php-parser/pkg/visitor"
	"github.com/VKCOM/php-parser/pkg/visitor/traverser"

	"github.com/doITmagic/rag-code-mcp/internal/codetypes"
)

// CodeAnalyzer implements PathAnalyzer for PHP
type CodeAnalyzer struct {
	currentNamespace string
	packages         map[string]*PackageInfo
}

// NewCodeAnalyzer creates a new PHP code analyzer
func NewCodeAnalyzer() *CodeAnalyzer {
	return &CodeAnalyzer{
		packages: make(map[string]*PackageInfo),
	}
}

// GetPackages returns the internal package information
// Useful for framework-specific analyzers (e.g., Laravel)
func (ca *CodeAnalyzer) GetPackages() []*PackageInfo {
	var result []*PackageInfo
	for _, pkg := range ca.packages {
		result = append(result, pkg)
	}
	return result
}

// AnalyzePaths implements the PathAnalyzer interface
func (ca *CodeAnalyzer) AnalyzePaths(paths []string) ([]codetypes.CodeChunk, error) {
	// Reset state for global analysis
	ca.packages = make(map[string]*PackageInfo)

	for _, root := range paths {
		// Check if it's a file or directory
		info, err := os.Stat(root)
		if err != nil {
			return nil, fmt.Errorf("error accessing path %s: %w", root, err)
		}

		if info.IsDir() {
			// Walk directory and analyze all PHP files
			err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
				if err != nil {
					return err
				}

				if d.IsDir() {
					base := filepath.Base(path)
					// Skip common directories that shouldn't be indexed
					if base == ".git" || base == "vendor" || base == "node_modules" ||
						base == "storage" || base == "public" || strings.HasPrefix(base, ".") {
						if path != root { // allow root even if hidden
							return filepath.SkipDir
						}
					}
					return nil
				}

				// Only analyze PHP files
				if !strings.HasSuffix(d.Name(), ".php") {
					return nil
				}

				content, err := os.ReadFile(path)
				if err != nil {
					fmt.Fprintf(os.Stderr, "Warning: failed to read %s: %v\n", path, err)
					return nil
				}

				if err := ca.parseAndCollect(path, content); err != nil {
					fmt.Fprintf(os.Stderr, "Warning: failed to analyze %s: %v\n", path, err)
				}
				return nil
			})

			if err != nil {
				return nil, fmt.Errorf("error walking directory %s: %w", root, err)
			}
		} else {
			// It's a single file
			content, err := os.ReadFile(root)
			if err != nil {
				return nil, fmt.Errorf("failed to read file: %w", err)
			}
			if err := ca.parseAndCollect(root, content); err != nil {
				return nil, fmt.Errorf("error analyzing %s: %w", root, err)
			}
		}
	}

	return ca.convertToChunks(), nil
}

// AnalyzeFile analyzes a single PHP file
func (ca *CodeAnalyzer) AnalyzeFile(filePath string) ([]codetypes.CodeChunk, error) {
	// Reset state for this file
	ca.packages = make(map[string]*PackageInfo)

	content, err := os.ReadFile(filePath)
	if err != nil {
		return nil, fmt.Errorf("failed to read file: %w", err)
	}

	if err := ca.parseAndCollect(filePath, content); err != nil {
		return nil, err
	}

	return ca.convertToChunks(), nil
}

// parseAndCollect parses PHP source and collects symbols into ca.packages
func (ca *CodeAnalyzer) parseAndCollect(filePath string, content []byte) error {
	// Parse PHP source
	rootNode, parserErrors, err := ca.parsePHPSource(content)
	if err != nil {
		return fmt.Errorf("failed to parse PHP: %w", err)
	}

	// Log parser errors but continue
	if len(parserErrors) > 0 {
		// Only log first few errors to avoid spam
		maxErrors := 3
		fmt.Fprintf(os.Stderr, "PHP parser warnings in %s:\n", filePath)
		for i, e := range parserErrors {
			if i >= maxErrors {
				fmt.Fprintf(os.Stderr, "  ... and %d more\n", len(parserErrors)-maxErrors)
				break
			}
			fmt.Fprintf(os.Stderr, "  %s\n", e.String())
		}
	}

	// Reset namespace for this file
	ca.currentNamespace = ""

	// Collect symbols using visitor pattern
	collector := &symbolCollector{
		analyzer:    ca,
		filePath:    filePath,
		fileContent: content,
	}

	traverser.NewTraverser(collector).Traverse(rootNode)
	return nil
}

// parsePHPSource parses PHP source code and returns the AST
func (ca *CodeAnalyzer) parsePHPSource(content []byte) (ast.Vertex, []*errors.Error, error) {
	var parserErrors []*errors.Error

	rootNode, err := parser.Parse(content, conf.Config{
		Version: &version.Version{Major: 8, Minor: 0},
		ErrorHandlerFunc: func(e *errors.Error) {
			parserErrors = append(parserErrors, e)
		},
	})

	if err != nil {
		return nil, parserErrors, err
	}

	return rootNode, parserErrors, nil
}

// symbolCollector is a visitor that collects PHP symbols
type symbolCollector struct {
	visitor.Null // Embedded - provides default implementations for all visitor methods
	analyzer     *CodeAnalyzer
	filePath     string
	fileContent  []byte            // Source code content for extracting code snippets
	currentClass *ClassInfo        // Track current class being processed
	imports      map[string]string // Track imports for the current file
}

// StmtNamespace handles namespace declarations
func (v *symbolCollector) StmtNamespace(n *ast.StmtNamespace) {
	v.analyzer.currentNamespace = v.extractNamespaceName(n.Name)
	// Reset imports when namespace changes (though usually one namespace per file)
	v.imports = make(map[string]string)
}

// StmtUse handles use statements (imports)
func (v *symbolCollector) StmtUse(n *ast.StmtUseList) {
	if v.imports == nil {
		v.imports = make(map[string]string)
	}

	for _, use := range n.Uses {
		if useNode, ok := use.(*ast.StmtUse); ok {
			name := v.extractName(useNode.Use)
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

// StmtClass handles class declarations
func (v *symbolCollector) StmtClass(n *ast.StmtClass) {
	className := v.extractIdentifier(n.Name)
	if className == "" {
		return
	}

	// Get or create package info
	pkgName := v.analyzer.currentNamespace
	if pkgName == "" {
		pkgName = "global"
	}

	pkg := v.analyzer.getOrCreatePackage(pkgName)

	// Create class info
	classInfo := &ClassInfo{
		Name:       className,
		Namespace:  pkgName,
		FullName:   v.buildFullName(className),
		Methods:    []MethodInfo{},
		Properties: []PropertyInfo{},
		Constants:  []ConstantInfo{},
		FilePath:   v.filePath,
		StartLine:  n.Position.StartLine,
		EndLine:    n.Position.EndLine,
		Imports:    v.copyImports(),
	}

	// Extract code from file content (LIMIT to first 50 lines for better embedding matching)
	if v.fileContent != nil && n.Position != nil {
		endLine := n.Position.EndLine
		// For large classes, only extract header/summary (first ~50 lines)
		maxLines := 50
		if endLine-n.Position.StartLine > maxLines {
			endLine = n.Position.StartLine + maxLines
		}
		classInfo.Code = extractCodeFromContent(v.fileContent, n.Position.StartLine, endLine)
	}

	// Extract PHPDoc comment from ClassTkn
	if n.ClassTkn != nil {
		phpDoc := extractPHPDocFromToken(n.ClassTkn)
		classInfo.Description = phpDoc.Description
	}

	// Extract extends
	if n.Extends != nil {
		classInfo.Extends = v.extractName(n.Extends)
	}

	// Extract implements
	if n.Implements != nil {
		for _, iface := range n.Implements {
			classInfo.Implements = append(classInfo.Implements, v.extractName(iface))
		}
	}

	// Store current class for method/property extraction
	v.currentClass = classInfo

	// Traverse child nodes to collect methods, properties, etc.
	if n.Stmts != nil {
		for _, stmt := range n.Stmts {
			traverser.NewTraverser(v).Traverse(stmt)
		}
	}

	// After collecting all methods/properties, add class to package
	pkg.Classes = append(pkg.Classes, *classInfo)

	// Store AST node for advanced analysis (e.g., Laravel)
	if pkg.ClassNodes == nil {
		pkg.ClassNodes = make(map[string]*ast.StmtClass)
	}
	pkg.ClassNodes[classInfo.FullName] = n

	// Reset current class
	v.currentClass = nil
}

// StmtClassMethod handles method declarations
func (v *symbolCollector) StmtClassMethod(n *ast.StmtClassMethod) {
	if v.currentClass == nil {
		return
	}

	methodName := v.extractIdentifier(n.Name)
	if methodName == "" {
		return
	}

	methodInfo := MethodInfo{
		Name:       methodName,
		Visibility: v.extractVisibility(n.Modifiers),
		IsStatic:   v.hasModifier(n.Modifiers, "static"),
		IsAbstract: v.hasModifier(n.Modifiers, "abstract"),
		IsFinal:    v.hasModifier(n.Modifiers, "final"),
		Parameters: v.extractParameters(n.Params),
		ReturnType: v.extractTypeNameString(n.ReturnType),
		ClassName:  v.currentClass.Name,
		FilePath:   v.filePath,
		StartLine:  n.Position.StartLine,
		EndLine:    n.Position.EndLine,
	}

	// Extract code from file content
	if v.fileContent != nil && n.Position != nil {
		methodInfo.Code = extractCodeFromContent(v.fileContent, n.Position.StartLine, n.Position.EndLine)
	}

	// Extract PHPDoc from modifiers
	phpDoc := v.extractPHPDocFromModifiers(n.Modifiers)
	methodInfo.Description = phpDoc.Description
	methodInfo.Signature = v.buildMethodSignature(methodName, n.Params, n.ReturnType, v.extractVisibility(n.Modifiers))

	// Merge PHPDoc returns with type hint
	if len(phpDoc.Returns) > 0 {
		methodInfo.Returns = convertPHPDocToReturnInfo(phpDoc.Returns)
	} else if methodInfo.ReturnType != "" {
		// Use return type hint as return info
		methodInfo.Returns = []codetypes.ReturnInfo{
			{Type: methodInfo.ReturnType, Description: ""},
		}
	}

	v.currentClass.Methods = append(v.currentClass.Methods, methodInfo)
}

// StmtTraitUse handles trait usage within a class
func (v *symbolCollector) StmtTraitUse(n *ast.StmtTraitUse) {
	if v.currentClass == nil {
		return
	}

	// Extract trait names
	for _, trait := range n.Traits {
		traitName := v.extractName(trait)
		if traitName != "" {
			v.currentClass.Uses = append(v.currentClass.Uses, traitName)
		}
	}
}

// StmtFunction handles global function declarations
func (v *symbolCollector) StmtFunction(n *ast.StmtFunction) {
	funcName := v.extractIdentifier(n.Name)
	if funcName == "" {
		return
	}

	pkgName := v.analyzer.currentNamespace
	if pkgName == "" {
		pkgName = "global"
	}

	pkg := v.analyzer.getOrCreatePackage(pkgName)

	funcInfo := FunctionInfo{
		Name:       funcName,
		Namespace:  pkgName,
		Parameters: v.extractParameters(n.Params),
		ReturnType: v.extractTypeNameString(n.ReturnType),
		FilePath:   v.filePath,
	}

	// Extract PHPDoc from FunctionTkn
	if n.FunctionTkn != nil {
		phpDoc := extractPHPDocFromToken(n.FunctionTkn)
		funcInfo.Description = phpDoc.Description
		funcInfo.Signature = v.buildMethodSignature(funcName, n.Params, n.ReturnType, "")

		if len(phpDoc.Returns) > 0 {
			funcInfo.Returns = convertPHPDocToReturnInfo(phpDoc.Returns)
		} else if funcInfo.ReturnType != "" {
			funcInfo.Returns = []codetypes.ReturnInfo{
				{Type: funcInfo.ReturnType, Description: ""},
			}
		}
	}

	pkg.Functions = append(pkg.Functions, funcInfo)
}

// StmtPropertyList handles class property declarations
func (v *symbolCollector) StmtPropertyList(n *ast.StmtPropertyList) {
	if v.currentClass == nil {
		return
	}

	// Extract visibility and modifiers from the property list
	visibility := v.extractVisibility(n.Modifiers)
	isStatic := v.hasModifier(n.Modifiers, "static")
	isReadonly := v.hasModifier(n.Modifiers, "readonly")

	// Extract type if present
	typeName := ""
	if n.Type != nil {
		typeName = v.extractTypeNameString(n.Type)
	}

	// Extract PHPDoc from modifiers
	phpDoc := v.extractPHPDocFromModifiers(n.Modifiers)

	// Use @var type if no type hint
	if typeName == "" && phpDoc.VarType != "" {
		typeName = phpDoc.VarType
	}

	// Process each property in the list
	for _, prop := range n.Props {
		if stmtProp, ok := prop.(*ast.StmtProperty); ok {
			propInfo := PropertyInfo{
				Name:        v.extractVariableName(stmtProp.Var),
				Type:        typeName,
				Visibility:  visibility,
				IsStatic:    isStatic,
				IsReadonly:  isReadonly,
				Description: phpDoc.Description,
				FilePath:    v.filePath,
				StartLine:   stmtProp.Position.StartLine,
				EndLine:     stmtProp.Position.EndLine,
			}
			v.currentClass.Properties = append(v.currentClass.Properties, propInfo)
		}
	}
}

// StmtClassConstList handles class constant declarations
func (v *symbolCollector) StmtClassConstList(n *ast.StmtClassConstList) {
	if v.currentClass == nil {
		return
	}

	// Extract visibility from modifiers
	visibility := v.extractVisibility(n.Modifiers)

	// Process each constant in the list
	for _, constVertex := range n.Consts {
		if stmtConst, ok := constVertex.(*ast.StmtConstant); ok {
			constName := v.extractIdentifier(stmtConst.Name)
			if constName == "" {
				continue
			}

			constInfo := ConstantInfo{
				Name:       constName,
				Type:       "", // PHP constants don't have explicit types
				Value:      v.extractConstValue(stmtConst.Expr),
				Visibility: visibility,
			}

			v.currentClass.Constants = append(v.currentClass.Constants, constInfo)
		}
	}
}

// StmtInterface handles interface declarations
func (v *symbolCollector) StmtInterface(n *ast.StmtInterface) {
	interfaceName := v.extractIdentifier(n.Name)
	if interfaceName == "" {
		return
	}

	pkgName := v.analyzer.currentNamespace
	if pkgName == "" {
		pkgName = "global"
	}

	pkg := v.analyzer.getOrCreatePackage(pkgName)

	interfaceInfo := InterfaceInfo{
		Name:      interfaceName,
		Namespace: pkgName,
		Methods:   []MethodInfo{},
		Extends:   []string{},
		FilePath:  v.filePath,
	}

	// Extract PHPDoc from InterfaceTkn
	if n.InterfaceTkn != nil {
		phpDoc := extractPHPDocFromToken(n.InterfaceTkn)
		interfaceInfo.Description = phpDoc.Description
	}

	// Extract extends (interfaces can extend multiple interfaces)
	if n.Extends != nil {
		for _, ext := range n.Extends {
			interfaceInfo.Extends = append(interfaceInfo.Extends, v.extractName(ext))
		}
	}

	// Store current interface (reuse currentClass mechanism)
	v.currentClass = &ClassInfo{
		Name:      interfaceName,
		Namespace: pkgName,
		FullName:  v.buildFullName(interfaceName),
		Methods:   []MethodInfo{},
	}

	// Traverse child nodes to collect methods
	if n.Stmts != nil {
		for _, stmt := range n.Stmts {
			traverser.NewTraverser(v).Traverse(stmt)
		}
	}

	// Transfer collected methods to interface
	interfaceInfo.Methods = v.currentClass.Methods

	// Add interface to package
	pkg.Interfaces = append(pkg.Interfaces, interfaceInfo)

	// Reset state
	v.currentClass = nil
}

// StmtTrait handles trait declarations
func (v *symbolCollector) StmtTrait(n *ast.StmtTrait) {
	traitName := v.extractIdentifier(n.Name)
	if traitName == "" {
		return
	}

	pkgName := v.analyzer.currentNamespace
	if pkgName == "" {
		pkgName = "global"
	}

	pkg := v.analyzer.getOrCreatePackage(pkgName)

	traitInfo := TraitInfo{
		Name:       traitName,
		Namespace:  pkgName,
		Methods:    []MethodInfo{},
		Properties: []PropertyInfo{},
		FilePath:   v.filePath,
	}

	// Extract PHPDoc from TraitTkn
	if n.TraitTkn != nil {
		phpDoc := extractPHPDocFromToken(n.TraitTkn)
		traitInfo.Description = phpDoc.Description
	}

	// Store current trait (reuse currentClass mechanism)
	v.currentClass = &ClassInfo{
		Name:       traitName,
		Namespace:  pkgName,
		FullName:   v.buildFullName(traitName),
		Methods:    []MethodInfo{},
		Properties: []PropertyInfo{},
	}

	// Traverse child nodes to collect methods and properties
	if n.Stmts != nil {
		for _, stmt := range n.Stmts {
			traverser.NewTraverser(v).Traverse(stmt)
		}
	}

	// Transfer collected methods and properties to trait
	traitInfo.Methods = v.currentClass.Methods
	traitInfo.Properties = v.currentClass.Properties

	// Add trait to package
	pkg.Traits = append(pkg.Traits, traitInfo)

	// Reset state
	v.currentClass = nil
}

// copyImports creates a deep copy of the current imports map
func (v *symbolCollector) copyImports() map[string]string {
	if v.imports == nil {
		return nil
	}
	dst := make(map[string]string, len(v.imports))
	for key, value := range v.imports {
		dst[key] = value
	}
	return dst
}

// Helper methods for symbol extraction

// extractPHPDocFromModifiers extracts PHPDoc from method/property modifiers
func (v *symbolCollector) extractPHPDocFromModifiers(modifiers []ast.Vertex) *PHPDocInfo {
	// Check all modifiers - first modifier usually has PHPDoc in FreeFloating
	for _, mod := range modifiers {
		if identifier, ok := mod.(*ast.Identifier); ok {
			if identifier.IdentifierTkn != nil {
				phpDoc := extractPHPDocFromToken(identifier.IdentifierTkn)
				if phpDoc.Description != "" || len(phpDoc.Params) > 0 || len(phpDoc.Returns) > 0 {
					return phpDoc
				}
			}
		}
	}

	return &PHPDocInfo{
		Params:   []ParamDoc{},
		Returns:  []ReturnDoc{},
		Throws:   []string{},
		See:      []string{},
		Examples: []string{},
	}
}

// buildMethodSignature creates a method signature string
func (v *symbolCollector) buildMethodSignature(name string, params []ast.Vertex, returnType ast.Vertex, visibility string) string {
	sig := visibility + " function " + name + "("

	// Add parameters
	paramStrs := make([]string, 0, len(params))
	for _, param := range params {
		if p, ok := param.(*ast.Parameter); ok {
			paramStr := ""

			// Add type
			if p.Type != nil {
				paramStr += v.extractTypeNameString(p.Type) + " "
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
		sig += ": " + v.extractTypeNameString(returnType)
	}

	return sig
}

func (v *symbolCollector) extractNamespaceName(node ast.Vertex) string {
	if node == nil {
		return ""
	}

	switch n := node.(type) {
	case *ast.Name:
		parts := make([]string, 0, len(n.Parts))
		for _, part := range n.Parts {
			if namePart, ok := part.(*ast.NamePart); ok {
				parts = append(parts, string(namePart.Value))
			}
		}
		return strings.Join(parts, "\\")
	}
	return ""
}

func (v *symbolCollector) extractIdentifier(node ast.Vertex) string {
	if node == nil {
		return ""
	}

	if ident, ok := node.(*ast.Identifier); ok {
		return string(ident.Value)
	}
	return ""
}

func (v *symbolCollector) extractName(node ast.Vertex) string {
	if node == nil {
		return ""
	}

	switch n := node.(type) {
	case *ast.Name:
		parts := make([]string, 0, len(n.Parts))
		for _, part := range n.Parts {
			if namePart, ok := part.(*ast.NamePart); ok {
				parts = append(parts, string(namePart.Value))
			}
		}
		return strings.Join(parts, "\\")
	case *ast.NameFullyQualified:
		parts := make([]string, 0, len(n.Parts))
		for _, part := range n.Parts {
			if namePart, ok := part.(*ast.NamePart); ok {
				parts = append(parts, string(namePart.Value))
			}
		}
		return "\\" + strings.Join(parts, "\\")
	}
	return ""
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

func (v *symbolCollector) extractParameters(params []ast.Vertex) []codetypes.ParamInfo {
	var result []codetypes.ParamInfo

	for _, param := range params {
		if p, ok := param.(*ast.Parameter); ok {
			paramInfo := codetypes.ParamInfo{
				Name: v.extractVariableName(p.Var),
				Type: v.extractTypeName(p.Type),
			}
			result = append(result, paramInfo)
		}
	}

	return result
}

func (v *symbolCollector) extractVariableName(node ast.Vertex) string {
	if node == nil {
		return ""
	}

	if exprVar, ok := node.(*ast.ExprVariable); ok {
		return v.extractIdentifier(exprVar.Name)
	}
	return ""
}

// extractConstValue attempts to extract a simple string representation of constant values
func (v *symbolCollector) extractConstValue(node ast.Vertex) string {
	if node == nil {
		return ""
	}

	switch n := node.(type) {
	case *ast.ScalarString:
		if n.Value != nil {
			return string(n.Value)
		}
	case *ast.ScalarLnumber:
		if n.Value != nil {
			return string(n.Value)
		}
	case *ast.ScalarDnumber:
		if n.Value != nil {
			return string(n.Value)
		}
	case *ast.ExprConstFetch:
		return v.extractName(n.Const)
	}

	return "" // For complex expressions, return empty
}

func (v *symbolCollector) extractTypeName(node ast.Vertex) string {
	if node == nil {
		return ""
	}

	switch n := node.(type) {
	case *ast.Name:
		return v.extractName(n)
	case *ast.NameFullyQualified:
		return v.extractName(n)
	case *ast.Identifier:
		return string(n.Value)
	case *ast.Nullable:
		return "?" + v.extractTypeName(n.Expr)
	}
	return ""
}

func (v *symbolCollector) extractTypeNameString(node ast.Vertex) string {
	if node == nil {
		return ""
	}
	return v.extractTypeName(node)
}

func (v *symbolCollector) buildFullName(name string) string {
	if v.analyzer.currentNamespace == "" {
		return name
	}
	return v.analyzer.currentNamespace + "\\" + name
}

// getOrCreatePackage gets or creates a package info
func (ca *CodeAnalyzer) getOrCreatePackage(pkgName string) *PackageInfo {
	if pkg, exists := ca.packages[pkgName]; exists {
		return pkg
	}

	pkg := &PackageInfo{
		Namespace:  pkgName,
		Classes:    []ClassInfo{},
		Interfaces: []InterfaceInfo{},
		Traits:     []TraitInfo{},
		Functions:  []FunctionInfo{},
		Constants:  []ConstantInfo{},
	}
	ca.packages[pkgName] = pkg
	return pkg
}

// convertToChunks converts collected PHP symbols to CodeChunks
func (ca *CodeAnalyzer) convertToChunks() []codetypes.CodeChunk {
	var chunks []codetypes.CodeChunk

	// Check if this is a Laravel project for enhanced metadata
	isLaravel := ca.IsLaravelProject()

	for _, pkg := range ca.packages {
		// Convert classes
		for _, class := range pkg.Classes {
			chunk := codetypes.CodeChunk{
				Name:      class.Name,
				Type:      "class",
				Language:  "php",
				Package:   class.Namespace,
				FilePath:  class.FilePath,
				StartLine: class.StartLine,
				EndLine:   class.EndLine,
				Docstring: class.Description,
				Code:      class.Code,
			}

			// Add a simple class signature similar to Go type summaries
			chunk.Signature = buildClassSignature(class)

			// Add Laravel metadata if applicable
			if isLaravel {
				ca.addLaravelMetadata(&chunk, &class, pkg)
			}

			chunks = append(chunks, chunk)

			// Add chunks for each method
			for _, method := range class.Methods {
				methodChunk := codetypes.CodeChunk{
					Name:      method.Name,
					Type:      "method",
					Language:  "php",
					Package:   class.Namespace,
					Signature: fmt.Sprintf("%s function %s()", method.Visibility, method.Name),
					FilePath:  class.FilePath,
					StartLine: method.StartLine,
					EndLine:   method.EndLine,
					Docstring: method.Description,
					Code:      method.Code,
				}
				chunks = append(chunks, methodChunk)
			}

			// Add chunks for each property
			for _, prop := range class.Properties {
				propChunk := codetypes.CodeChunk{
					Name:      prop.Name,
					Type:      "property",
					Language:  "php",
					Package:   class.Namespace,
					Signature: fmt.Sprintf("%s %s $%s", prop.Visibility, prop.Type, prop.Name),
					FilePath:  class.FilePath,
					StartLine: prop.StartLine,
					EndLine:   prop.EndLine,
					Docstring: prop.Description,
				}
				chunks = append(chunks, propChunk)
			}

			// Add chunks for each constant
			for _, constant := range class.Constants {
				constChunk := codetypes.CodeChunk{
					Name:      constant.Name,
					Type:      "constant",
					Language:  "php",
					Package:   class.Namespace,
					Signature: fmt.Sprintf("%s const %s", constant.Visibility, constant.Name),
				}
				chunks = append(chunks, constChunk)
			}
		}

		// Convert interfaces
		for _, iface := range pkg.Interfaces {
			chunk := codetypes.CodeChunk{
				Name:     iface.Name,
				Type:     "interface",
				Language: "php",
				Package:  iface.Namespace,
			}
			chunks = append(chunks, chunk)

			// Add chunks for interface methods
			for _, method := range iface.Methods {
				methodChunk := codetypes.CodeChunk{
					Name:      method.Name,
					Type:      "method",
					Language:  "php",
					Package:   iface.Namespace,
					Signature: fmt.Sprintf("function %s()", method.Name),
				}
				chunks = append(chunks, methodChunk)
			}
		}

		// Convert traits
		for _, trait := range pkg.Traits {
			chunk := codetypes.CodeChunk{
				Name:     trait.Name,
				Type:     "trait",
				Language: "php",
				Package:  trait.Namespace,
			}
			chunks = append(chunks, chunk)

			// Add chunks for trait methods
			for _, method := range trait.Methods {
				methodChunk := codetypes.CodeChunk{
					Name:      method.Name,
					Type:      "method",
					Language:  "php",
					Package:   trait.Namespace,
					Signature: fmt.Sprintf("%s function %s()", method.Visibility, method.Name),
				}
				chunks = append(chunks, methodChunk)
			}

			// Add chunks for trait properties
			for _, prop := range trait.Properties {
				propChunk := codetypes.CodeChunk{
					Name:      prop.Name,
					Type:      "property",
					Language:  "php",
					Package:   trait.Namespace,
					Signature: fmt.Sprintf("%s %s $%s", prop.Visibility, prop.Type, prop.Name),
				}
				chunks = append(chunks, propChunk)
			}
		}

		// Convert global functions
		for _, fn := range pkg.Functions {
			chunk := codetypes.CodeChunk{
				Name:     fn.Name,
				Type:     "function",
				Language: "php",
				Package:  fn.Namespace,
			}
			chunks = append(chunks, chunk)
		}

		// Convert global constants
		for _, constant := range pkg.Constants {
			constChunk := codetypes.CodeChunk{
				Name:      constant.Name,
				Type:      "constant",
				Language:  "php",
				Package:   pkg.Namespace,
				Signature: fmt.Sprintf("const %s", constant.Name),
			}
			chunks = append(chunks, constChunk)
		}
	}

	return chunks
}

// extractCodeFromContent extracts code from file content based on line numbers (1-indexed)
func extractCodeFromContent(content []byte, startLine, endLine int) string {
	if content == nil || startLine < 1 || endLine < startLine {
		return ""
	}

	lines := strings.Split(string(content), "\n")
	if startLine > len(lines) {
		return ""
	}

	// Adjust endLine if it exceeds file length
	if endLine > len(lines) {
		endLine = len(lines)
	}

	// Extract lines (convert from 1-indexed to 0-indexed)
	return strings.Join(lines[startLine-1:endLine], "\n")
}

// IsLaravelProject detects if the analyzed code is from a Laravel project
func (ca *CodeAnalyzer) IsLaravelProject() bool {
	for _, pkg := range ca.packages {
		// Check for Laravel-specific namespaces
		if strings.HasPrefix(pkg.Namespace, "App\\Models") ||
			strings.HasPrefix(pkg.Namespace, "App\\Http\\Controllers") ||
			strings.HasPrefix(pkg.Namespace, "Illuminate\\") {
			return true
		}

		// Check for Laravel base classes
		for _, class := range pkg.Classes {
			if class.Extends == "Model" ||
				class.Extends == "Controller" ||
				class.Extends == "Authenticatable" ||
				strings.Contains(class.Extends, "Illuminate\\") {
				return true
			}
		}
	}
	return false
}

// addLaravelMetadata enriches chunks with Laravel-specific metadata
// NOTE: Full Laravel integration will be done via laravel.Analyzer
// This method is reserved for future direct integration if needed
func (ca *CodeAnalyzer) addLaravelMetadata(chunk *codetypes.CodeChunk, class *ClassInfo, pkg *PackageInfo) {
	// For now, just mark Laravel classes with a basic tag
	// Full Laravel analysis happens in laravel package to avoid import cycles
	if chunk.Metadata == nil {
		chunk.Metadata = make(map[string]any)
	}

	// Add basic Laravel detection metadata
	if class.Extends == "Model" || class.Extends == "Authenticatable" {
		chunk.Metadata["framework"] = "laravel"
		chunk.Metadata["laravel_type"] = "model"
	} else if strings.Contains(class.Extends, "Controller") {
		chunk.Metadata["framework"] = "laravel"
		chunk.Metadata["laravel_type"] = "controller"
	}
}

// buildClassSignature constructs a human-readable PHP class signature used in
// CodeChunk.Signature and other descriptors. Kept here so that the legacy
// api_analyzer.go (build-tagged out) is not required for normal builds.
func buildClassSignature(cls ClassInfo) string {
	sig := "class " + cls.Name

	if cls.Extends != "" {
		sig += " extends " + cls.Extends
	}

	if len(cls.Implements) > 0 {
		sig += " implements " + strings.Join(cls.Implements, ", ")
	}

	return sig
}

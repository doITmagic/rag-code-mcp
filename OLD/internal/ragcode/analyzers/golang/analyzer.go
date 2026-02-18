package golang

import (
	"bufio"
	"fmt"
	"go/ast"
	"go/doc"
	"go/parser"
	"go/token"
	"go/types"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/doITmagic/rag-code-mcp/internal/codetypes"
)

// CodeAnalyzer mirrors the tutorial's analyzer to extract rich package info.
type CodeAnalyzer struct {
	fset *token.FileSet
}

func NewCodeAnalyzer() *CodeAnalyzer {
	return &CodeAnalyzer{fset: token.NewFileSet()}
}

func (ca *CodeAnalyzer) AnalyzePackage(dir string) (*PackageInfo, error) {
	// Create a new FileSet for each directory
	fset := token.NewFileSet()

	// Parse files individually to retain AST bodies
	files, err := filepath.Glob(filepath.Join(dir, "*.go"))
	if err != nil {
		return nil, fmt.Errorf("globbing directory: %w", err)
	}

	var astFiles []*ast.File
	fileMap := make(map[string]*ast.File)

	for _, file := range files {
		if strings.HasSuffix(file, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, file, nil, parser.ParseComments)
		if err != nil {
			continue // Skip files with parse errors
		}
		astFiles = append(astFiles, f)
		fileMap[file] = f
	}

	if len(astFiles) == 0 {
		return nil, fmt.Errorf("no parseable Go files found in %s", dir)
	}

	// Store fset for this analysis session
	ca.fset = fset

	// Build a map from function name -> AST FuncDecl (with Body) BEFORE doc.New()
	astFuncMap := ca.buildFunctionASTMap(astFiles)

	// Build documentation view (this may modify AST nodes)
	docPkg, err := doc.NewFromFiles(ca.fset, astFiles, "./", doc.Mode(0))
	if err != nil {
		return nil, fmt.Errorf("doc.NewFromFiles: %w", err)
	}

	info := &PackageInfo{
		Name:        docPkg.Name,
		Path:        dir,
		Description: cleanDoc(docPkg.Doc),
		Imports:     ca.extractImports(astFiles),
	}

	// Functions
	for _, fn := range docPkg.Funcs {
		fnInfo := ca.analyzeFunctionDecl(fn, astFuncMap)
		info.Functions = append(info.Functions, fnInfo)
	}

	// Types + methods
	for _, typ := range docPkg.Types {
		typeInfo := ca.analyzeTypeDecl(typ, astFuncMap)
		typeIdx := len(info.Types) // Save index before append
		info.Types = append(info.Types, typeInfo)

		// Process methods for this type
		for _, method := range typ.Methods {
			methodInfo := ca.analyzeFunctionDecl(method, astFuncMap, typ.Name) // pass receiver name
			methodInfo.IsMethod = true
			methodInfo.Receiver = typ.Name
			info.Functions = append(info.Functions, methodInfo)

			// Add to TypeInfo.Methods (modify the slice element directly)
			info.Types[typeIdx].Methods = append(info.Types[typeIdx].Methods,
				ca.convertFunctionToMethodInfo(methodInfo, typ.Name))
		}
	} // Consts and vars
	for _, c := range docPkg.Consts {
		constInfo := ca.analyzeConstantDecl(c)
		info.Constants = append(info.Constants, constInfo...)
	}
	for _, v := range docPkg.Vars {
		varInfo := ca.analyzeVariableDecl(v)
		info.Variables = append(info.Variables, varInfo...)
	}

	return info, nil
}

// buildFunctionASTMap creates a map from function/method name to AST FuncDecl (with Body intact)
func (ca *CodeAnalyzer) buildFunctionASTMap(files []*ast.File) map[string]*ast.BlockStmt {
	funcMap := make(map[string]*ast.BlockStmt)

	for _, file := range files {
		ast.Inspect(file, func(n ast.Node) bool {
			if fn, ok := n.(*ast.FuncDecl); ok {
				// Map by function name (for functions) or receiver.method (for methods)
				key := fn.Name.Name
				if fn.Recv != nil && len(fn.Recv.List) > 0 {
					// Extract receiver type name
					recvType := ""
					switch t := fn.Recv.List[0].Type.(type) {
					case *ast.StarExpr:
						if ident, ok := t.X.(*ast.Ident); ok {
							recvType = ident.Name
						}
					case *ast.Ident:
						recvType = t.Name
					}
					if recvType != "" {
						key = recvType + "." + fn.Name.Name
					}
				}
				// Store ONLY the Body pointer (doc.New won't touch this)
				if fn.Body != nil {
					funcMap[key] = fn.Body
				}
			}
			return true
		})
	}

	return funcMap
}

func (ca *CodeAnalyzer) analyzeFunctionDecl(fn *doc.Func, astBodyMap map[string]*ast.BlockStmt, receiverName ...string) FunctionInfo {
	info := FunctionInfo{
		Name:        fn.Name,
		Description: cleanDoc(fn.Doc),
		IsExported:  ast.IsExported(fn.Name),
		Examples:    ca.extractExamples(fn.Doc),
	}

	// Try to find the AST Body
	key := fn.Name
	if len(receiverName) > 0 && receiverName[0] != "" {
		key = receiverName[0] + "." + fn.Name
	}
	astBody := astBodyMap[key]

	if fn.Decl != nil && astBody != nil {
		// Use doc.Func for position (Pos()) and astBody for end position
		pos := ca.fset.Position(fn.Decl.Pos())
		endPos := ca.fset.Position(astBody.Rbrace + 1) // +1 to include closing brace line

		info.FilePath = pos.Filename
		info.StartLine = pos.Line
		info.EndLine = endPos.Line

		// Extract actual code body
		if code, err := ca.extractCodeFromFile(pos.Filename, pos.Line, endPos.Line); err == nil {
			info.Code = code
		}

		if fn.Decl.Type != nil {
			info.Signature = ca.getFunctionSignature(fn.Decl)
			info.Parameters = ca.extractParameters(fn.Decl.Type.Params)
			info.Returns = ca.extractReturns(fn.Decl.Type.Results)
		}
	} else if fn.Decl != nil {
		// Fallback to doc.Func Decl (won't have Body)
		// Extract position information
		pos := ca.fset.Position(fn.Decl.Pos())
		// Use Body.End() to get the end of the function body, not just the declaration
		endPos := fn.Decl.End()
		if fn.Decl.Body != nil {
			endPos = fn.Decl.Body.End()
		}
		end := ca.fset.Position(endPos)
		info.FilePath = pos.Filename
		info.StartLine = pos.Line
		info.EndLine = end.Line

		// DEBUG: Print position info
		// fmt.Printf("DEBUG analyzeFunctionDecl: %s - Pos:%d Start:%d Body.End:%d EndLine:%d\n",
		// 	fn.Name, fn.Decl.Pos(), pos.Line, endPos, end.Line)

		// Extract actual code body
		if code, err := ca.extractCodeFromFile(pos.Filename, pos.Line, end.Line); err == nil {
			info.Code = code
		}

		if fn.Decl.Type != nil {
			info.Signature = ca.getFunctionSignature(fn.Decl)
			info.Parameters = ca.extractParameters(fn.Decl.Type.Params)
			info.Returns = ca.extractReturns(fn.Decl.Type.Results)
		}
	}
	return info
}

func (ca *CodeAnalyzer) analyzeTypeDecl(typ *doc.Type, astBodyMap map[string]*ast.BlockStmt) TypeInfo {
	info := TypeInfo{
		Name:        typ.Name,
		Description: cleanDoc(typ.Doc),
		IsExported:  ast.IsExported(typ.Name),
	}
	if typ.Decl != nil {
		// Extract position information
		pos := ca.fset.Position(typ.Decl.Pos())
		end := ca.fset.Position(typ.Decl.End())
		info.FilePath = pos.Filename
		info.StartLine = pos.Line
		info.EndLine = end.Line

		// Extract actual code body
		if code, err := ca.extractCodeFromFile(pos.Filename, pos.Line, end.Line); err == nil {
			info.Code = code
		}

		for _, spec := range typ.Decl.Specs {
			if ts, ok := spec.(*ast.TypeSpec); ok {
				info.Kind = ca.getTypeKind(ts.Type)
				if structType, ok := ts.Type.(*ast.StructType); ok {
					info.Fields = ca.extractFields(structType)
				}
				// Extract interface methods (they don't have bodies, only signatures)
				if interfaceType, ok := ts.Type.(*ast.InterfaceType); ok {
					info.Methods = ca.extractInterfaceMethods(interfaceType, typ.Name)
				}
			}
		}
	}
	// Note: Methods for structs are added in AnalyzePackage after processing typ.Methods
	return info
}

// convertFunctionToMethodInfo converts a FunctionInfo to codetypes.MethodInfo
func (ca *CodeAnalyzer) convertFunctionToMethodInfo(fn FunctionInfo, receiverType string) codetypes.MethodInfo {
	return codetypes.MethodInfo{
		Name:         fn.Name,
		Signature:    fn.Signature,
		Description:  fn.Description,
		Parameters:   fn.Parameters,
		Returns:      fn.Returns,
		ReceiverType: receiverType,
		IsExported:   fn.IsExported,
		FilePath:     fn.FilePath,
		StartLine:    fn.StartLine,
		EndLine:      fn.EndLine,
		Code:         fn.Code,
	}
}

// extractInterfaceMethods extracts method signatures from an interface type
func (ca *CodeAnalyzer) extractInterfaceMethods(iface *ast.InterfaceType, typeName string) []codetypes.MethodInfo {
	var methods []codetypes.MethodInfo

	if iface.Methods == nil {
		return methods
	}

	for _, field := range iface.Methods.List {
		// field.Names can be nil for embedded interfaces
		if len(field.Names) == 0 {
			continue
		}

		for _, name := range field.Names {
			method := codetypes.MethodInfo{
				Name:         name.Name,
				ReceiverType: typeName,
				IsExported:   ast.IsExported(name.Name),
			}

			// Extract signature from function type
			if funcType, ok := field.Type.(*ast.FuncType); ok {
				method.Signature = ca.formatInterfaceMethodSignature(name.Name, funcType)
				method.Parameters = ca.extractParameters(funcType.Params)
				method.Returns = ca.extractReturns(funcType.Results)
			}

			// Extract doc comment if available
			if field.Doc != nil {
				method.Description = cleanDoc(field.Doc.Text())
			}

			methods = append(methods, method)
		}
	}

	return methods
}

// formatInterfaceMethodSignature formats an interface method signature
func (ca *CodeAnalyzer) formatInterfaceMethodSignature(name string, funcType *ast.FuncType) string {
	var buf strings.Builder
	buf.WriteString(name)
	buf.WriteString("(")

	if funcType.Params != nil {
		for i, param := range funcType.Params.List {
			if i > 0 {
				buf.WriteString(", ")
			}

			// Parameter names
			for j, paramName := range param.Names {
				if j > 0 {
					buf.WriteString(", ")
				}
				buf.WriteString(paramName.Name)
			}

			// Parameter type
			if len(param.Names) > 0 {
				buf.WriteString(" ")
			}
			buf.WriteString(types.ExprString(param.Type))
		}
	}

	buf.WriteString(")")

	// Return types
	if funcType.Results != nil && len(funcType.Results.List) > 0 {
		buf.WriteString(" ")
		if len(funcType.Results.List) > 1 {
			buf.WriteString("(")
		}
		for i, result := range funcType.Results.List {
			if i > 0 {
				buf.WriteString(", ")
			}
			buf.WriteString(types.ExprString(result.Type))
		}
		if len(funcType.Results.List) > 1 {
			buf.WriteString(")")
		}
	}

	return buf.String()
}

func (ca *CodeAnalyzer) analyzeConstantDecl(c *doc.Value) []ConstantInfo {
	var constants []ConstantInfo

	// Extract position information from declaration
	var filePath string
	var startLine, endLine int
	if c.Decl != nil {
		pos := ca.fset.Position(c.Decl.Pos())
		end := ca.fset.Position(c.Decl.End())
		filePath = pos.Filename
		startLine = pos.Line
		endLine = end.Line
	}

	for _, spec := range c.Decl.Specs {
		if vs, ok := spec.(*ast.ValueSpec); ok {
			for i, name := range vs.Names {
				constInfo := ConstantInfo{
					Name:        name.Name,
					Description: cleanDoc(c.Doc),
					IsExported:  ast.IsExported(name.Name),
					FilePath:    filePath,
					StartLine:   startLine,
					EndLine:     endLine,
				}
				if vs.Type != nil {
					constInfo.Type = ca.typeToString(vs.Type)
				}
				if i < len(vs.Values) && vs.Values[i] != nil {
					constInfo.Value = ca.exprToString(vs.Values[i])
				}
				constants = append(constants, constInfo)
			}
		}
	}
	return constants
}

func (ca *CodeAnalyzer) analyzeVariableDecl(v *doc.Value) []VariableInfo {
	var variables []VariableInfo

	// Extract position information from declaration
	var filePath string
	var startLine, endLine int
	if v.Decl != nil {
		pos := ca.fset.Position(v.Decl.Pos())
		end := ca.fset.Position(v.Decl.End())
		filePath = pos.Filename
		startLine = pos.Line
		endLine = end.Line
	}

	for _, spec := range v.Decl.Specs {
		if vs, ok := spec.(*ast.ValueSpec); ok {
			for _, name := range vs.Names {
				varInfo := VariableInfo{
					Name:        name.Name,
					Description: cleanDoc(v.Doc),
					IsExported:  ast.IsExported(name.Name),
					FilePath:    filePath,
					StartLine:   startLine,
					EndLine:     endLine,
				}
				if vs.Type != nil {
					varInfo.Type = ca.typeToString(vs.Type)
				}
				variables = append(variables, varInfo)
			}
		}
	}
	return variables
}

func (ca *CodeAnalyzer) extractImports(files []*ast.File) []string {
	importSet := make(map[string]bool)
	for _, file := range files {
		for _, imp := range file.Imports {
			path := strings.Trim(imp.Path.Value, "\"")
			importSet[path] = true
		}
	}
	var imports []string
	for imp := range importSet {
		imports = append(imports, imp)
	}
	return imports
}

func (ca *CodeAnalyzer) extractParameters(fields *ast.FieldList) []codetypes.ParamInfo {
	if fields == nil {
		return nil
	}
	var params []codetypes.ParamInfo
	for _, field := range fields.List {
		paramType := ca.typeToString(field.Type)
		if len(field.Names) == 0 {
			params = append(params, codetypes.ParamInfo{Name: "", Type: paramType})
		} else {
			for _, name := range field.Names {
				params = append(params, codetypes.ParamInfo{Name: name.Name, Type: paramType})
			}
		}
	}
	return params
}

func (ca *CodeAnalyzer) extractReturns(fields *ast.FieldList) []codetypes.ReturnInfo {
	if fields == nil {
		return nil
	}
	var returns []codetypes.ReturnInfo
	for _, field := range fields.List {
		returns = append(returns, codetypes.ReturnInfo{Type: ca.typeToString(field.Type)})
	}
	return returns
}

func (ca *CodeAnalyzer) extractFields(structType *ast.StructType) []codetypes.FieldInfo {
	var fields []codetypes.FieldInfo
	for _, field := range structType.Fields.List {
		fieldType := ca.typeToString(field.Type)
		var tag string
		if field.Tag != nil {
			tag = field.Tag.Value
		}
		if len(field.Names) == 0 {
			fields = append(fields, codetypes.FieldInfo{Name: "", Type: fieldType, Tag: tag})
		} else {
			for _, name := range field.Names {
				fields = append(fields, codetypes.FieldInfo{Name: name.Name, Type: fieldType, Tag: tag})
			}
		}
	}
	return fields
}

func (ca *CodeAnalyzer) getFunctionSignature(decl *ast.FuncDecl) string {
	var parts []string
	parts = append(parts, "func")
	if decl.Recv != nil {
		recv := ca.fieldListToString(decl.Recv)
		parts = append(parts, fmt.Sprintf("(%s)", recv))
	}
	parts = append(parts, decl.Name.Name)
	if decl.Type.Params != nil {
		params := ca.fieldListToString(decl.Type.Params)
		parts = append(parts, fmt.Sprintf("(%s)", params))
	} else {
		parts = append(parts, "()")
	}
	if decl.Type.Results != nil {
		results := ca.fieldListToString(decl.Type.Results)
		if len(decl.Type.Results.List) == 1 && len(decl.Type.Results.List[0].Names) == 0 {
			parts = append(parts, results)
		} else {
			parts = append(parts, fmt.Sprintf("(%s)", results))
		}
	}
	return strings.Join(parts, " ")
}

func (ca *CodeAnalyzer) fieldListToString(fields *ast.FieldList) string {
	if fields == nil {
		return ""
	}
	var parts []string
	for _, field := range fields.List {
		fieldType := ca.typeToString(field.Type)
		if len(field.Names) == 0 {
			parts = append(parts, fieldType)
		} else {
			for _, name := range field.Names {
				parts = append(parts, fmt.Sprintf("%s %s", name.Name, fieldType))
			}
		}
	}
	return strings.Join(parts, ", ")
}

func (ca *CodeAnalyzer) typeToString(expr ast.Expr) string {
	switch t := expr.(type) {
	case *ast.Ident:
		return t.Name
	case *ast.StarExpr:
		return "*" + ca.typeToString(t.X)
	case *ast.ArrayType:
		return "[]" + ca.typeToString(t.Elt)
	case *ast.MapType:
		return fmt.Sprintf("map[%s]%s", ca.typeToString(t.Key), ca.typeToString(t.Value))
	case *ast.SelectorExpr:
		return fmt.Sprintf("%s.%s", ca.typeToString(t.X), t.Sel.Name)
	case *ast.InterfaceType:
		return "interface{}"
	default:
		return "unknown"
	}
}

func (ca *CodeAnalyzer) exprToString(expr ast.Expr) string {
	switch e := expr.(type) {
	case *ast.BasicLit:
		return e.Value
	case *ast.Ident:
		return e.Name
	default:
		return "..."
	}
}

func (ca *CodeAnalyzer) getTypeKind(expr ast.Expr) string {
	switch expr.(type) {
	case *ast.StructType:
		return "struct"
	case *ast.InterfaceType:
		return "interface"
	case *ast.ArrayType:
		return "array"
	case *ast.MapType:
		return "map"
	case *ast.ChanType:
		return "channel"
	case *ast.FuncType:
		return "function"
	default:
		return "alias"
	}
}

func (ca *CodeAnalyzer) extractExamples(docstr string) []string {
	var examples []string
	lines := strings.Split(docstr, "\n")
	var inExample bool
	var current strings.Builder
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "Example:") || strings.HasPrefix(trimmed, "Usage:") || strings.Contains(trimmed, "```go") {
			inExample = true
			current.Reset()
			continue
		}
		if inExample {
			if strings.Contains(trimmed, "```") || (trimmed == "" && current.Len() > 0) {
				if current.Len() > 0 {
					examples = append(examples, current.String())
					current.Reset()
				}
				inExample = false
				continue
			}
			if strings.HasPrefix(line, "    ") || strings.HasPrefix(line, "\t") {
				current.WriteString(strings.TrimPrefix(strings.TrimPrefix(line, "    "), "\t"))
				current.WriteString("\n")
			}
		}
	}
	return examples
}

func cleanDoc(docstr string) string {
	if docstr == "" {
		return ""
	}
	docstr = strings.TrimSpace(docstr)
	docstr = strings.ReplaceAll(docstr, "\r\n", "\n")
	lines := strings.Split(docstr, "\n")
	var cleaned []string
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line != "" {
			cleaned = append(cleaned, line)
		}
	}
	return strings.Join(cleaned, "\n")
}

// AnalyzePaths walks directories, analyzes Go packages using CodeAnalyzer, and converts results to CodeChunks.
func (ca *CodeAnalyzer) AnalyzePaths(paths []string) ([]codetypes.CodeChunk, error) {
	var chunks []codetypes.CodeChunk
	visited := make(map[string]bool)
	for _, root := range paths {
		err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() {
				base := filepath.Base(path)
				if base == "vendor" || base == ".git" || base == "testdata" || strings.HasPrefix(base, ".") {
					if path != root { // allow root even if hidden
						return filepath.SkipDir
					}
				}
				return nil
			}
			if !strings.HasSuffix(d.Name(), ".go") || strings.HasSuffix(d.Name(), "_test.go") {
				return nil
			}
			dir := filepath.Dir(path)
			if visited[dir] {
				return nil
			}
			// mark and analyze the whole package directory
			visited[dir] = true
			pkgInfo, perr := ca.AnalyzePackage(dir)
			if perr != nil {
				// Non-fatal: skip directories without proper Go package
				return nil
			}
			chunks = append(chunks, convertPackageInfoToChunks(pkgInfo)...)
			return nil
		})
		if err != nil {
			return nil, err
		}
	}
	return chunks, nil
}

// convertPackageInfoToChunks adapts tutorial structures into CodeChunk entries used by indexer.
func convertPackageInfoToChunks(pi *PackageInfo) []codetypes.CodeChunk {
	var out []codetypes.CodeChunk

	// Functions and methods
	for _, fn := range pi.Functions {
		kind := "function"
		if fn.IsMethod {
			kind = "method"
		}
		out = append(out, codetypes.CodeChunk{
			Type:      kind,
			Name:      fn.Name,
			Package:   pi.Name,
			Language:  "go",
			FilePath:  fn.FilePath,
			StartLine: fn.StartLine,
			EndLine:   fn.EndLine,
			Signature: fn.Signature,
			Docstring: fn.Description,
			Code:      fn.Code,
			Metadata: map[string]any{
				"receiver":  fn.Receiver,
				"is_method": fn.IsMethod,
				"params":    fn.Parameters,
				"returns":   fn.Returns,
				"examples":  fn.Examples,
			},
		})
	}

	// Types (structs, interfaces, aliases)
	for _, tp := range pi.Types {
		sig := tp.Kind
		if sig == "" {
			sig = "type"
		}
		out = append(out, codetypes.CodeChunk{
			Type:      "type",
			Name:      tp.Name,
			Package:   pi.Name,
			Language:  "go",
			FilePath:  tp.FilePath,
			StartLine: tp.StartLine,
			EndLine:   tp.EndLine,
			Signature: fmt.Sprintf("%s %s", sig, tp.Name),
			Docstring: tp.Description,
			Code:      tp.Code,
			Metadata: map[string]any{
				"fields":    tp.Fields,
				"methods":   tp.Methods,
				"is_export": tp.IsExported,
			},
		})
	}

	// Constants
	for _, c := range pi.Constants {
		out = append(out, codetypes.CodeChunk{
			Type:      "const",
			Name:      c.Name,
			Package:   pi.Name,
			Language:  "go",
			FilePath:  c.FilePath,
			StartLine: c.StartLine,
			EndLine:   c.EndLine,
			Signature: fmt.Sprintf("const %s %s", c.Name, c.Type),
			Docstring: c.Description,
			Code:      c.Value,
			Metadata: map[string]any{
				"is_export": c.IsExported,
			},
		})
	}

	// Variables
	for _, v := range pi.Variables {
		out = append(out, codetypes.CodeChunk{
			Type:      "var",
			Name:      v.Name,
			Package:   pi.Name,
			Language:  "go",
			FilePath:  v.FilePath,
			StartLine: v.StartLine,
			EndLine:   v.EndLine,
			Signature: fmt.Sprintf("var %s %s", v.Name, v.Type),
			Docstring: v.Description,
			Code:      "",
			Metadata: map[string]any{
				"is_export": v.IsExported,
			},
		})
	}
	return out
}

// extractCodeFromFile reads source code from a file between specified line numbers (inclusive, 1-based)
func (ca *CodeAnalyzer) extractCodeFromFile(filePath string, startLine, endLine int) (string, error) {
	if startLine <= 0 || endLine <= 0 || startLine > endLine {
		return "", fmt.Errorf("invalid line range: %d-%d", startLine, endLine)
	}

	file, err := os.Open(filePath)
	if err != nil {
		return "", fmt.Errorf("open file: %w", err)
	}
	defer file.Close()

	var lines []string
	scanner := bufio.NewScanner(file)
	lineNum := 1

	for scanner.Scan() {
		if lineNum >= startLine && lineNum <= endLine {
			lines = append(lines, scanner.Text())
		}
		if lineNum > endLine {
			break
		}
		lineNum++
	}

	if err := scanner.Err(); err != nil {
		return "", fmt.Errorf("scan file: %w", err)
	}

	return strings.Join(lines, "\n"), nil
}

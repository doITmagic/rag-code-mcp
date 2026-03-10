package python

import (
	"regexp"
	"strings"
	"unicode"

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
	Variables []VariableInfo
	Constants []ConstantInfo
	FilePath  string
}

// Parse parses a Python source file with tree-sitter
func (p *TreeSitterParser) Parse(source []byte, filePath string) (*PyFileAnalysis, error) {
	lang := grammars.DetectLanguage(filePath)
	if lang == nil {
		return nil, nil
	}

	// Workaround: gotreesitter v0.6.0 cannot parse `except X as e:` — it produces
	// a flat/broken AST. Strip the `as VARNAME` part before parsing.
	// See patchExceptAs for details; the workaround preserves byte offsets.
	parseable := patchExceptAs(source)

	parser := gotreesitter.NewParser(lang.Language())
	tree, err := parser.Parse(parseable)
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
		case "expression_statement":
			p.extractAssignment(child, source, langObj, filePath, fa)
		case "assignment":
			// gotreesitter may put assignments directly at root without expression_statement wrapper
			p.extractAssignmentDirect(child, source, langObj, filePath, fa)
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
		case "block":
			// Extract calls from function body for Code Graph relations
			fn.Calls = p.extractCallsFromNode(child, source, lang)
			// Detect generator
			fn.IsGenerator = p.nodeContainsType(child, lang, "yield")
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
			// Base classes: class Foo(Bar, Mixin, metaclass=Meta):
			for j := 0; j < child.ChildCount(); j++ {
				arg := child.Child(j)
				at := arg.Type(lang)
				switch at {
				case "identifier", "attribute":
					cls.Bases = append(cls.Bases, arg.Text(source))
				case "keyword_argument":
					txt := arg.Text(source)
					if strings.HasPrefix(txt, "metaclass=") {
						cls.Metaclass = strings.TrimPrefix(txt, "metaclass=")
					}
				}
			}
		case "block":
			cls.Description = p.extractDocstringFromBody(node, source, lang)
			cls.Methods = p.extractClassMethods(child, source, lang, cls.Name, filePath)
			cls.ClassVars = p.extractClassVarsFromBlock(child, source, lang, filePath)
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
		if strings.Contains(base, "Mixin") {
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

		switch ct {
		case "function_definition":
			fnNode = child
		case "decorated_definition":
			for j := 0; j < child.ChildCount(); j++ {
				gc := child.Child(j)
				gct := gc.Type(lang)
				switch gct {
				case "decorator":
					dec := strings.TrimPrefix(gc.Text(source), "@")
					dec = strings.SplitN(dec, "\n", 2)[0]
					decorators = append(decorators, dec)
				case "function_definition":
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
			case "block":
				// Extract calls from method body for Code Graph relations
				method.Calls = p.extractCallsFromNode(fc, source, lang)
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
		// Found the block — check if first statement is a docstring
		if child.ChildCount() == 0 {
			return ""
		}
		stmt := child.Child(0)
		stmtType := stmt.Type(lang)

		// Case 1: gotreesitter puts string directly in block (no expression_statement wrapper)
		if stmtType == "string" {
			return p.stripDocstringQuotes(stmt.Text(source))
		}

		// Case 2: expression_statement wrapping a string
		if stmtType == "expression_statement" {
			for k := 0; k < stmt.ChildCount(); k++ {
				expr := stmt.Child(k)
				if expr.Type(lang) == "string" {
					return p.stripDocstringQuotes(expr.Text(source))
				}
			}
		}
		return ""
	}
	return ""
}

// stripDocstringQuotes removes triple-quotes from a docstring text
func (p *TreeSitterParser) stripDocstringQuotes(text string) string {
	for _, q := range []string{`"""`, `'''`} {
		text = strings.TrimPrefix(text, q)
		text = strings.TrimSuffix(text, q)
	}
	text = strings.Trim(text, `"'`)
	return strings.TrimSpace(text)
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

// ── Call extraction for Code Graph ──────────────────────────────────────────

// extractCallsFromNode recursively walks an AST node and extracts all call expressions.
// Powers Code Graph relations (RelCalls) for rag_find_usages / rag_call_hierarchy.
func (p *TreeSitterParser) extractCallsFromNode(node *gotreesitter.Node, source []byte, lang *gotreesitter.Language) []MethodCall {
	var calls []MethodCall
	seen := make(map[string]bool)
	p.walkCalls(node, source, lang, &calls, seen)
	return calls
}

func (p *TreeSitterParser) walkCalls(node *gotreesitter.Node, source []byte, lang *gotreesitter.Language, calls *[]MethodCall, seen map[string]bool) {
	if node == nil {
		return
	}
	if node.Type(lang) == "call" {
		p.handleCallNode(node, source, lang, calls, seen)
	}
	for i := 0; i < node.ChildCount(); i++ {
		p.walkCalls(node.Child(i), source, lang, calls, seen)
	}
}

// handleCallNode extracts a single call: foo(), self.bar(), ClassName.method(), ClassName()
func (p *TreeSitterParser) handleCallNode(callNode *gotreesitter.Node, source []byte, lang *gotreesitter.Language, calls *[]MethodCall, seen map[string]bool) {
	lineNum := int(callNode.StartPoint().Row) + 1

	// Find the function expression child (first non-punctuation child)
	var funcExpr *gotreesitter.Node
	for i := 0; i < callNode.ChildCount(); i++ {
		c := callNode.Child(i)
		ct := c.Type(lang)
		if ct == "identifier" || ct == "attribute" {
			funcExpr = c
			break
		}
	}
	if funcExpr == nil {
		return
	}

	ct := funcExpr.Type(lang)

	switch ct {
	case "identifier":
		// Direct call: foo() or MyClass()
		name := funcExpr.Text(source)
		if name == "" || isBuiltinType(name) || isPythonBuiltinFunc(name) {
			return
		}
		key := "fn." + name
		if !seen[key] {
			*calls = append(*calls, MethodCall{Name: name, Line: lineNum})
			seen[key] = true
		}

	case "attribute":
		// Dotted call: self.method(), ClassName.method(), module.func()
		fullText := funcExpr.Text(source)
		fullText = strings.Join(strings.Fields(fullText), "")

		dotIdx := strings.LastIndex(fullText, ".")
		if dotIdx < 0 || dotIdx == len(fullText)-1 {
			return
		}
		receiver := fullText[:dotIdx]
		method := fullText[dotIdx+1:]

		// Skip self.x() and cls.x() — those are internal method calls
		if receiver == "self" || receiver == "cls" || receiver == "super()" {
			return
		}

		// If receiver looks PascalCase → class static call → add receiver as dependency
		if len(receiver) > 0 && unicode.IsUpper(rune(receiver[0])) && !isBuiltinType(receiver) {
			key := "cls." + receiver
			if !seen[key] {
				*calls = append(*calls, MethodCall{Name: receiver, Line: lineNum})
				seen[key] = true
			}
		}

		// Add the method call itself
		if method != "" && !isPythonBuiltinFunc(method) {
			key := receiver + "." + method
			if !seen[key] {
				*calls = append(*calls, MethodCall{
					Name:      method,
					Receiver:  receiver,
					ClassName: receiver,
					Line:      lineNum,
				})
				seen[key] = true
			}
		}
	}
}

// nodeContainsType checks if a node tree contains a node of the given type.
func (p *TreeSitterParser) nodeContainsType(node *gotreesitter.Node, lang *gotreesitter.Language, nodeType string) bool {
	if node == nil {
		return false
	}
	if node.Type(lang) == nodeType {
		return true
	}
	for i := 0; i < node.ChildCount(); i++ {
		if p.nodeContainsType(node.Child(i), lang, nodeType) {
			return true
		}
	}
	return false
}

// isPythonBuiltinFunc returns true for Python keywords/built-in functions that should NOT be tracked.
func isPythonBuiltinFunc(name string) bool {
	return pythonBuiltins[name]
}

var pythonBuiltins = map[string]bool{
	"if": true, "else": true, "elif": true, "for": true, "while": true,
	"try": true, "except": true, "finally": true, "with": true, "as": true,
	"def": true, "class": true, "return": true, "yield": true, "raise": true,
	"import": true, "from": true, "pass": true, "break": true, "continue": true,
	"and": true, "or": true, "not": true, "in": true, "is": true,
	"lambda": true, "global": true, "nonlocal": true, "assert": true, "del": true,
	"async": true, "await": true,
	"print": true, "len": true, "range": true, "str": true, "int": true,
	"float": true, "bool": true, "list": true, "dict": true, "set": true,
	"tuple": true, "type": true, "isinstance": true, "issubclass": true,
	"hasattr": true, "getattr": true, "setattr": true, "delattr": true,
	"open": true, "input": true, "super": true, "property": true,
	"staticmethod": true, "classmethod": true, "enumerate": true, "zip": true,
	"map": true, "filter": true, "sorted": true, "reversed": true,
	"min": true, "max": true, "sum": true, "abs": true, "round": true,
	"any": true, "all": true, "next": true, "iter": true,
	"repr": true, "hash": true, "id": true, "dir": true, "vars": true,
	"callable": true, "hex": true, "oct": true, "bin": true, "chr": true,
	"ord": true, "format": true, "object": true,
}

// exceptAsRe matches `except <Type> as <var>:` and captures the parts to strip `as <var>`.
// Workaround for gotreesitter v0.6.0 bug: `except X as e:` produces broken AST.
var exceptAsRe = regexp.MustCompile(`(\bexcept\s+\w[\w.]*)(\s+as\s+\w+)(\s*:)`)

// patchExceptAs strips `as VARNAME` from except clauses so gotreesitter can parse correctly.
// The line numbers and structure are preserved (same byte offsets via padding).
func patchExceptAs(source []byte) []byte {
	if !exceptAsRe.Match(source) {
		return source
	}
	// Replace `except ValueError as e:` → `except ValueError      :`
	// We pad with spaces to keep byte offsets and line numbers identical.
	return exceptAsRe.ReplaceAllFunc(source, func(m []byte) []byte {
		parts := exceptAsRe.FindSubmatch(m)
		// parts[1] = "except ValueError"
		// parts[2] = " as e"
		// parts[3] = ":"
		padLen := len(parts[2])
		result := make([]byte, 0, len(m))
		result = append(result, parts[1]...)
		for i := 0; i < padLen; i++ {
			result = append(result, ' ')
		}
		result = append(result, parts[3]...)
		return result
	})
}

// extractAssignment handles module-level assignments: VAR = value, x: int = 5
func (p *TreeSitterParser) extractAssignment(exprStmt *gotreesitter.Node, source []byte, lang *gotreesitter.Language, filePath string, fa *PyFileAnalysis) {
	for i := 0; i < exprStmt.ChildCount(); i++ {
		child := exprStmt.Child(i)
		ct := child.Type(lang)
		if ct != "assignment" {
			continue
		}
		text := child.Text(source)
		parts := strings.SplitN(text, "=", 2)
		if len(parts) < 2 {
			continue
		}
		lhs := strings.TrimSpace(parts[0])
		rhs := strings.TrimSpace(parts[1])
		line := int(child.StartPoint().Row) + 1

		var varName, varType string
		if colonIdx := strings.Index(lhs, ":"); colonIdx > 0 {
			varName = strings.TrimSpace(lhs[:colonIdx])
			varType = strings.TrimSpace(lhs[colonIdx+1:])
		} else {
			varName = lhs
		}

		if varName == "" || strings.Contains(varName, ".") || strings.Contains(varName, "[") {
			continue
		}

		if isConstantName(varName) {
			fa.Constants = append(fa.Constants, ConstantInfo{
				Name: varName, Type: varType, Value: rhs,
				FilePath: filePath, StartLine: line, EndLine: line,
			})
		} else {
			fa.Variables = append(fa.Variables, VariableInfo{
				Name: varName, Type: varType, Value: rhs,
				FilePath: filePath, StartLine: line, EndLine: line,
			})
		}
	}
}

// extractAssignmentDirect handles a raw assignment node at root level (no expression_statement wrapper)
func (p *TreeSitterParser) extractAssignmentDirect(node *gotreesitter.Node, source []byte, lang *gotreesitter.Language, filePath string, fa *PyFileAnalysis) {
	text := node.Text(source)
	parts := strings.SplitN(text, "=", 2)
	if len(parts) < 2 {
		return
	}
	lhs := strings.TrimSpace(parts[0])
	rhs := strings.TrimSpace(parts[1])
	line := int(node.StartPoint().Row) + 1

	var varName, varType string
	if colonIdx := strings.Index(lhs, ":"); colonIdx > 0 {
		varName = strings.TrimSpace(lhs[:colonIdx])
		varType = strings.TrimSpace(lhs[colonIdx+1:])
	} else {
		varName = lhs
	}

	if varName == "" || strings.Contains(varName, ".") || strings.Contains(varName, "[") {
		return
	}

	if isConstantName(varName) {
		fa.Constants = append(fa.Constants, ConstantInfo{
			Name: varName, Type: varType, Value: rhs,
			FilePath: filePath, StartLine: line, EndLine: line,
		})
	} else {
		fa.Variables = append(fa.Variables, VariableInfo{
			Name: varName, Type: varType, Value: rhs,
			FilePath: filePath, StartLine: line, EndLine: line,
		})
	}
}

// extractClassVarsFromBlock extracts class-level variables from a class body block.
// Handles both `expression_statement > assignment` (standard) and
// `assignment` placed directly in the block (gotreesitter quirk, same as module-level).
func (p *TreeSitterParser) extractClassVarsFromBlock(blockNode *gotreesitter.Node, source []byte, lang *gotreesitter.Language, filePath string) []VariableInfo {
	var vars []VariableInfo
	for i := 0; i < blockNode.ChildCount(); i++ {
		child := blockNode.Child(i)
		ct := child.Type(lang)

		// Find the assignment node, regardless of whether it is wrapped in
		// expression_statement or placed directly in the block.
		var assignNode *gotreesitter.Node
		switch ct {
		case "expression_statement":
			for j := 0; j < child.ChildCount(); j++ {
				gc := child.Child(j)
				if gc.Type(lang) == "assignment" {
					assignNode = gc
					break
				}
			}
		case "assignment":
			// gotreesitter may place assignments directly in the block without
			// an expression_statement wrapper (same as at module level).
			assignNode = child
		}

		if assignNode == nil {
			continue
		}

		text := assignNode.Text(source)
		parts := strings.SplitN(text, "=", 2)
		if len(parts) < 2 {
			continue
		}
		lhs := strings.TrimSpace(parts[0])
		rhs := strings.TrimSpace(parts[1])
		line := int(assignNode.StartPoint().Row) + 1

		var varName, varType string
		if colonIdx := strings.Index(lhs, ":"); colonIdx > 0 {
			varName = strings.TrimSpace(lhs[:colonIdx])
			varType = strings.TrimSpace(lhs[colonIdx+1:])
		} else {
			varName = lhs
		}

		if varName == "" || strings.Contains(varName, ".") {
			continue
		}

		vars = append(vars, VariableInfo{
			Name: varName, Type: varType, Value: rhs,
			FilePath: filePath, StartLine: line, EndLine: line,
		})
	}
	return vars
}

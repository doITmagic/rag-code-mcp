package javascript

import (
	"regexp"
	"strings"
)

// Regex patterns for JavaScript/TypeScript symbol extraction
var (
	// Functions
	reFuncDecl   = regexp.MustCompile(`(?m)^(export\s+)?(default\s+)?(async\s+)?function\s*\*?\s+(\w+)\s*(<[^>]*>)?\s*\(([^)]*)\)(?:\s*:\s*([^\s{]+))?\s*\{`)
	reArrowConst = regexp.MustCompile(`(?m)^(export\s+)?(default\s+)?(?:const|let|var)\s+(\w+)\s*(?::\s*[^=]+)?\s*=\s*(async\s+)?(?:\([^)]*\)|[a-zA-Z_]\w*)\s*(?::\s*\w[^\s=]*\s*)?\s*=>\s*`)

	reModuleExports = regexp.MustCompile(`(?m)^module\.exports\s*=\s*`)
	reExportsMethod = regexp.MustCompile(`(?m)^exports\.(\w+)\s*=\s*`)

	// Classes
	reClassDecl   = regexp.MustCompile(`(?m)^(export\s+)?(default\s+)?(abstract\s+)?class\s+(\w+)(?:\s+extends\s+(\w+(?:\.\w+)*))?(?:\s+implements\s+([\w\s,.<>]+))?\s*\{`)
	reMethod      = regexp.MustCompile(`(?m)^\s+(static\s+)?(async\s+)?(get\s+|set\s+)?(#)?(\w+)\s*\(([^)]*)\)(?:\s*:\s*([^\s{]+))?\s*\{`)
	reConstructor = regexp.MustCompile(`(?m)^\s+(constructor)\s*\(([^)]*)\)\s*\{`)

	// TypeScript specific
	reInterface = regexp.MustCompile(`(?m)^(export\s+)?interface\s+(\w+)(?:<[^>]*>)?(?:\s+extends\s+([\w\s,.<>]+))?\s*\{`)
	reTypeAlias = regexp.MustCompile(`(?m)^(export\s+)?type\s+(\w+)(?:<[^>]*>)?\s*=\s*(.+)`)
	reEnum      = regexp.MustCompile(`(?m)^(export\s+)?(const\s+)?enum\s+(\w+)\s*\{`)
	reTSProp    = regexp.MustCompile(`^\s+(\w+)(\?)?:\s*(.+?);?\s*$`)

	// Imports
	reImportDefault    = regexp.MustCompile(`(?m)^import\s+(type\s+)?(\w+)\s+from\s+['"]([^'"]+)['"]`)
	reImportNamed      = regexp.MustCompile(`(?m)^import\s+(type\s+)?\{([^}]+)\}\s+from\s+['"]([^'"]+)['"]`)
	reImportNamespace  = regexp.MustCompile(`(?m)^import\s+\*\s+as\s+(\w+)\s+from\s+['"]([^'"]+)['"]`)
	reImportSideEffect = regexp.MustCompile(`(?m)^import\s+['"]([^'"]+)['"]`)
	reRequire          = regexp.MustCompile(`(?m)(?:const|let|var)\s+(\{[^}]+\}|\w+)\s*=\s*require\s*\(\s*['"]([^'"]+)['"]\s*\)`)

	// Exports
	reExportNamed   = regexp.MustCompile(`(?m)^export\s+\{([^}]+)\}`)
	reExportDefault = regexp.MustCompile(`(?m)^export\s+default\s+(\w+)`)

	// JSDoc
	reJSDoc = regexp.MustCompile(`(?s)/\*\*\s*(.*?)\*/`)
)

// ExtractFunctions extracts function declarations and arrow functions from source code
func ExtractFunctions(source string, filePath string) []JSFunction {
	var functions []JSFunction
	lines := strings.Split(source, "\n")

	// Track JSDoc comments
	jsdocs := extractJSDocPositions(source)

	// Function declarations: (export) (async) function name(params) {
	for _, match := range reFuncDecl.FindAllStringSubmatchIndex(source, -1) {
		line := countLinesUpTo(source, match[0])
		exported := match[2] != -1 && match[3] != -1 && source[match[2]:match[3]] != ""
		isDefault := match[4] != -1 && match[5] != -1 && source[match[4]:match[5]] != ""
		isAsync := match[6] != -1 && match[7] != -1 && source[match[6]:match[7]] != ""
		name := source[match[8]:match[9]]

		params := ""
		if match[12] != -1 && match[13] != -1 {
			params = strings.TrimSpace(source[match[12]:match[13]])
		}
		returnType := ""
		if match[14] != -1 && match[15] != -1 {
			returnType = strings.TrimSpace(source[match[14]:match[15]])
		}

		endLine := findClosingBrace(lines, line-1)

		fn := JSFunction{
			Name:       name,
			Params:     parseParams(params),
			ReturnType: returnType,
			IsAsync:    isAsync,
			IsExported: exported,
			IsDefault:  isDefault,
			Docstring:  findJSDocBefore(jsdocs, match[0]),
			FilePath:   filePath,
			StartLine:  line,
			EndLine:    endLine,
		}
		functions = append(functions, fn)
	}

	// Arrow functions: (export) const name = (async) (...) =>
	for _, match := range reArrowConst.FindAllStringSubmatchIndex(source, -1) {
		line := countLinesUpTo(source, match[0])
		exported := match[2] != -1 && source[match[2]:match[3]] != ""
		isDefault := match[4] != -1 && source[match[4]:match[5]] != ""
		name := source[match[6]:match[7]]
		isAsync := match[8] != -1 && source[match[8]:match[9]] != ""

		endLine := findClosingBrace(lines, line-1)
		if endLine == 0 {
			endLine = line // single-line arrow
		}

		fn := JSFunction{
			Name:       name,
			IsArrow:    true,
			IsAsync:    isAsync,
			IsExported: exported,
			IsDefault:  isDefault,
			Docstring:  findJSDocBefore(jsdocs, match[0]),
			FilePath:   filePath,
			StartLine:  line,
			EndLine:    endLine,
		}
		functions = append(functions, fn)
	}

	return functions
}

// ExtractClasses extracts class declarations from source code
func ExtractClasses(source string, filePath string) []JSClass {
	var classes []JSClass
	lines := strings.Split(source, "\n")
	jsdocs := extractJSDocPositions(source)

	for _, match := range reClassDecl.FindAllStringSubmatchIndex(source, -1) {
		line := countLinesUpTo(source, match[0])
		exported := match[2] != -1 && source[match[2]:match[3]] != ""
		isDefault := match[4] != -1 && source[match[4]:match[5]] != ""
		isAbstract := match[6] != -1 && source[match[6]:match[7]] != ""
		name := source[match[8]:match[9]]

		extends := ""
		if match[10] != -1 {
			extends = strings.TrimSpace(source[match[10]:match[11]])
		}

		var implements []string
		if match[12] != -1 {
			implStr := strings.TrimSpace(source[match[12]:match[13]])
			for _, imp := range strings.Split(implStr, ",") {
				imp = strings.TrimSpace(imp)
				if imp != "" {
					implements = append(implements, imp)
				}
			}
		}

		endLine := findClosingBrace(lines, line-1)

		// Extract methods within the class body
		methods := extractMethods(lines, line-1, endLine-1)

		classes = append(classes, JSClass{
			Name:       name,
			Extends:    extends,
			Implements: implements,
			Methods:    methods,
			IsExported: exported,
			IsDefault:  isDefault,
			IsAbstract: isAbstract,
			Docstring:  findJSDocBefore(jsdocs, match[0]),
			FilePath:   filePath,
			StartLine:  line,
			EndLine:    endLine,
		})
	}

	return classes
}

// ExtractTSInterfaces extracts TypeScript interfaces
func ExtractTSInterfaces(source string, filePath string) []TSInterface {
	var interfaces []TSInterface
	lines := strings.Split(source, "\n")
	jsdocs := extractJSDocPositions(source)

	for _, match := range reInterface.FindAllStringSubmatchIndex(source, -1) {
		line := countLinesUpTo(source, match[0])
		exported := match[2] != -1 && source[match[2]:match[3]] != ""
		name := source[match[4]:match[5]]

		var extends []string
		if match[6] != -1 {
			extStr := strings.TrimSpace(source[match[6]:match[7]])
			for _, ext := range strings.Split(extStr, ",") {
				ext = strings.TrimSpace(ext)
				if ext != "" {
					extends = append(extends, ext)
				}
			}
		}

		endLine := findClosingBrace(lines, line-1)

		// Extract properties
		properties := extractTSProperties(lines, line-1, endLine-1)

		interfaces = append(interfaces, TSInterface{
			Name:       name,
			Extends:    extends,
			Properties: properties,
			IsExported: exported,
			Docstring:  findJSDocBefore(jsdocs, match[0]),
			FilePath:   filePath,
			StartLine:  line,
			EndLine:    endLine,
		})
	}

	return interfaces
}

// ExtractTSTypeAliases extracts TypeScript type aliases
func ExtractTSTypeAliases(source string, filePath string) []TSTypeAlias {
	var types []TSTypeAlias

	for _, match := range reTypeAlias.FindAllStringSubmatchIndex(source, -1) {
		line := countLinesUpTo(source, match[0])
		exported := match[2] != -1 && source[match[2]:match[3]] != ""
		name := source[match[4]:match[5]]
		def := strings.TrimSpace(source[match[6]:match[7]])
		// Strip trailing semicolons
		def = strings.TrimSuffix(def, ";")

		types = append(types, TSTypeAlias{
			Name:       name,
			Definition: def,
			IsExported: exported,
			FilePath:   filePath,
			StartLine:  line,
			EndLine:    line,
		})
	}

	return types
}

// ExtractTSEnums extracts TypeScript enums
func ExtractTSEnums(source string, filePath string) []TSEnum {
	var enums []TSEnum
	lines := strings.Split(source, "\n")

	for _, match := range reEnum.FindAllStringSubmatchIndex(source, -1) {
		line := countLinesUpTo(source, match[0])
		exported := match[2] != -1 && source[match[2]:match[3]] != ""
		isConst := match[4] != -1 && source[match[4]:match[5]] != ""
		name := source[match[6]:match[7]]

		endLine := findClosingBrace(lines, line-1)

		// Extract members
		var members []string
		for i := line; i < endLine-1 && i < len(lines); i++ {
			trimmed := strings.TrimSpace(lines[i])
			trimmed = strings.TrimSuffix(trimmed, ",")
			if trimmed != "" && trimmed != "}" && !strings.HasPrefix(trimmed, "//") {
				// Just get the member name
				parts := strings.SplitN(trimmed, "=", 2)
				memberName := strings.TrimSpace(parts[0])
				if memberName != "" {
					members = append(members, memberName)
				}
			}
		}

		enums = append(enums, TSEnum{
			Name:       name,
			Members:    members,
			IsConst:    isConst,
			IsExported: exported,
			FilePath:   filePath,
			StartLine:  line,
			EndLine:    endLine,
		})
	}

	return enums
}

// ExtractImports extracts import statements
func ExtractImports(source string) []JSImport {
	var imports []JSImport

	// import Default from 'module'
	for _, match := range reImportDefault.FindAllStringSubmatchIndex(source, -1) {
		line := countLinesUpTo(source, match[0])
		isType := match[2] != -1 && source[match[2]:match[3]] != ""
		defaultName := source[match[4]:match[5]]
		src := source[match[6]:match[7]]

		imports = append(imports, JSImport{
			Source:  src,
			Default: defaultName,
			IsType:  isType,
			Line:    line,
		})
	}

	// import { a, b } from 'module'
	for _, match := range reImportNamed.FindAllStringSubmatchIndex(source, -1) {
		line := countLinesUpTo(source, match[0])
		isType := match[2] != -1 && source[match[2]:match[3]] != ""
		namedStr := source[match[4]:match[5]]
		src := source[match[6]:match[7]]

		var named []string
		for _, n := range strings.Split(namedStr, ",") {
			n = strings.TrimSpace(n)
			if n != "" {
				named = append(named, n)
			}
		}

		imports = append(imports, JSImport{
			Source: src,
			Named:  named,
			IsType: isType,
			Line:   line,
		})
	}

	// import * as X from 'module'
	for _, match := range reImportNamespace.FindAllStringSubmatchIndex(source, -1) {
		line := countLinesUpTo(source, match[0])
		ns := source[match[2]:match[3]]
		src := source[match[4]:match[5]]

		imports = append(imports, JSImport{
			Source:    src,
			Namespace: ns,
			Line:      line,
		})
	}

	// require()
	for _, match := range reRequire.FindAllStringSubmatchIndex(source, -1) {
		line := countLinesUpTo(source, match[0])
		binding := source[match[2]:match[3]]
		src := source[match[4]:match[5]]

		imp := JSImport{
			Source: src,
			Line:   line,
		}
		if strings.HasPrefix(binding, "{") {
			// Destructured: const { a, b } = require(...)
			inner := strings.Trim(binding, "{}")
			for _, n := range strings.Split(inner, ",") {
				n = strings.TrimSpace(n)
				if n != "" {
					imp.Named = append(imp.Named, n)
				}
			}
		} else {
			imp.Default = binding
		}
		imports = append(imports, imp)
	}

	return imports
}

// ExtractExports extracts export statements
func ExtractExports(source string) []JSExport {
	var exports []JSExport

	// export { a, b }
	for _, match := range reExportNamed.FindAllStringSubmatchIndex(source, -1) {
		line := countLinesUpTo(source, match[0])
		namedStr := source[match[2]:match[3]]
		for _, n := range strings.Split(namedStr, ",") {
			n = strings.TrimSpace(n)
			if n != "" {
				exports = append(exports, JSExport{
					Name: n,
					Line: line,
				})
			}
		}
	}

	// export default X
	for _, match := range reExportDefault.FindAllStringSubmatchIndex(source, -1) {
		line := countLinesUpTo(source, match[0])
		name := source[match[2]:match[3]]
		exports = append(exports, JSExport{
			Name:      name,
			IsDefault: true,
			Line:      line,
		})
	}

	return exports
}

// --- Helper functions ---

// countLinesUpTo counts the number of newlines before the given offset (1-indexed)
func countLinesUpTo(source string, offset int) int {
	return strings.Count(source[:offset], "\n") + 1
}

// findClosingBrace finds the line number of the matching closing brace
func findClosingBrace(lines []string, startLine int) int {
	depth := 0
	for i := startLine; i < len(lines); i++ {
		for _, ch := range lines[i] {
			if ch == '{' {
				depth++
			} else if ch == '}' {
				depth--
				if depth == 0 {
					return i + 1 // 1-indexed
				}
			}
		}
	}
	return startLine + 1
}

// parseParams splits a parameter string into individual parameter names
func parseParams(params string) []string {
	if params == "" {
		return nil
	}
	var result []string
	for _, p := range strings.Split(params, ",") {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		// Strip type annotations: "name: Type" → "name"
		if idx := strings.Index(p, ":"); idx != -1 {
			p = strings.TrimSpace(p[:idx])
		}
		// Strip defaults: "name = value" → "name"
		if idx := strings.Index(p, "="); idx != -1 {
			p = strings.TrimSpace(p[:idx])
		}
		// Strip rest/spread: "...args" → "args"
		p = strings.TrimPrefix(p, "...")
		if p != "" {
			result = append(result, p)
		}
	}
	return result
}

// extractMethods extracts methods from within a class body
func extractMethods(lines []string, startLine int, endLine int) []JSMethod {
	var methods []JSMethod

	for i := startLine + 1; i < endLine && i < len(lines); i++ {
		line := lines[i]

		// Constructor
		if m := reConstructor.FindStringSubmatch(line); m != nil {
			end := findClosingBrace(lines, i)
			methods = append(methods, JSMethod{
				Name:      "constructor",
				Params:    parseParams(m[2]),
				StartLine: i + 1,
				EndLine:   end,
			})
			i = end - 1
			continue
		}

		// Regular method
		if m := reMethod.FindStringSubmatch(line); m != nil {
			isStatic := m[1] != ""
			isAsync := m[2] != ""
			isPrivate := m[4] != "" // # prefix
			name := m[5]
			params := m[6]
			retType := m[7]

			visibility := "public"
			if isPrivate {
				visibility = "private"
			}

			end := findClosingBrace(lines, i)
			methods = append(methods, JSMethod{
				Name:       name,
				Params:     parseParams(params),
				ReturnType: retType,
				IsAsync:    isAsync,
				IsStatic:   isStatic,
				IsPrivate:  isPrivate,
				Visibility: visibility,
				StartLine:  i + 1,
				EndLine:    end,
			})
			i = end - 1
		}
	}

	return methods
}

// extractTSProperties extracts properties from a TS interface body
func extractTSProperties(lines []string, startLine int, endLine int) []TSProperty {
	var props []TSProperty

	for i := startLine + 1; i < endLine && i < len(lines); i++ {
		if m := reTSProp.FindStringSubmatch(lines[i]); m != nil {
			props = append(props, TSProperty{
				Name:     m[1],
				Type:     strings.TrimSpace(m[3]),
				Optional: m[2] == "?",
			})
		}
	}

	return props
}

// jsdocPosition tracks a JSDoc comment's position in source
type jsdocPosition struct {
	start   int
	end     int
	content string
}

// extractJSDocPositions finds all /** ... */ comments in source
func extractJSDocPositions(source string) []jsdocPosition {
	var positions []jsdocPosition
	for _, match := range reJSDoc.FindAllStringSubmatchIndex(source, -1) {
		positions = append(positions, jsdocPosition{
			start:   match[0],
			end:     match[1],
			content: strings.TrimSpace(source[match[2]:match[3]]),
		})
	}
	return positions
}

// findJSDocBefore finds the JSDoc comment immediately preceding the given offset
func findJSDocBefore(jsdocs []jsdocPosition, offset int) string {
	for _, jsdoc := range jsdocs {
		// JSDoc must end close to the target (within whitespace/newlines)
		between := strings.TrimSpace(strings.ReplaceAll(
			strings.ReplaceAll(source_between(jsdoc.end, offset), "\n", ""),
			"\r", ""))
		if between == "" && jsdoc.end <= offset {
			// Clean up JSDoc content
			lines := strings.Split(jsdoc.content, "\n")
			var cleaned []string
			for _, line := range lines {
				line = strings.TrimSpace(line)
				line = strings.TrimPrefix(line, "* ")
				line = strings.TrimPrefix(line, "*")
				line = strings.TrimSpace(line)
				if line != "" {
					cleaned = append(cleaned, line)
				}
			}
			return strings.Join(cleaned, " ")
		}
	}
	return ""
}

// source_between is a placeholder — actual implementation uses source slicing
// This is called with absolute positions, but we need the source string.
// We'll handle this in the analyzer by pre-storing source.
var sourceCache string

func source_between(start, end int) string {
	if start >= 0 && end <= len(sourceCache) && start <= end {
		return sourceCache[start:end]
	}
	return ""
}

// SetSourceCache sets the source code for JSDoc lookup
func SetSourceCache(source string) {
	sourceCache = source
}

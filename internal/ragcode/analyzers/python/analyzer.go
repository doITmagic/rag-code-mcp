package python

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"unicode"

	"github.com/doITmagic/rag-code-mcp/internal/codetypes"
)

// Pre-compiled regex patterns for better performance
var (
	importRe     = regexp.MustCompile(`^import\s+(.+)$`)
	fromImportRe = regexp.MustCompile(`^from\s+(\S+)\s+import\s+(.+)$`)
)

// CodeAnalyzer implements PathAnalyzer for Python
type CodeAnalyzer struct {
	modules      map[string]*ModuleInfo
	includeTests bool // Option to include test files
}

// NewCodeAnalyzer creates a new Python code analyzer
func NewCodeAnalyzer() *CodeAnalyzer {
	return &CodeAnalyzer{
		modules:      make(map[string]*ModuleInfo),
		includeTests: false,
	}
}

// NewCodeAnalyzerWithOptions creates a Python code analyzer with options
func NewCodeAnalyzerWithOptions(includeTests bool) *CodeAnalyzer {
	return &CodeAnalyzer{
		modules:      make(map[string]*ModuleInfo),
		includeTests: includeTests,
	}
}

// AnalyzePaths implements the PathAnalyzer interface
func (ca *CodeAnalyzer) AnalyzePaths(paths []string) ([]codetypes.CodeChunk, error) {
	// Reset state for global analysis
	ca.modules = make(map[string]*ModuleInfo)

	for _, root := range paths {
		info, err := os.Stat(root)
		if err != nil {
			return nil, fmt.Errorf("error accessing path %s: %w", root, err)
		}

		if info.IsDir() {
			err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
				if err != nil {
					return err
				}

				if d.IsDir() {
					base := filepath.Base(path)
					// Skip common directories that shouldn't be indexed
					if base == ".git" || base == "__pycache__" || base == ".venv" ||
						base == "venv" || base == "env" || base == ".env" ||
						base == "node_modules" || base == ".tox" || base == ".pytest_cache" ||
						base == ".mypy_cache" || base == "dist" || base == "build" ||
						base == "*.egg-info" || strings.HasPrefix(base, ".") {
						if path != root {
							return filepath.SkipDir
						}
					}
					return nil
				}

				// Only analyze Python files
				if !strings.HasSuffix(d.Name(), ".py") {
					return nil
				}

				// Skip test files unless includeTests is enabled
				if !ca.includeTests {
					if strings.HasPrefix(d.Name(), "test_") || strings.HasSuffix(d.Name(), "_test.py") {
						return nil
					}
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
			// Single file
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

// AnalyzeFile analyzes a single Python file
func (ca *CodeAnalyzer) AnalyzeFile(filePath string) ([]codetypes.CodeChunk, error) {
	ca.modules = make(map[string]*ModuleInfo)

	content, err := os.ReadFile(filePath)
	if err != nil {
		return nil, fmt.Errorf("failed to read file: %w", err)
	}

	if err := ca.parseAndCollect(filePath, content); err != nil {
		return nil, err
	}

	return ca.convertToChunks(), nil
}

// GetModules returns the internal module information
func (ca *CodeAnalyzer) GetModules() []*ModuleInfo {
	var result []*ModuleInfo
	for _, mod := range ca.modules {
		result = append(result, mod)
	}
	return result
}

// parseAndCollect parses Python source and collects symbols
func (ca *CodeAnalyzer) parseAndCollect(filePath string, content []byte) error {
	moduleName := ca.extractModuleName(filePath)

	module := &ModuleInfo{
		Name:      moduleName,
		Path:      filePath,
		Classes:   []ClassInfo{},
		Functions: []FunctionInfo{},
		Constants: []ConstantInfo{},
		Variables: []VariableInfo{},
		Imports:   []ImportInfo{},
	}

	lines := strings.Split(string(content), "\n")

	// Extract module docstring
	module.Description = ca.extractModuleDocstring(lines)

	// Parse imports
	module.Imports = ca.extractImports(lines)

	// Parse classes
	module.Classes = ca.extractClasses(lines, filePath, content)

	// Parse module-level functions
	module.Functions = ca.extractFunctions(lines, filePath, content)

	// Parse module-level variables and constants
	module.Variables, module.Constants = ca.extractVariablesAndConstants(lines, filePath)

	ca.modules[moduleName] = module
	return nil
}

// extractModuleName derives module name from file path
func (ca *CodeAnalyzer) extractModuleName(filePath string) string {
	// Get base name without extension
	base := filepath.Base(filePath)
	name := strings.TrimSuffix(base, ".py")

	// Try to build package path from directory structure
	dir := filepath.Dir(filePath)
	parts := []string{name}

	// Walk up looking for __init__.py to determine package structure
	for i := 0; i < 5; i++ { // Limit depth
		initPath := filepath.Join(dir, "__init__.py")
		if _, err := os.Stat(initPath); err == nil {
			parts = append([]string{filepath.Base(dir)}, parts...)
			dir = filepath.Dir(dir)
		} else {
			break
		}
	}

	return strings.Join(parts, ".")
}

// extractModuleDocstring extracts the module-level docstring
func (ca *CodeAnalyzer) extractModuleDocstring(lines []string) string {
	// Skip shebang and encoding declarations
	startIdx := 0
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			startIdx = i + 1
			continue
		}
		break
	}

	if startIdx >= len(lines) {
		return ""
	}

	return ca.extractDocstring(lines, startIdx)
}

// extractDocstring extracts a docstring starting at the given line index
func (ca *CodeAnalyzer) extractDocstring(lines []string, startIdx int) string {
	if startIdx >= len(lines) {
		return ""
	}

	line := strings.TrimSpace(lines[startIdx])

	// Check for triple-quoted string
	var quote string
	if strings.HasPrefix(line, `"""`) {
		quote = `"""`
	} else if strings.HasPrefix(line, `'''`) {
		quote = `'''`
	} else {
		return ""
	}

	// Single line docstring
	if strings.Count(line, quote) >= 2 {
		return strings.Trim(line, quote+" \t")
	}

	// Multi-line docstring
	var docLines []string
	docLines = append(docLines, strings.TrimPrefix(line, quote))

	for i := startIdx + 1; i < len(lines); i++ {
		l := lines[i]
		if strings.Contains(l, quote) {
			// End of docstring
			endPart := strings.Split(l, quote)[0]
			docLines = append(docLines, strings.TrimSpace(endPart))
			break
		}
		docLines = append(docLines, strings.TrimSpace(l))
	}

	return strings.TrimSpace(strings.Join(docLines, "\n"))
}

// extractImports parses import statements (including multi-line imports)
func (ca *CodeAnalyzer) extractImports(lines []string) []ImportInfo {
	var imports []ImportInfo

	for i, line := range lines {
		trimmed := strings.TrimSpace(line)

		// Skip comments and empty lines
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}

		// Check "from X import Y" (including multi-line with parentheses)
		if matches := fromImportRe.FindStringSubmatch(trimmed); matches != nil {
			module := matches[1]
			namesStr := matches[2]

			// Handle multi-line imports: from X import (
			if strings.HasPrefix(strings.TrimSpace(namesStr), "(") {
				namesStr = strings.TrimPrefix(strings.TrimSpace(namesStr), "(")
				// Collect lines until closing parenthesis
				for j := i + 1; j < len(lines); j++ {
					contLine := strings.TrimSpace(lines[j])
					if strings.Contains(contLine, ")") {
						namesStr += " " + strings.TrimSuffix(contLine, ")")
						break
					}
					namesStr += " " + contLine
				}
			}

			// Parse imported names
			var names []string
			for _, name := range strings.Split(namesStr, ",") {
				name = strings.TrimSpace(name)
				// Handle "as" alias
				if idx := strings.Index(name, " as "); idx != -1 {
					name = strings.TrimSpace(name[:idx])
				}
				if name != "" && name != "*" && name != "(" && name != ")" {
					names = append(names, name)
				}
			}

			imports = append(imports, ImportInfo{
				Module:    module,
				Names:     names,
				IsFrom:    true,
				StartLine: i + 1,
			})
			continue
		}

		// Check "import X"
		if matches := importRe.FindStringSubmatch(trimmed); matches != nil {
			modulesStr := matches[1]
			for _, mod := range strings.Split(modulesStr, ",") {
				mod = strings.TrimSpace(mod)
				alias := ""
				if idx := strings.Index(mod, " as "); idx != -1 {
					alias = strings.TrimSpace(mod[idx+4:])
					mod = strings.TrimSpace(mod[:idx])
				}
				imports = append(imports, ImportInfo{
					Module:    mod,
					Alias:     alias,
					IsFrom:    false,
					StartLine: i + 1,
				})
			}
		}
	}

	return imports
}

// extractClasses parses class definitions
func (ca *CodeAnalyzer) extractClasses(lines []string, filePath string, content []byte) []ClassInfo {
	var classes []ClassInfo

	classRe := regexp.MustCompile(`^class\s+(\w+)(?:\s*\(([^)]*)\))?\s*:`)
	decoratorRe := regexp.MustCompile(`^@(\w+(?:\.\w+)*)(?:\(.*\))?$`)

	var currentDecorators []string

	for i := 0; i < len(lines); i++ {
		line := lines[i]
		trimmed := strings.TrimSpace(line)

		// Collect decorators
		if matches := decoratorRe.FindStringSubmatch(trimmed); matches != nil {
			currentDecorators = append(currentDecorators, matches[1])
			continue
		}

		// Check for class definition (must be at module level - no indentation)
		if !strings.HasPrefix(line, " ") && !strings.HasPrefix(line, "\t") {
			if matches := classRe.FindStringSubmatch(trimmed); matches != nil {
				className := matches[1]
				basesStr := ""
				if len(matches) > 2 {
					basesStr = matches[2]
				}

				// Parse base classes
				var bases []string
				if basesStr != "" {
					for _, base := range strings.Split(basesStr, ",") {
						base = strings.TrimSpace(base)
						if base != "" {
							bases = append(bases, base)
						}
					}
				}

				// Find class end line
				startLine := i + 1
				endLine := ca.findBlockEnd(lines, i)

				// Extract class docstring
				docstring := ""
				if i+1 < len(lines) {
					docstring = ca.extractDocstring(lines, i+1)
				}

				// Check for special decorators
				isDataclass := false
				isAbstract := false
				for _, dec := range currentDecorators {
					if dec == "dataclass" || dec == "dataclasses.dataclass" {
						isDataclass = true
					}
					if dec == "abstractmethod" || strings.Contains(dec, "abstract") {
						isAbstract = true
					}
				}

				// Check base classes for special types
				isEnum := false
				isProtocol := false
				for _, base := range bases {
					if base == "ABC" || strings.Contains(base, "Abstract") {
						isAbstract = true
					}
					if base == "Enum" || base == "IntEnum" || base == "StrEnum" || base == "Flag" || base == "IntFlag" {
						isEnum = true
					}
					if base == "Protocol" || base == "typing.Protocol" {
						isProtocol = true
					}
				}

				classInfo := ClassInfo{
					Name:        className,
					Description: docstring,
					Bases:       bases,
					Decorators:  currentDecorators,
					IsAbstract:  isAbstract,
					IsDataclass: isDataclass,
					IsEnum:      isEnum,
					IsProtocol:  isProtocol,
					FilePath:    filePath,
					StartLine:   startLine,
					EndLine:     endLine,
					Code:        extractCodeFromContent(content, startLine, endLine),
				}

				// Extract methods and properties
				classInfo.Methods = ca.extractMethods(lines, i, endLine-1, className, filePath, content)
				classInfo.Properties = ca.extractProperties(classInfo.Methods)
				classInfo.ClassVars = ca.extractClassVariables(lines, i, endLine-1, filePath)

				classes = append(classes, classInfo)
				currentDecorators = nil
			} else if trimmed != "" && !strings.HasPrefix(trimmed, "#") && !strings.HasPrefix(trimmed, "@") {
				// Reset decorators if we hit a non-decorator, non-class line
				currentDecorators = nil
			}
		}
	}

	return classes
}

// extractMethods parses methods within a class
func (ca *CodeAnalyzer) extractMethods(lines []string, classStartIdx, classEndIdx int, className, filePath string, content []byte) []MethodInfo {
	var methods []MethodInfo

	funcRe := regexp.MustCompile(`^\s+(?:async\s+)?def\s+(\w+)\s*\(([^)]*)\)(?:\s*->\s*(\S+))?\s*:`)
	decoratorRe := regexp.MustCompile(`^\s+@(\w+(?:\.\w+)*)(?:\(.*\))?$`)

	var currentDecorators []string

	for i := classStartIdx + 1; i <= classEndIdx && i < len(lines); i++ {
		line := lines[i]
		trimmed := strings.TrimSpace(line)

		// Collect decorators
		if matches := decoratorRe.FindStringSubmatch(line); matches != nil {
			currentDecorators = append(currentDecorators, matches[1])
			continue
		}

		// Check for method definition
		if matches := funcRe.FindStringSubmatch(line); matches != nil {
			methodName := matches[1]
			paramsStr := matches[2]
			returnType := ""
			if len(matches) > 3 {
				returnType = matches[3]
			}

			// Parse parameters
			params := ca.parseParameters(paramsStr)

			// Check decorators for method type
			isStatic := false
			isClassMethod := false
			isProperty := false
			isAbstract := false
			isAsync := strings.Contains(line, "async def")

			for _, dec := range currentDecorators {
				switch dec {
				case "staticmethod":
					isStatic = true
				case "classmethod":
					isClassMethod = true
				case "property":
					isProperty = true
				case "abstractmethod", "abc.abstractmethod":
					isAbstract = true
				default:
					// Check for property setter/deleter (e.g., @value.setter, @value.deleter)
					if strings.HasSuffix(dec, ".setter") || strings.HasSuffix(dec, ".deleter") {
						isProperty = true
					}
				}
			}

			// Find method end
			startLine := i + 1
			endLine := ca.findMethodEnd(lines, i)

			// Extract docstring
			docstring := ""
			if i+1 < len(lines) {
				docstring = ca.extractDocstring(lines, i+1)
			}

			// Build signature
			signature := ca.buildMethodSignature(methodName, params, returnType, isAsync)

			methodInfo := MethodInfo{
				Name:          methodName,
				Signature:     signature,
				Description:   docstring,
				Parameters:    params,
				ReturnType:    returnType,
				Decorators:    currentDecorators,
				IsStatic:      isStatic,
				IsClassMethod: isClassMethod,
				IsProperty:    isProperty,
				IsAbstract:    isAbstract,
				IsAsync:       isAsync,
				ClassName:     className,
				FilePath:      filePath,
				StartLine:     startLine,
				EndLine:       endLine,
				Code:          extractCodeFromContent(content, startLine, endLine),
			}

			methods = append(methods, methodInfo)
			currentDecorators = nil
		} else if trimmed != "" && !strings.HasPrefix(trimmed, "#") && !strings.HasPrefix(trimmed, "@") {
			currentDecorators = nil
		}
	}

	return methods
}

// extractProperties extracts property definitions from methods
func (ca *CodeAnalyzer) extractProperties(methods []MethodInfo) []PropertyInfo {
	propMap := make(map[string]*PropertyInfo)

	for _, method := range methods {
		if !method.IsProperty {
			continue
		}

		// Check for setter/deleter decorators
		isSetter := false
		isDeleter := false
		baseName := method.Name

		for _, dec := range method.Decorators {
			if strings.HasSuffix(dec, ".setter") {
				isSetter = true
				baseName = strings.TrimSuffix(dec, ".setter")
			} else if strings.HasSuffix(dec, ".deleter") {
				isDeleter = true
				baseName = strings.TrimSuffix(dec, ".deleter")
			}
		}

		prop, exists := propMap[baseName]
		if !exists {
			prop = &PropertyInfo{
				Name:        baseName,
				Type:        method.ReturnType,
				Description: method.Description,
				FilePath:    method.FilePath,
				StartLine:   method.StartLine,
				EndLine:     method.EndLine,
			}
			propMap[baseName] = prop
		}

		if isSetter {
			prop.HasSetter = true
		} else if isDeleter {
			prop.HasDeleter = true
		} else {
			prop.HasGetter = true
		}
	}

	var properties []PropertyInfo
	for _, prop := range propMap {
		properties = append(properties, *prop)
	}
	return properties
}

// extractClassVariables extracts class-level variable assignments
func (ca *CodeAnalyzer) extractClassVariables(lines []string, classStartIdx, classEndIdx int, filePath string) []VariableInfo {
	var vars []VariableInfo

	// Match class variable assignments (with optional type annotation)
	varRe := regexp.MustCompile(`^\s{4}(\w+)(?:\s*:\s*(\S+))?\s*=\s*(.+)$`)
	annotationRe := regexp.MustCompile(`^\s{4}(\w+)\s*:\s*(\S+)\s*$`)

	for i := classStartIdx + 1; i <= classEndIdx && i < len(lines); i++ {
		line := lines[i]

		// Skip if inside a method (more than 4 spaces indentation)
		if len(line) > 0 && (strings.HasPrefix(line, "        ") || strings.HasPrefix(line, "\t\t")) {
			continue
		}

		// Check for variable assignment
		if matches := varRe.FindStringSubmatch(line); matches != nil {
			name := matches[1]
			typeName := matches[2]
			value := strings.TrimSpace(matches[3])

			// Skip if it's a method definition
			if strings.HasPrefix(value, "def ") || strings.HasPrefix(value, "lambda") {
				continue
			}

			vars = append(vars, VariableInfo{
				Name:       name,
				Type:       typeName,
				Value:      value,
				IsConstant: isConstantName(name),
				FilePath:   filePath,
				StartLine:  i + 1,
				EndLine:    i + 1,
			})
		} else if matches := annotationRe.FindStringSubmatch(line); matches != nil {
			// Type annotation without assignment
			vars = append(vars, VariableInfo{
				Name:      matches[1],
				Type:      matches[2],
				FilePath:  filePath,
				StartLine: i + 1,
				EndLine:   i + 1,
			})
		}
	}

	return vars
}

// extractFunctions parses module-level functions
func (ca *CodeAnalyzer) extractFunctions(lines []string, filePath string, content []byte) []FunctionInfo {
	var functions []FunctionInfo

	funcRe := regexp.MustCompile(`^(?:async\s+)?def\s+(\w+)\s*\(([^)]*)\)(?:\s*->\s*(\S+))?\s*:`)
	decoratorRe := regexp.MustCompile(`^@(\w+(?:\.\w+)*)(?:\(.*\))?$`)

	var currentDecorators []string

	for i := 0; i < len(lines); i++ {
		line := lines[i]
		trimmed := strings.TrimSpace(line)

		// Collect decorators (at module level - no indentation)
		if !strings.HasPrefix(line, " ") && !strings.HasPrefix(line, "\t") {
			if matches := decoratorRe.FindStringSubmatch(trimmed); matches != nil {
				currentDecorators = append(currentDecorators, matches[1])
				continue
			}
		}

		// Check for function definition at module level (no indentation)
		if !strings.HasPrefix(line, " ") && !strings.HasPrefix(line, "\t") {
			if matches := funcRe.FindStringSubmatch(trimmed); matches != nil {
				funcName := matches[1]
				paramsStr := matches[2]
				returnType := ""
				if len(matches) > 3 {
					returnType = matches[3]
				}

				// Parse parameters
				params := ca.parseParameters(paramsStr)

				isAsync := strings.HasPrefix(trimmed, "async ")

				// Find function end
				startLine := i + 1
				endLine := ca.findBlockEnd(lines, i)

				// Extract docstring
				docstring := ""
				if i+1 < len(lines) {
					docstring = ca.extractDocstring(lines, i+1)
				}

				// Check for generator (yield keyword)
				isGenerator := false
				for j := i + 1; j < endLine && j < len(lines); j++ {
					if strings.Contains(lines[j], "yield") {
						isGenerator = true
						break
					}
				}

				// Build signature
				signature := ca.buildFunctionSignature(funcName, params, returnType, isAsync)

				funcInfo := FunctionInfo{
					Name:        funcName,
					Signature:   signature,
					Description: docstring,
					Parameters:  params,
					ReturnType:  returnType,
					Decorators:  currentDecorators,
					IsAsync:     isAsync,
					IsGenerator: isGenerator,
					FilePath:    filePath,
					StartLine:   startLine,
					EndLine:     endLine,
					Code:        extractCodeFromContent(content, startLine, endLine),
				}

				functions = append(functions, funcInfo)
				currentDecorators = nil
			} else if trimmed != "" && !strings.HasPrefix(trimmed, "#") && !strings.HasPrefix(trimmed, "@") {
				currentDecorators = nil
			}
		}
	}

	return functions
}

// extractVariablesAndConstants parses module-level variables and constants
func (ca *CodeAnalyzer) extractVariablesAndConstants(lines []string, filePath string) ([]VariableInfo, []ConstantInfo) {
	var variables []VariableInfo
	var constants []ConstantInfo

	// Match module-level assignments (no indentation)
	varRe := regexp.MustCompile(`^(\w+)(?:\s*:\s*(\S+))?\s*=\s*(.+)$`)
	annotationRe := regexp.MustCompile(`^(\w+)\s*:\s*(\S+)\s*$`)

	for i, line := range lines {
		// Skip indented lines (inside functions/classes)
		if strings.HasPrefix(line, " ") || strings.HasPrefix(line, "\t") {
			continue
		}

		trimmed := strings.TrimSpace(line)

		// Skip comments, imports, and definitions
		if trimmed == "" || strings.HasPrefix(trimmed, "#") ||
			strings.HasPrefix(trimmed, "import ") || strings.HasPrefix(trimmed, "from ") ||
			strings.HasPrefix(trimmed, "def ") || strings.HasPrefix(trimmed, "class ") ||
			strings.HasPrefix(trimmed, "@") || strings.HasPrefix(trimmed, "async def") {
			continue
		}

		// Check for variable assignment
		if matches := varRe.FindStringSubmatch(trimmed); matches != nil {
			name := matches[1]
			typeName := matches[2]
			value := strings.TrimSpace(matches[3])

			// Skip if it starts with reserved keywords
			if name == "if" || name == "for" || name == "while" || name == "with" || name == "try" {
				continue
			}

			if isConstantName(name) {
				constants = append(constants, ConstantInfo{
					Name:      name,
					Type:      typeName,
					Value:     value,
					FilePath:  filePath,
					StartLine: i + 1,
					EndLine:   i + 1,
				})
			} else {
				variables = append(variables, VariableInfo{
					Name:      name,
					Type:      typeName,
					Value:     value,
					FilePath:  filePath,
					StartLine: i + 1,
					EndLine:   i + 1,
				})
			}
		} else if matches := annotationRe.FindStringSubmatch(trimmed); matches != nil {
			// Type annotation without assignment
			name := matches[1]
			typeName := matches[2]

			if isConstantName(name) {
				constants = append(constants, ConstantInfo{
					Name:      name,
					Type:      typeName,
					FilePath:  filePath,
					StartLine: i + 1,
					EndLine:   i + 1,
				})
			} else {
				variables = append(variables, VariableInfo{
					Name:      name,
					Type:      typeName,
					FilePath:  filePath,
					StartLine: i + 1,
					EndLine:   i + 1,
				})
			}
		}
	}

	return variables, constants
}

// parseParameters parses a parameter string into ParamInfo slice
func (ca *CodeAnalyzer) parseParameters(paramsStr string) []codetypes.ParamInfo {
	var params []codetypes.ParamInfo

	if strings.TrimSpace(paramsStr) == "" {
		return params
	}

	// Simple parameter parsing (handles basic cases)
	// For complex cases with nested brackets, a proper parser would be needed
	depth := 0
	current := ""

	for _, ch := range paramsStr {
		switch ch {
		case '[', '(':
			depth++
			current += string(ch)
		case ']', ')':
			depth--
			current += string(ch)
		case ',':
			if depth == 0 {
				if param := ca.parseParameter(strings.TrimSpace(current)); param != nil {
					params = append(params, *param)
				}
				current = ""
			} else {
				current += string(ch)
			}
		default:
			current += string(ch)
		}
	}

	// Don't forget the last parameter
	if param := ca.parseParameter(strings.TrimSpace(current)); param != nil {
		params = append(params, *param)
	}

	return params
}

// parseParameter parses a single parameter string
func (ca *CodeAnalyzer) parseParameter(paramStr string) *codetypes.ParamInfo {
	if paramStr == "" {
		return nil
	}

	// Handle *args and **kwargs
	if strings.HasPrefix(paramStr, "**") {
		name := strings.TrimPrefix(paramStr, "**")
		if idx := strings.Index(name, ":"); idx != -1 {
			return &codetypes.ParamInfo{
				Name: "**" + strings.TrimSpace(name[:idx]),
				Type: strings.TrimSpace(name[idx+1:]),
			}
		}
		return &codetypes.ParamInfo{Name: "**" + name, Type: ""}
	}
	if strings.HasPrefix(paramStr, "*") {
		name := strings.TrimPrefix(paramStr, "*")
		if idx := strings.Index(name, ":"); idx != -1 {
			return &codetypes.ParamInfo{
				Name: "*" + strings.TrimSpace(name[:idx]),
				Type: strings.TrimSpace(name[idx+1:]),
			}
		}
		return &codetypes.ParamInfo{Name: "*" + name, Type: ""}
	}

	// Handle default values
	defaultIdx := strings.Index(paramStr, "=")
	if defaultIdx != -1 {
		paramStr = paramStr[:defaultIdx]
	}

	// Handle type annotation
	colonIdx := strings.Index(paramStr, ":")
	if colonIdx != -1 {
		name := strings.TrimSpace(paramStr[:colonIdx])
		typeName := strings.TrimSpace(paramStr[colonIdx+1:])
		return &codetypes.ParamInfo{Name: name, Type: typeName}
	}

	return &codetypes.ParamInfo{Name: strings.TrimSpace(paramStr), Type: ""}
}

// findBlockEnd finds the end line of a Python block (class or function)
func (ca *CodeAnalyzer) findBlockEnd(lines []string, startIdx int) int {
	if startIdx >= len(lines) {
		return startIdx + 1
	}

	// Get the indentation of the block header
	baseIndent := getIndentation(lines[startIdx])

	endLine := startIdx + 1
	for i := startIdx + 1; i < len(lines); i++ {
		line := lines[i]

		// Skip empty lines
		if strings.TrimSpace(line) == "" {
			endLine = i + 1
			continue
		}

		// If we find a line with same or less indentation, block ends
		currentIndent := getIndentation(line)
		if currentIndent <= baseIndent && strings.TrimSpace(line) != "" {
			break
		}

		endLine = i + 1
	}

	return endLine
}

// findMethodEnd finds the end line of a method within a class
func (ca *CodeAnalyzer) findMethodEnd(lines []string, startIdx int) int {
	if startIdx >= len(lines) {
		return startIdx + 1
	}

	// Get the indentation of the method definition
	baseIndent := getIndentation(lines[startIdx])

	endLine := startIdx + 1
	for i := startIdx + 1; i < len(lines); i++ {
		line := lines[i]

		// Skip empty lines
		if strings.TrimSpace(line) == "" {
			endLine = i + 1
			continue
		}

		// If we find a line with same or less indentation, method ends
		currentIndent := getIndentation(line)
		if currentIndent <= baseIndent && strings.TrimSpace(line) != "" {
			break
		}

		endLine = i + 1
	}

	return endLine
}

// buildMethodSignature creates a method signature string
func (ca *CodeAnalyzer) buildMethodSignature(name string, params []codetypes.ParamInfo, returnType string, isAsync bool) string {
	var sig strings.Builder

	if isAsync {
		sig.WriteString("async ")
	}
	sig.WriteString("def ")
	sig.WriteString(name)
	sig.WriteString("(")

	var paramStrs []string
	for _, p := range params {
		if p.Type != "" {
			paramStrs = append(paramStrs, fmt.Sprintf("%s: %s", p.Name, p.Type))
		} else {
			paramStrs = append(paramStrs, p.Name)
		}
	}
	sig.WriteString(strings.Join(paramStrs, ", "))
	sig.WriteString(")")

	if returnType != "" {
		sig.WriteString(" -> ")
		sig.WriteString(returnType)
	}

	return sig.String()
}

// buildFunctionSignature creates a function signature string
func (ca *CodeAnalyzer) buildFunctionSignature(name string, params []codetypes.ParamInfo, returnType string, isAsync bool) string {
	return ca.buildMethodSignature(name, params, returnType, isAsync)
}

// convertToChunks converts collected Python symbols to CodeChunks
func (ca *CodeAnalyzer) convertToChunks() []codetypes.CodeChunk {
	var chunks []codetypes.CodeChunk

	for _, module := range ca.modules {
		// Convert classes
		for _, class := range module.Classes {
			chunk := codetypes.CodeChunk{
				Name:      class.Name,
				Type:      "class",
				Language:  "python",
				Package:   module.Name,
				FilePath:  class.FilePath,
				StartLine: class.StartLine,
				EndLine:   class.EndLine,
				Signature: buildClassSignature(class),
				Docstring: class.Description,
				Code:      class.Code,
				Metadata: map[string]any{
					"bases":        class.Bases,
					"decorators":   class.Decorators,
					"is_abstract":  class.IsAbstract,
					"is_dataclass": class.IsDataclass,
					"is_enum":      class.IsEnum,
					"is_protocol":  class.IsProtocol,
				},
			}
			chunks = append(chunks, chunk)

			// Add chunks for each method
			for _, method := range class.Methods {
				// Skip property methods (they're handled separately)
				if method.IsProperty {
					continue
				}

				methodChunk := codetypes.CodeChunk{
					Name:      method.Name,
					Type:      "method",
					Language:  "python",
					Package:   module.Name,
					FilePath:  method.FilePath,
					StartLine: method.StartLine,
					EndLine:   method.EndLine,
					Signature: method.Signature,
					Docstring: method.Description,
					Code:      method.Code,
					Metadata: map[string]any{
						"class_name":     method.ClassName,
						"is_static":      method.IsStatic,
						"is_classmethod": method.IsClassMethod,
						"is_async":       method.IsAsync,
						"decorators":     method.Decorators,
					},
				}
				chunks = append(chunks, methodChunk)
			}

			// Add chunks for properties
			for _, prop := range class.Properties {
				propChunk := codetypes.CodeChunk{
					Name:      prop.Name,
					Type:      "property",
					Language:  "python",
					Package:   module.Name,
					FilePath:  prop.FilePath,
					StartLine: prop.StartLine,
					EndLine:   prop.EndLine,
					Signature: fmt.Sprintf("@property %s: %s", prop.Name, prop.Type),
					Docstring: prop.Description,
					Metadata: map[string]any{
						"has_getter":  prop.HasGetter,
						"has_setter":  prop.HasSetter,
						"has_deleter": prop.HasDeleter,
					},
				}
				chunks = append(chunks, propChunk)
			}
		}

		// Convert module-level functions
		for _, fn := range module.Functions {
			chunk := codetypes.CodeChunk{
				Name:      fn.Name,
				Type:      "function",
				Language:  "python",
				Package:   module.Name,
				FilePath:  fn.FilePath,
				StartLine: fn.StartLine,
				EndLine:   fn.EndLine,
				Signature: fn.Signature,
				Docstring: fn.Description,
				Code:      fn.Code,
				Metadata: map[string]any{
					"is_async":     fn.IsAsync,
					"is_generator": fn.IsGenerator,
					"decorators":   fn.Decorators,
				},
			}
			chunks = append(chunks, chunk)
		}

		// Convert constants
		for _, c := range module.Constants {
			chunk := codetypes.CodeChunk{
				Name:      c.Name,
				Type:      "const",
				Language:  "python",
				Package:   module.Name,
				FilePath:  c.FilePath,
				StartLine: c.StartLine,
				EndLine:   c.EndLine,
				Signature: fmt.Sprintf("%s: %s = %s", c.Name, c.Type, c.Value),
				Docstring: c.Description,
				Code:      c.Value,
			}
			chunks = append(chunks, chunk)
		}

		// Convert module-level variables
		for _, v := range module.Variables {
			chunk := codetypes.CodeChunk{
				Name:      v.Name,
				Type:      "var",
				Language:  "python",
				Package:   module.Name,
				FilePath:  v.FilePath,
				StartLine: v.StartLine,
				EndLine:   v.EndLine,
				Signature: fmt.Sprintf("%s: %s", v.Name, v.Type),
				Docstring: v.Description,
			}
			chunks = append(chunks, chunk)
		}
	}

	return chunks
}

// Helper functions

// extractCodeFromContent extracts code from file content based on line numbers (1-indexed)
func extractCodeFromContent(content []byte, startLine, endLine int) string {
	if content == nil || startLine < 1 || endLine < startLine {
		return ""
	}

	lines := strings.Split(string(content), "\n")
	if startLine > len(lines) {
		return ""
	}

	if endLine > len(lines) {
		endLine = len(lines)
	}

	// Limit code extraction to avoid huge chunks
	maxLines := 100
	if endLine-startLine > maxLines {
		endLine = startLine + maxLines
	}

	return strings.Join(lines[startLine-1:endLine], "\n")
}

// getIndentation returns the number of leading spaces/tabs
func getIndentation(line string) int {
	count := 0
	for _, ch := range line {
		if ch == ' ' {
			count++
		} else if ch == '\t' {
			count += 4 // Treat tab as 4 spaces
		} else {
			break
		}
	}
	return count
}

// isConstantName checks if a name follows Python constant naming convention (UPPER_CASE)
func isConstantName(name string) bool {
	if name == "" {
		return false
	}

	// Must start with uppercase letter
	if !unicode.IsUpper(rune(name[0])) {
		return false
	}

	// Check if all letters are uppercase
	for _, ch := range name {
		if unicode.IsLetter(ch) && !unicode.IsUpper(ch) {
			return false
		}
	}

	return true
}

// buildClassSignature creates a class signature string
func buildClassSignature(cls ClassInfo) string {
	sig := "class " + cls.Name

	if len(cls.Bases) > 0 {
		sig += "(" + strings.Join(cls.Bases, ", ") + ")"
	}

	return sig
}

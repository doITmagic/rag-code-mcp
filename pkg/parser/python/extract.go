package python

import (
	"fmt"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"unicode"

	pkgParser "github.com/doITmagic/rag-code-mcp/pkg/parser"
)

// Pre-compiled regex patterns for better performance
var (
	importRe     = regexp.MustCompile(`^import\s+(.+)$`)
	fromImportRe = regexp.MustCompile(`^from\s+(\S+)\s+import\s+(.+)$`)
)

// CodeAnalyzer implements rich Python analysis
type CodeAnalyzer struct {
	modules      map[string]*ModuleInfo
	includeTests bool // Option to include test files
	tsParser     *TreeSitterParser
}

// NewCodeAnalyzer creates a new Python code analyzer
func NewCodeAnalyzer() *CodeAnalyzer {
	return &CodeAnalyzer{
		modules:      make(map[string]*ModuleInfo),
		includeTests: false,
		tsParser:     NewTreeSitterParser(),
	}
}

// ReleaseResources drops cached tree-sitter parsers so the GC can reclaim arena memory.
func (ca *CodeAnalyzer) ReleaseResources() {
	if ca.tsParser != nil {
		ca.tsParser.ReleaseResources()
	}
}

// AnalyzePaths analyzes the provided paths and returns CodeChunks
func (ca *CodeAnalyzer) AnalyzePaths(paths []string) ([]CodeChunk, error) {
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
					return nil
				}

				if err := ca.parseAndCollect(path, content); err != nil {
					// Skip files that fail to parse
					log.Printf("[DEBUG] Skipping file %s due to parse error: %v", path, err)
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

// parseAndCollect parses Python source and collects symbols
// Uses tree-sitter for accurate AST parsing, with regex as fallback/supplement
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
	module.Description = ca.extractModuleDocstring(lines)

	// Try tree-sitter for imports (more accurate than regex for complex cases)
	tsResult, tsErr := ca.tsParser.Parse(content, filePath)
	if tsErr == nil && tsResult != nil && len(tsResult.Imports) > 0 {
		module.Imports = tsResult.Imports
	} else {
		module.Imports = ca.extractImports(lines)
	}

	// Always use regex for classes and functions — it captures metaclass,
	// method calls, dependencies, and other metadata not in tree-sitter scope
	module.Classes = ca.extractClasses(lines, filePath, content)
	module.Functions = ca.extractFunctions(lines, filePath, content)
	module.Variables, module.Constants = ca.extractVariablesAndConstants(lines, filePath)

	// Augment class bases and function params with tree-sitter type hints when available
	if tsErr == nil && tsResult != nil {
		augmentWithTreeSitter(module, tsResult)
	}

	ca.modules[moduleName] = module
	return nil
}

// augmentWithTreeSitter merges tree-sitter type hints into regex-parsed results
func augmentWithTreeSitter(module *ModuleInfo, ts *PyFileAnalysis) {
	// Build lookup maps from tree-sitter
	tsFuncs := make(map[string]*FunctionInfo, len(ts.Functions))
	for i := range ts.Functions {
		tsFuncs[ts.Functions[i].Name] = &ts.Functions[i]
	}
	tsClasses := make(map[string]*ClassInfo, len(ts.Classes))
	for i := range ts.Classes {
		tsClasses[ts.Classes[i].Name] = &ts.Classes[i]
	}

	// Augment functions: add typed params from tree-sitter if regex missed them
	for i := range module.Functions {
		fn := &module.Functions[i]
		if tsFn, ok := tsFuncs[fn.Name]; ok {
			if len(fn.Parameters) == 0 && len(tsFn.Parameters) > 0 {
				fn.Parameters = tsFn.Parameters
			}
			// Use tree-sitter async detection (more reliable)
			if tsFn.IsAsync {
				fn.IsAsync = true
			}
		}
	}

	// Augment classes: if bases are missing, use tree-sitter's
	for i := range module.Classes {
		cls := &module.Classes[i]
		if tsCls, ok := tsClasses[cls.Name]; ok {
			if len(cls.Bases) == 0 && len(tsCls.Bases) > 0 {
				cls.Bases = tsCls.Bases
			}
		}
	}
}

// extractModuleName derives module name from file path
func (ca *CodeAnalyzer) extractModuleName(filePath string) string {
	base := filepath.Base(filePath)
	name := strings.TrimSuffix(base, ".py")

	dir := filepath.Dir(filePath)
	parts := []string{name}

	for i := 0; i < 5; i++ {
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

	var quote string
	if strings.HasPrefix(line, `"""`) {
		quote = `"""`
	} else if strings.HasPrefix(line, `'''`) {
		quote = `'''`
	} else {
		return ""
	}

	if strings.Count(line, quote) >= 2 {
		return strings.Trim(line, quote+" \t")
	}

	var docLines []string
	docLines = append(docLines, strings.TrimPrefix(line, quote))

	for i := startIdx + 1; i < len(lines); i++ {
		l := lines[i]
		if strings.Contains(l, quote) {
			endPart := strings.Split(l, quote)[0]
			docLines = append(docLines, strings.TrimSpace(endPart))
			break
		}
		docLines = append(docLines, strings.TrimSpace(l))
	}

	return strings.TrimSpace(strings.Join(docLines, "\n"))
}

// extractImports parses import statements
func (ca *CodeAnalyzer) extractImports(lines []string) []ImportInfo {
	var imports []ImportInfo

	for i, line := range lines {
		trimmed := strings.TrimSpace(line)

		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}

		if matches := fromImportRe.FindStringSubmatch(trimmed); matches != nil {
			module := matches[1]
			namesStr := matches[2]

			if strings.HasPrefix(strings.TrimSpace(namesStr), "(") {
				namesStr = strings.TrimPrefix(strings.TrimSpace(namesStr), "(")
				for j := i + 1; j < len(lines); j++ {
					contLine := strings.TrimSpace(lines[j])
					if strings.Contains(contLine, ")") {
						namesStr += " " + strings.TrimSuffix(contLine, ")")
						break
					}
					namesStr += " " + contLine
				}
			}

			var names []string
			for _, name := range strings.Split(namesStr, ",") {
				name = strings.TrimSpace(name)
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

		if matches := decoratorRe.FindStringSubmatch(trimmed); matches != nil {
			currentDecorators = append(currentDecorators, matches[1])
			continue
		}

		if !strings.HasPrefix(line, " ") && !strings.HasPrefix(line, "\t") {
			if matches := classRe.FindStringSubmatch(trimmed); matches != nil {
				className := matches[1]
				basesStr := ""
				if len(matches) > 2 {
					basesStr = matches[2]
				}

				var bases []string
				if basesStr != "" {
					for _, base := range strings.Split(basesStr, ",") {
						base = strings.TrimSpace(base)
						if base != "" {
							bases = append(bases, base)
						}
					}
				}

				startLine := i + 1
				endLine := ca.findBlockEnd(lines, i)

				docstring := ""
				if i+1 < len(lines) {
					docstring = ca.extractDocstring(lines, i+1)
				}

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

				isMixin := isMixinClass(className, bases)
				metaclass := ca.extractMetaclass(lines, i)

				classInfo := ClassInfo{
					Name:        className,
					Description: docstring,
					Bases:       bases,
					Decorators:  currentDecorators,
					IsAbstract:  isAbstract,
					IsDataclass: isDataclass,
					IsEnum:      isEnum,
					IsProtocol:  isProtocol,
					IsMixin:     isMixin,
					Metaclass:   metaclass,
					FilePath:    filePath,
					StartLine:   startLine,
					EndLine:     endLine,
					Code:        extractCodeFromContent(content, startLine, endLine),
				}

				classInfo.Methods = ca.extractMethods(lines, i, endLine-1, className, filePath, content)
				classInfo.Properties = ca.extractProperties(classInfo.Methods)
				classInfo.ClassVars = ca.extractClassVariables(lines, i, endLine-1, filePath)
				classInfo.Dependencies = ca.extractClassDependencies(&classInfo, nil)

				classes = append(classes, classInfo)
				currentDecorators = nil
			} else if trimmed != "" && !strings.HasPrefix(trimmed, "#") && !strings.HasPrefix(trimmed, "@") {
				currentDecorators = nil
			}
		}
	}

	return classes
}

// ... remaining functions will be added in next chunk or combined if possible ...
func (ca *CodeAnalyzer) extractMethods(lines []string, classStartIdx, classEndIdx int, className, filePath string, content []byte) []MethodInfo {
	var methods []MethodInfo

	funcRe := regexp.MustCompile(`^\s+(?:async\s+)?def\s+(\w+)\s*\(([^)]*)\)(?:\s*->\s*(\S+))?\s*:`)
	decoratorRe := regexp.MustCompile(`^\s+@(\w+(?:\.\w+)*)(?:\(.*\))?$`)

	var currentDecorators []string

	for i := classStartIdx + 1; i <= classEndIdx && i < len(lines); i++ {
		line := lines[i]
		trimmed := strings.TrimSpace(line)

		if matches := decoratorRe.FindStringSubmatch(line); matches != nil {
			currentDecorators = append(currentDecorators, matches[1])
			continue
		}

		if matches := funcRe.FindStringSubmatch(line); matches != nil {
			methodName := matches[1]
			paramsStr := matches[2]
			returnType := ""
			if len(matches) > 3 {
				returnType = matches[3]
			}

			params := ca.parseParameters(paramsStr)

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
					if strings.HasSuffix(dec, ".setter") || strings.HasSuffix(dec, ".deleter") {
						isProperty = true
					}
				}
			}

			startLine := i + 1
			endLine := ca.findMethodEnd(lines, i)

			docstring := ""
			if i+1 < len(lines) {
				docstring = ca.extractDocstring(lines, i+1)
			}

			signature := ca.buildMethodSignature(methodName, params, returnType, isAsync)
			calls := ca.extractMethodCalls(lines, i+1, endLine-1)
			typeDeps := ca.extractTypeDependencies(params, returnType)

			methods = append(methods, MethodInfo{
				Name:          methodName,
				Signature:     signature,
				Description:   docstring,
				Parameters:    params,
				ReturnType:    returnType,
				Decorators:    currentDecorators,
				Calls:         calls,
				TypeDeps:      typeDeps,
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
			})
			currentDecorators = nil
		} else if trimmed != "" && !strings.HasPrefix(trimmed, "#") && !strings.HasPrefix(trimmed, "@") {
			currentDecorators = nil
		}
	}
	return methods
}

func (ca *CodeAnalyzer) extractProperties(methods []MethodInfo) []PropertyInfo {
	propMap := make(map[string]*PropertyInfo)
	for _, method := range methods {
		if !method.IsProperty {
			continue
		}
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

func (ca *CodeAnalyzer) extractClassVariables(lines []string, classStartIdx, classEndIdx int, filePath string) []VariableInfo {
	var vars []VariableInfo
	varRe := regexp.MustCompile(`^\s{4}(\w+)(?:\s*:\s*(\S+))?\s*=\s*(.+)$`)
	annotationRe := regexp.MustCompile(`^\s{4}(\w+)\s*:\s*(\S+)\s*$`)
	for i := classStartIdx + 1; i <= classEndIdx && i < len(lines); i++ {
		line := lines[i]
		if len(line) > 0 && (strings.HasPrefix(line, "        ") || strings.HasPrefix(line, "\t\t")) {
			continue
		}
		if matches := varRe.FindStringSubmatch(line); matches != nil {
			name := matches[1]
			vars = append(vars, VariableInfo{
				Name:       name,
				Type:       matches[2],
				Value:      strings.TrimSpace(matches[3]),
				IsConstant: isConstantName(name),
				FilePath:   filePath,
				StartLine:  i + 1,
				EndLine:    i + 1,
			})
		} else if matches := annotationRe.FindStringSubmatch(line); matches != nil {
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

func (ca *CodeAnalyzer) extractFunctions(lines []string, filePath string, content []byte) []FunctionInfo {
	var functions []FunctionInfo
	funcRe := regexp.MustCompile(`^(?:async\s+)?def\s+(\w+)\s*\(([^)]*)\)(?:\s*->\s*(\S+))?\s*:`)
	decoratorRe := regexp.MustCompile(`^@(\w+(?:\.\w+)*)(?:\(.*\))?$`)
	var currentDecorators []string
	for i := 0; i < len(lines); i++ {
		line := lines[i]
		trimmed := strings.TrimSpace(line)
		if !strings.HasPrefix(line, " ") && !strings.HasPrefix(line, "\t") {
			if matches := decoratorRe.FindStringSubmatch(trimmed); matches != nil {
				currentDecorators = append(currentDecorators, matches[1])
				continue
			}
			if matches := funcRe.FindStringSubmatch(trimmed); matches != nil {
				funcName := matches[1]
				paramsStr := matches[2]
				returnType := ""
				if len(matches) > 3 {
					returnType = matches[3]
				}
				params := ca.parseParameters(paramsStr)
				isAsync := strings.HasPrefix(trimmed, "async ")
				startLine := i + 1
				endLine := ca.findBlockEnd(lines, i)
				docstring := ""
				if i+1 < len(lines) {
					docstring = ca.extractDocstring(lines, i+1)
				}
				isGenerator := false
				for j := i + 1; j < endLine && j < len(lines); j++ {
					if strings.Contains(lines[j], "yield") {
						isGenerator = true
						break
					}
				}
				signature := ca.buildFunctionSignature(funcName, params, returnType, isAsync)
				calls := ca.extractMethodCalls(lines, i+1, endLine-1)
				functions = append(functions, FunctionInfo{
					Name:        funcName,
					Signature:   signature,
					Description: docstring,
					Parameters:  params,
					ReturnType:  returnType,
					Decorators:  currentDecorators,
					Calls:       calls,
					IsAsync:     isAsync,
					IsGenerator: isGenerator,
					FilePath:    filePath,
					StartLine:   startLine,
					EndLine:     endLine,
					Code:        extractCodeFromContent(content, startLine, endLine),
				})
				currentDecorators = nil
			} else if trimmed != "" && !strings.HasPrefix(trimmed, "#") && !strings.HasPrefix(trimmed, "@") {
				currentDecorators = nil
			}
		}
	}
	return functions
}

func (ca *CodeAnalyzer) extractVariablesAndConstants(lines []string, filePath string) ([]VariableInfo, []ConstantInfo) {
	var variables []VariableInfo
	var constants []ConstantInfo
	varRe := regexp.MustCompile(`^(\w+)(?:\s*:\s*(\S+))?\s*=\s*(.+)$`)
	annotationRe := regexp.MustCompile(`^(\w+)\s*:\s*(\S+)\s*$`)
	for i, line := range lines {
		if strings.HasPrefix(line, " ") || strings.HasPrefix(line, "\t") {
			continue
		}
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") ||
			strings.HasPrefix(trimmed, "import ") || strings.HasPrefix(trimmed, "from ") ||
			strings.HasPrefix(trimmed, "def ") || strings.HasPrefix(trimmed, "class ") ||
			strings.HasPrefix(trimmed, "@") || strings.HasPrefix(trimmed, "async def") {
			continue
		}
		if matches := varRe.FindStringSubmatch(trimmed); matches != nil {
			name := matches[1]
			if name == "if" || name == "for" || name == "while" || name == "with" || name == "try" {
				continue
			}
			if isConstantName(name) {
				constants = append(constants, ConstantInfo{
					Name:      name,
					Type:      matches[2],
					Value:     strings.TrimSpace(matches[3]),
					FilePath:  filePath,
					StartLine: i + 1,
					EndLine:   i + 1,
				})
			} else {
				variables = append(variables, VariableInfo{
					Name:      name,
					Type:      matches[2],
					Value:     strings.TrimSpace(matches[3]),
					FilePath:  filePath,
					StartLine: i + 1,
					EndLine:   i + 1,
				})
			}
		} else if matches := annotationRe.FindStringSubmatch(trimmed); matches != nil {
			name := matches[1]
			if isConstantName(name) {
				constants = append(constants, ConstantInfo{
					Name:      name,
					Type:      matches[2],
					FilePath:  filePath,
					StartLine: i + 1,
					EndLine:   i + 1,
				})
			} else {
				variables = append(variables, VariableInfo{
					Name:      name,
					Type:      matches[2],
					FilePath:  filePath,
					StartLine: i + 1,
					EndLine:   i + 1,
				})
			}
		}
	}
	return variables, constants
}

func (ca *CodeAnalyzer) parseParameters(paramsStr string) []ParamInfo {
	var params []ParamInfo
	if strings.TrimSpace(paramsStr) == "" {
		return params
	}
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
	if param := ca.parseParameter(strings.TrimSpace(current)); param != nil {
		params = append(params, *param)
	}
	return params
}

func (ca *CodeAnalyzer) parseParameter(paramStr string) *ParamInfo {
	if paramStr == "" {
		return nil
	}
	if strings.HasPrefix(paramStr, "**") {
		name := strings.TrimPrefix(paramStr, "**")
		if idx := strings.Index(name, ":"); idx != -1 {
			return &ParamInfo{Name: "**" + strings.TrimSpace(name[:idx]), Type: strings.TrimSpace(name[idx+1:])}
		}
		return &ParamInfo{Name: "**" + name, Type: ""}
	}
	if strings.HasPrefix(paramStr, "*") {
		name := strings.TrimPrefix(paramStr, "*")
		if idx := strings.Index(name, ":"); idx != -1 {
			return &ParamInfo{Name: "*" + strings.TrimSpace(name[:idx]), Type: strings.TrimSpace(name[idx+1:])}
		}
		return &ParamInfo{Name: "*" + name, Type: ""}
	}
	defaultIdx := strings.Index(paramStr, "=")
	if defaultIdx != -1 {
		paramStr = paramStr[:defaultIdx]
	}
	colonIdx := strings.Index(paramStr, ":")
	if colonIdx != -1 {
		return &ParamInfo{Name: strings.TrimSpace(paramStr[:colonIdx]), Type: strings.TrimSpace(paramStr[colonIdx+1:])}
	}
	return &ParamInfo{Name: strings.TrimSpace(paramStr), Type: ""}
}

func (ca *CodeAnalyzer) findBlockEnd(lines []string, startIdx int) int {
	if startIdx >= len(lines) {
		return startIdx + 1
	}
	baseIndent := getIndentation(lines[startIdx])
	endLine := startIdx + 1
	for i := startIdx + 1; i < len(lines); i++ {
		line := lines[i]
		if strings.TrimSpace(line) == "" {
			endLine = i + 1
			continue
		}
		currentIndent := getIndentation(line)
		if currentIndent <= baseIndent && strings.TrimSpace(line) != "" {
			break
		}
		endLine = i + 1
	}
	return endLine
}

func (ca *CodeAnalyzer) findMethodEnd(lines []string, startIdx int) int {
	return ca.findBlockEnd(lines, startIdx)
}

func (ca *CodeAnalyzer) buildMethodSignature(name string, params []ParamInfo, returnType string, isAsync bool) string {
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

func (ca *CodeAnalyzer) buildFunctionSignature(name string, params []ParamInfo, returnType string, isAsync bool) string {
	return ca.buildMethodSignature(name, params, returnType, isAsync)
}

func (ca *CodeAnalyzer) convertToChunks() []CodeChunk {
	var chunks []CodeChunk
	for _, module := range ca.modules {
		// Convert module level (include module docstring)
		if module.Description != "" {
			chunks = append(chunks, CodeChunk{
				Name:      module.Name,
				Type:      "type", // Mapped to module or generic type
				Language:  "python",
				Package:   module.Name,
				FilePath:  module.Path,
				StartLine: 1,
				EndLine:   1,
				Docstring: module.Description,
				Metadata:  map[string]any{"python_kind": "module"},
			})
		}

		for _, class := range module.Classes {
			chunk := CodeChunk{
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
					"is_mixin":     class.IsMixin,
					"metaclass":    class.Metaclass,
					"dependencies": class.Dependencies,
				},
			}
			// Add basic relations (inheritance)
			for _, base := range class.Bases {
				chunk.Relations = append(chunk.Relations, pkgParser.Relation{TargetName: base, Type: pkgParser.RelInheritance})
			}
			// Add dependency relations
			for _, dep := range class.Dependencies {
				chunk.Relations = append(chunk.Relations, pkgParser.Relation{TargetName: dep, Type: pkgParser.RelDependency})
			}

			chunks = append(chunks, chunk)
			for _, method := range class.Methods {
				if method.IsProperty {
					continue
				}
				methodChunk := CodeChunk{
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
						"class":          method.ClassName,
						"is_static":      method.IsStatic,
						"is_classmethod": method.IsClassMethod,
						"is_async":       method.IsAsync,
						"decorators":     method.Decorators,
						"type_deps":      method.TypeDeps,
					},
				}
				// Add method call relations
				for _, call := range method.Calls {
					methodChunk.Relations = append(methodChunk.Relations, pkgParser.Relation{TargetName: call.Name, Type: pkgParser.RelCalls})
				}
				// Add type dependency relations
				for _, dep := range method.TypeDeps {
					methodChunk.Relations = append(methodChunk.Relations, pkgParser.Relation{TargetName: dep, Type: pkgParser.RelUsesType})
				}

				chunks = append(chunks, methodChunk)
			}
		}
		for _, fn := range module.Functions {
			chunk := CodeChunk{
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
			// Add function call relations
			for _, call := range fn.Calls {
				chunk.Relations = append(chunk.Relations, pkgParser.Relation{TargetName: call.Name, Type: pkgParser.RelCalls})
			}
			chunks = append(chunks, chunk)
		}
		for _, c := range module.Constants {
			chunks = append(chunks, CodeChunk{
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
			})
		}
		for _, v := range module.Variables {
			chunks = append(chunks, CodeChunk{
				Name:      v.Name,
				Type:      "var",
				Language:  "python",
				Package:   module.Name,
				FilePath:  v.FilePath,
				StartLine: v.StartLine,
				EndLine:   v.EndLine,
				Signature: fmt.Sprintf("%s: %s", v.Name, v.Type),
				Docstring: v.Description,
			})
		}
	}
	return chunks
}

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
	return strings.Join(lines[startLine-1:endLine], "\n")
}

func getIndentation(line string) int {
	count := 0
	for _, ch := range line {
		switch ch {
		case ' ':
			count++
		case '\t':
			count += 4
		default:
			return count
		}
	}
	return count
}

func isConstantName(name string) bool {
	if name == "" {
		return false
	}
	if !unicode.IsUpper(rune(name[0])) {
		return false
	}
	for _, ch := range name {
		if unicode.IsLetter(ch) && !unicode.IsUpper(ch) {
			return false
		}
	}
	return true
}

func buildClassSignature(cls ClassInfo) string {
	sig := "class " + cls.Name
	if len(cls.Bases) > 0 {
		sig += "(" + strings.Join(cls.Bases, ", ") + ")"
	}
	return sig
}

func isMixinClass(name string, bases []string) bool {
	if strings.HasSuffix(name, "Mixin") || strings.Contains(name, "Mixin") {
		return true
	}
	for _, base := range bases {
		if strings.HasSuffix(base, "Mixin") || strings.Contains(base, "Mixin") {
			return true
		}
	}
	return false
}

func (ca *CodeAnalyzer) extractMetaclass(lines []string, classLineIdx int) string {
	if classLineIdx >= len(lines) {
		return ""
	}
	line := lines[classLineIdx]
	metaclassRe := regexp.MustCompile(`metaclass\s*=\s*(\w+)`)
	if matches := metaclassRe.FindStringSubmatch(line); matches != nil {
		return matches[1]
	}
	return ""
}

func (ca *CodeAnalyzer) extractMethodCalls(lines []string, startIdx, endIdx int) []MethodCall {
	var calls []MethodCall
	selfCallRe := regexp.MustCompile(`self\.(\w+)\s*\(`)
	clsCallRe := regexp.MustCompile(`cls\.(\w+)\s*\(`)
	superCallRe := regexp.MustCompile(`super\(\)\.(\w+)\s*\(`)
	classCallRe := regexp.MustCompile(`([A-Z]\w+)\.(\w+)\s*\(`)
	funcCallRe := regexp.MustCompile(`(?:^|[^.\w])(\w+)\s*\(`)
	seen := make(map[string]bool)
	for i := startIdx; i <= endIdx && i < len(lines); i++ {
		line := lines[i]
		lineNum := i + 1
		for _, match := range selfCallRe.FindAllStringSubmatch(line, -1) {
			key := "self." + match[1]
			if !seen[key] {
				calls = append(calls, MethodCall{Name: match[1], Receiver: "self", Line: lineNum})
				seen[key] = true
			}
		}
		for _, match := range clsCallRe.FindAllStringSubmatch(line, -1) {
			key := "cls." + match[1]
			if !seen[key] {
				calls = append(calls, MethodCall{Name: match[1], Receiver: "cls", Line: lineNum})
				seen[key] = true
			}
		}
		for _, match := range superCallRe.FindAllStringSubmatch(line, -1) {
			key := "super." + match[1]
			if !seen[key] {
				calls = append(calls, MethodCall{Name: match[1], Receiver: "super()", Line: lineNum})
				seen[key] = true
			}
		}
		for _, match := range classCallRe.FindAllStringSubmatch(line, -1) {
			key := match[1] + "." + match[2]
			if !seen[key] {
				calls = append(calls, MethodCall{Name: match[2], Receiver: match[1], ClassName: match[1], Line: lineNum})
				seen[key] = true
			}
		}
		for _, match := range funcCallRe.FindAllStringSubmatch(line, -1) {
			funcName := match[1]
			if isKeywordOrBuiltin(funcName) {
				continue
			}
			if seen["self."+funcName] || seen["cls."+funcName] {
				continue
			}
			key := "func." + funcName
			if !seen[key] {
				calls = append(calls, MethodCall{Name: funcName, Line: lineNum})
				seen[key] = true
			}
		}
	}
	return calls
}

func (ca *CodeAnalyzer) extractTypeDependencies(params []ParamInfo, returnType string) []string {
	var deps []string
	seen := make(map[string]bool)
	typeRe := regexp.MustCompile(`([A-Z]\w+)`)
	for _, param := range params {
		if param.Type != "" {
			for _, match := range typeRe.FindAllStringSubmatch(param.Type, -1) {
				typeName := match[1]
				if !seen[typeName] && !isBuiltinType(typeName) {
					deps = append(deps, typeName)
					seen[typeName] = true
				}
			}
		}
	}
	if returnType != "" {
		for _, match := range typeRe.FindAllStringSubmatch(returnType, -1) {
			typeName := match[1]
			if !seen[typeName] && !isBuiltinType(typeName) {
				deps = append(deps, typeName)
				seen[typeName] = true
			}
		}
	}
	return deps
}

func (ca *CodeAnalyzer) extractClassDependencies(class *ClassInfo, _ []ImportInfo) []string {
	var deps []string
	seen := make(map[string]bool)
	for _, base := range class.Bases {
		baseName := extractBaseTypeName(base)
		if baseName != "" && !isBuiltinType(baseName) && !seen[baseName] {
			deps = append(deps, baseName)
			seen[baseName] = true
		}
	}
	if class.Metaclass != "" && !seen[class.Metaclass] {
		deps = append(deps, class.Metaclass)
		seen[class.Metaclass] = true
	}
	for _, method := range class.Methods {
		for _, typeDep := range method.TypeDeps {
			if !seen[typeDep] {
				deps = append(deps, typeDep)
				seen[typeDep] = true
			}
		}
	}
	for _, v := range class.ClassVars {
		if v.Type != "" {
			typeName := extractBaseTypeName(v.Type)
			if typeName != "" && !isBuiltinType(typeName) && !seen[typeName] {
				deps = append(deps, typeName)
				seen[typeName] = true
			}
		}
	}
	return deps
}

func isKeywordOrBuiltin(name string) bool {
	keywords := map[string]bool{
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
	}
	return keywords[name]
}

func isBuiltinType(name string) bool {
	builtins := map[string]bool{
		"str": true, "int": true, "float": true, "bool": true, "bytes": true,
		"list": true, "List": true, "dict": true, "Dict": true,
		"set": true, "Set": true, "tuple": true, "Tuple": true,
		"None": true, "Any": true, "Optional": true, "Union": true,
		"Callable": true, "Type": true, "Sequence": true, "Mapping": true,
		"Iterable": true, "Iterator": true, "Generator": true,
		"Coroutine": true, "Awaitable": true, "AsyncIterator": true,
		"Self": true, "TypeVar": true, "Generic": true,
	}
	return builtins[name]
}

func extractBaseTypeName(typeName string) string {
	typeName = strings.TrimSpace(typeName)
	idx := strings.Index(typeName, "[")
	if idx == -1 {
		return typeName
	}
	endIdx := strings.LastIndex(typeName, "]")
	if endIdx == -1 || endIdx <= idx {
		endIdx = len(typeName)
	}
	if idx+1 >= endIdx {
		return typeName[:idx]
	}
	inner := typeName[idx+1 : endIdx]
	if strings.Contains(inner, ",") {
		parts := strings.Split(inner, ",")
		inner = strings.TrimSpace(parts[len(parts)-1])
	}
	return extractBaseTypeName(inner)
}

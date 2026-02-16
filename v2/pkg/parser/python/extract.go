package python

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"unicode"

	"github.com/doITmagic/rag-code-mcp/v2/pkg/parser"
)

var (
	classRe      = regexp.MustCompile(`^(\s*)class\s+([a-zA-Z_][a-zA-Z0-9_]*)(?:\s*\((.*?)\))?\s*:`)
	funcRe       = regexp.MustCompile(`^(\s*)def\s+([a-zA-Z_][a-zA-Z0-9_]*)\s*\((.*?)\)(?:\s*->\s*(.*?))?\s*:`)
	varRe        = regexp.MustCompile(`^(\w+)(?:\s*:\s*(\S+))?\s*=\s*(.+)$`)
	annotationRe = regexp.MustCompile(`^(\w+)\s*:\s*(\S+)\s*$`)
)

func isConstantName(name string) bool {
	if len(name) < 2 {
		return false
	}
	for _, r := range name {
		if unicode.IsLower(r) {
			return false
		}
	}
	return true
}

type extractor struct {
filePath    string
fileContent []byte
moduleName  string
}

func (e *extractor) extract() []parser.Symbol {
	e.moduleName = e.getModuleName()
	lines := strings.Split(string(e.fileContent), "\n")

	var symbols []parser.Symbol

	// Extract module docstring
	moduleDoc := e.extractModuleDocstring(lines)
	if moduleDoc != "" {
		symbols = append(symbols, parser.Symbol{
			Name:      e.moduleName,
			Type:      parser.Type,
			Package:   e.moduleName,
			FilePath:  e.filePath,
			Language:  "python",
			StartLine: 1,
			EndLine:   1,
			Docstring: moduleDoc,
			Metadata:  map[string]any{"python_kind": "module"},
		})
	}

	var currentClass string
	var classIndent int = -1

	for i := 0; i < len(lines); i++ {
		line := lines[i]
		trimmed := strings.TrimSpace(line)

		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}

		// Calculate indentation
		indent := 0
		for _, c := range line {
			if c == ' ' {
				indent++
			} else if c == '\t' {
				indent += 4 // Assume 4 spaces
			} else {
				break
			}
		}

		// Reset current class if we hit a line with less or equal indentation than the class line
		if currentClass != "" && indent <= classIndent && !strings.HasPrefix(trimmed, "def ") && !strings.HasPrefix(trimmed, "class ") {
			currentClass = ""
			classIndent = -1
		}

		// Handle classes
		if matches := classRe.FindStringSubmatch(line); matches != nil {
			name := matches[2]
			bases := matches[3]

			sym := parser.Symbol{
				Name:      name,
				Type:      parser.Class,
				Package:   e.moduleName,
				FilePath:  e.filePath,
				Language:  "python",
				StartLine: i + 1,
				Metadata:  make(map[string]any),
			}
			if bases != "" {
				sym.Metadata["bases"] = strings.Split(bases, ",")
			}

			endLine, doc := e.findBlockRange(lines, i, indent)
			sym.EndLine = endLine
			sym.Docstring = doc

			symbols = append(symbols, sym)
			currentClass = name
			classIndent = indent
			continue
		}

		// Handle functions/methods
		if matches := funcRe.FindStringSubmatch(line); matches != nil {
			name := matches[2]
			args := matches[3]
			ret := matches[4]

			symType := parser.Function
			if currentClass != "" && indent > classIndent {
				symType = parser.Method
			}

			sym := parser.Symbol{
				Name:      name,
				Type:      symType,
				Package:   e.moduleName,
				FilePath:  e.filePath,
				Language:  "python",
				StartLine: i + 1,
				Signature: fmt.Sprintf("def %s(%s)%s", name, args, e.formatReturn(ret)),
				Metadata:  make(map[string]any),
			}
			if symType == parser.Method {
				sym.Metadata["class"] = currentClass
			}

			endLine, doc := e.findBlockRange(lines, i, indent)
			sym.EndLine = endLine
			sym.Docstring = doc

			symbols = append(symbols, sym)
			continue
		}

		// Handle variables and constants (only at module level or class level, not inside functions)
		if indent == 0 || (currentClass != "" && indent == classIndent+4) {
			if matches := varRe.FindStringSubmatch(trimmed); matches != nil {
				name := matches[1]
				typeName := matches[2]
				value := strings.TrimSpace(matches[3])

				// Skip if it starts with reserved keywords
				if name != "if" && name != "for" && name != "while" && name != "with" && name != "try" {
					symType := parser.Var
					if isConstantName(name) {
						symType = parser.Const
					}

					sym := parser.Symbol{
						Name:      name,
						Type:      symType,
						Package:   e.moduleName,
						FilePath:  e.filePath,
						Language:  "python",
						StartLine: i + 1,
						EndLine:   i + 1,
						Metadata:  make(map[string]any),
					}
					if typeName != "" {
						sym.Metadata["type"] = typeName
					}
					sym.Metadata["value"] = value
					if currentClass != "" {
						sym.Metadata["class"] = currentClass
					}
					symbols = append(symbols, sym)
					continue
				}
			}
		}
	}

	return symbols
}

func (e *extractor) extractModuleDocstring(lines []string) string {
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

	line := strings.TrimSpace(lines[startIdx])
	if strings.HasPrefix(line, `"""`) || strings.HasPrefix(line, `'''`) {
		return e.extractDocstring(lines, startIdx)
	}
	return ""
}

func (e *extractor) getModuleName() string {
base := filepath.Base(e.filePath)
name := strings.TrimSuffix(base, ".py")

dir, err := filepath.Abs(filepath.Dir(e.filePath))
if err != nil {
return name
}

parts := []string{name}
for i := 0; i < 5; i++ {
if _, err := os.Stat(filepath.Join(dir, "__init__.py")); err == nil {
parts = append([]string{filepath.Base(dir)}, parts...)
dir = filepath.Dir(dir)
} else {
break
}
}
return strings.Join(parts, ".")
}

func (e *extractor) findBlockRange(lines []string, startIdx int, startIndent int) (int, string) {
doc := ""
endLine := startIdx + 1

// Look for docstring immediately after definition
foundDoc := false
if startIdx+1 < len(lines) {
for j := startIdx + 1; j < len(lines); j++ {
trimmed := strings.TrimSpace(lines[j])
if trimmed == "" {
continue
}
if (strings.HasPrefix(trimmed, `"""`) || strings.HasPrefix(trimmed, `'''`)) && !foundDoc {
doc = e.extractDocstring(lines, j)
foundDoc = true
}
break
}
}

for i := startIdx + 1; i < len(lines); i++ {
line := lines[i]
trimmed := strings.TrimSpace(line)
if trimmed == "" {
continue
}

indent := 0
for _, c := range line {
if c == ' ' {
indent++
} else if c == '\t' {
indent += 4
} else {
break
}
}

if indent <= startIndent && !strings.HasPrefix(trimmed, "#") {
break
}
endLine = i + 1
}
return endLine, doc
}

func (e *extractor) extractDocstring(lines []string, startIdx int) string {
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
return strings.Trim(line, quote)
}

var docLines []string
docLines = append(docLines, strings.TrimPrefix(line, quote))
for i := startIdx + 1; i < len(lines); i++ {
l := lines[i]
if strings.Contains(l, quote) {
docLines = append(docLines, strings.Split(l, quote)[0])
break
}
docLines = append(docLines, l)
}
return strings.TrimSpace(strings.Join(docLines, "\n"))
}

func (e *extractor) formatReturn(ret string) string {
if ret == "" {
return ""
}
return " -> " + strings.TrimSpace(ret)
}

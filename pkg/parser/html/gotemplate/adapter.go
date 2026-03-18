package gotemplate

import (
	"fmt"
	"path/filepath"
	"strings"

	pkgParser "github.com/doITmagic/rag-code-mcp/pkg/parser"
)

// ConvertToSymbols converts parsed GoTemplate results to parser.Symbol entries
// with structural relations (dependency for {{ template }}, inheritance-like for {{ block }}).
func ConvertToSymbols(templates []GoTemplate) []pkgParser.Symbol {
	var symbols []pkgParser.Symbol

	for _, tpl := range templates {
		baseName := filepath.Base(tpl.FilePath)
		nameNoExt := strings.TrimSuffix(baseName, filepath.Ext(baseName))

		// If template has {{ define }} blocks, create a symbol per define.
		if len(tpl.Defines) > 0 {
			for _, def := range tpl.Defines {
				sym := buildDefineSymbol(tpl, def)
				symbols = append(symbols, sym)
			}
		}

		// Always create a file-level symbol for the whole template.
		sym := buildFileSymbol(tpl, nameNoExt)
		symbols = append(symbols, sym)
	}

	return symbols
}

// buildFileSymbol creates a file-level symbol representing the entire template.
func buildFileSymbol(tpl GoTemplate, nameNoExt string) pkgParser.Symbol {
	// Build signature summary
	var sigParts []string
	sigParts = append(sigParts, "go_template")
	if len(tpl.Defines) > 0 {
		names := make([]string, len(tpl.Defines))
		for i, d := range tpl.Defines {
			names[i] = d.Name
		}
		sigParts = append(sigParts, fmt.Sprintf("defines: %s", strings.Join(names, ", ")))
	}
	if len(tpl.TemplateIncludes) > 0 {
		names := make([]string, len(tpl.TemplateIncludes))
		for i, t := range tpl.TemplateIncludes {
			names[i] = t.Name
		}
		sigParts = append(sigParts, fmt.Sprintf("includes: %s", strings.Join(names, ", ")))
	}
	if len(tpl.Blocks) > 0 {
		names := make([]string, len(tpl.Blocks))
		for i, b := range tpl.Blocks {
			names[i] = b.Name
		}
		sigParts = append(sigParts, fmt.Sprintf("blocks: %s", strings.Join(names, ", ")))
	}

	// Build docstring
	var docParts []string
	if len(tpl.Variables) > 0 {
		docParts = append(docParts, fmt.Sprintf("Variables: %s", strings.Join(tpl.Variables, ", ")))
	}
	if len(tpl.CustomFuncs) > 0 {
		docParts = append(docParts, fmt.Sprintf("Custom funcs: %s", strings.Join(tpl.CustomFuncs, ", ")))
	}
	if len(tpl.Ranges) > 0 {
		vars := make([]string, len(tpl.Ranges))
		for i, r := range tpl.Ranges {
			vars[i] = r.Variable
		}
		docParts = append(docParts, fmt.Sprintf("Iterates: %s", strings.Join(vars, ", ")))
	}

	// Build relations: template includes → dependency
	var relations []pkgParser.Relation
	for _, inc := range tpl.TemplateIncludes {
		relations = append(relations, pkgParser.Relation{
			TargetName: inc.Name,
			Type:       pkgParser.RelDependency,
		})
	}

	endLine := tpl.TotalLines
	if endLine < 1 {
		endLine = 1
	}

	return pkgParser.Symbol{
		Name:      nameNoExt,
		Type:      pkgParser.Type,
		FilePath:  tpl.FilePath,
		Language:  "html",
		StartLine: 1,
		EndLine:   endLine,
		Signature: strings.Join(sigParts, " | "),
		Docstring: strings.Join(docParts, " | "),
		IsPublic:  true,
		Relations: relations,
		Metadata: map[string]any{
			"template_type": "go_template",
			"defines":       extractDefineNames(tpl),
			"includes":      extractIncludeNames(tpl),
			"blocks":        extractBlockNames(tpl),
			"variables":     tpl.Variables,
			"custom_funcs":  tpl.CustomFuncs,
			"ranges":        extractRangeVars(tpl),
		},
	}
}

// buildDefineSymbol creates a symbol for a specific {{ define "name" }} block.
func buildDefineSymbol(tpl GoTemplate, def DefineDirective) pkgParser.Symbol {
	endLine := def.EndLine
	if endLine < def.Line {
		endLine = def.Line
	}

	// Relations: any {{ template "x" }} inside this define are dependencies
	var relations []pkgParser.Relation
	for _, inc := range tpl.TemplateIncludes {
		if inc.Line >= def.Line && inc.Line <= endLine {
			relations = append(relations, pkgParser.Relation{
				TargetName: inc.Name,
				Type:       pkgParser.RelDependency,
			})
		}
	}

	return pkgParser.Symbol{
		Name:      def.Name,
		Type:      pkgParser.Type,
		FilePath:  tpl.FilePath,
		Language:  "html",
		StartLine: def.Line,
		EndLine:   endLine,
		Signature: fmt.Sprintf(`go_template | {{ define "%s" }}`, def.Name),
		IsPublic:  true,
		Relations: relations,
		Metadata: map[string]any{
			"template_type": "go_template_define",
			"define_name":   def.Name,
		},
	}
}

func extractDefineNames(tpl GoTemplate) []string {
	names := make([]string, len(tpl.Defines))
	for i, d := range tpl.Defines {
		names[i] = d.Name
	}
	return names
}

func extractIncludeNames(tpl GoTemplate) []string {
	names := make([]string, len(tpl.TemplateIncludes))
	for i, t := range tpl.TemplateIncludes {
		names[i] = t.Name
	}
	return names
}

func extractBlockNames(tpl GoTemplate) []string {
	names := make([]string, len(tpl.Blocks))
	for i, b := range tpl.Blocks {
		names[i] = b.Name
	}
	return names
}

func extractRangeVars(tpl GoTemplate) []string {
	vars := make([]string, len(tpl.Ranges))
	for i, r := range tpl.Ranges {
		vars[i] = r.Variable
	}
	return vars
}

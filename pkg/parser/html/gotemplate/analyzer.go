package gotemplate

import (
	"bufio"
	"os"
	"regexp"
	"strings"
)

// Regex patterns for Go template directives.
var (
	reDefine   = regexp.MustCompile(`\{\{-?\s*define\s+"([^"]+)"\s*-?\}\}`)
	reBlock    = regexp.MustCompile(`\{\{-?\s*block\s+"([^"]+)"\s*(\.[\w.]*)?`)
	reTemplate = regexp.MustCompile(`\{\{-?\s*template\s+"([^"]+)"\s*(\.[\w.]*)?`)
	reRange    = regexp.MustCompile(`\{\{-?\s*range\s+(\.[\w.]+)`)
	reIf       = regexp.MustCompile(`\{\{-?\s*if\s+(.+?)\s*-?\}\}`)
	reElse     = regexp.MustCompile(`\{\{-?\s*else\s*-?\}\}`)
	reWith     = regexp.MustCompile(`\{\{-?\s*with\s+(\.[\w.]+)`)
	reEnd      = regexp.MustCompile(`\{\{-?\s*end\s*-?\}\}`)
	reComment  = regexp.MustCompile(`\{\{/\*.*?\*/\}\}`)
	reVariable = regexp.MustCompile(`\{\{-?\s*(\.[\w.]+)\s*-?\}\}`)
	// Custom funcs: {{ funcName ... }} where funcName is not a keyword.
	reCustomFunc = regexp.MustCompile(`\{\{-?\s*([a-zA-Z]\w+)\s+`)

	// Go template keywords that are NOT custom functions.
	keywords = map[string]bool{
		"if": true, "else": true, "end": true, "range": true,
		"with": true, "template": true, "define": true, "block": true,
		"nil": true, "not": true, "and": true, "or": true,
		"print": true, "printf": true, "println": true,
		"len": true, "index": true, "slice": true, "call": true,
		"html": true, "js": true, "urlquery": true,
		"eq": true, "ne": true, "lt": true, "le": true, "gt": true, "ge": true,
	}
)

// GoTemplateAnalyzer parses Go template files and extracts directives.
type GoTemplateAnalyzer struct{}

// Analyze parses the given template files, extracting directives.
func (a *GoTemplateAnalyzer) Analyze(filePaths []string) []GoTemplate {
	var templates []GoTemplate
	for _, fp := range filePaths {
		tpl, err := a.analyzeFile(fp)
		if err != nil {
			continue // skip unreadable files
		}
		templates = append(templates, tpl)
	}
	return templates
}

// analyzeFile parses a single Go template file.
func (a *GoTemplateAnalyzer) analyzeFile(filePath string) (GoTemplate, error) {
	file, err := os.Open(filePath)
	if err != nil {
		return GoTemplate{}, err
	}
	defer file.Close()

	tpl := GoTemplate{
		FilePath: filePath,
	}

	// Track open blocks for EndLine matching.
	type openBlock struct {
		kind string // "define", "block", "range", "if", "with"
		idx  int    // index in the corresponding slice
	}
	var stack []openBlock

	varSet := make(map[string]bool)
	funcSet := make(map[string]bool)

	lineNum := 0
	scanner := bufio.NewScanner(file)
	// Allow lines up to 1MB.
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	for scanner.Scan() {
		lineNum++
		line := scanner.Text()

		// Comments (can be multiline in theory, but we handle single-line here)
		if matches := reComment.FindAllString(line, -1); len(matches) > 0 {
			tpl.Comments = append(tpl.Comments, matches...)
		}

		// {{ define "name" }}
		if m := reDefine.FindStringSubmatch(line); m != nil {
			tpl.Defines = append(tpl.Defines, DefineDirective{
				Name: m[1],
				Line: lineNum,
			})
			stack = append(stack, openBlock{kind: "define", idx: len(tpl.Defines) - 1})
		}

		// {{ block "name" pipeline }}
		if m := reBlock.FindStringSubmatch(line); m != nil {
			pipeline := strings.TrimSpace(m[2])
			tpl.Blocks = append(tpl.Blocks, BlockDirective{
				Name:     m[1],
				Pipeline: pipeline,
				Line:     lineNum,
			})
			stack = append(stack, openBlock{kind: "block", idx: len(tpl.Blocks) - 1})
		}

		// {{ template "name" pipeline }}
		if m := reTemplate.FindStringSubmatch(line); m != nil {
			pipeline := strings.TrimSpace(m[2])
			tpl.TemplateIncludes = append(tpl.TemplateIncludes, TemplateInclude{
				Name:     m[1],
				Pipeline: pipeline,
				Line:     lineNum,
			})
		}

		// {{ range .Variable }}
		if m := reRange.FindStringSubmatch(line); m != nil {
			tpl.Ranges = append(tpl.Ranges, RangeDirective{
				Variable: m[1],
				Line:     lineNum,
			})
			stack = append(stack, openBlock{kind: "range", idx: len(tpl.Ranges) - 1})
		}

		// {{ if .Condition }}
		if m := reIf.FindStringSubmatch(line); m != nil {
			tpl.Conditionals = append(tpl.Conditionals, ConditionalDirective{
				Condition: strings.TrimSpace(m[1]),
				Line:      lineNum,
			})
			stack = append(stack, openBlock{kind: "if", idx: len(tpl.Conditionals) - 1})
		}

		// {{ else }}
		if reElse.MatchString(line) {
			// Find the matching if on the stack
			for i := len(stack) - 1; i >= 0; i-- {
				if stack[i].kind == "if" {
					tpl.Conditionals[stack[i].idx].HasElse = true
					break
				}
			}
		}

		// {{ with .Pipeline }}
		if m := reWith.FindStringSubmatch(line); m != nil {
			tpl.WithBlocks = append(tpl.WithBlocks, WithDirective{
				Pipeline: m[1],
				Line:     lineNum,
			})
			stack = append(stack, openBlock{kind: "with", idx: len(tpl.WithBlocks) - 1})
		}

		// {{ end }}
		if reEnd.MatchString(line) && len(stack) > 0 {
			top := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			switch top.kind {
			case "define":
				tpl.Defines[top.idx].EndLine = lineNum
			case "block":
				tpl.Blocks[top.idx].EndLine = lineNum
			case "range":
				tpl.Ranges[top.idx].EndLine = lineNum
			case "if":
				tpl.Conditionals[top.idx].EndLine = lineNum
			case "with":
				tpl.WithBlocks[top.idx].EndLine = lineNum
			}
		}

		// Variables: {{ .Something }} — but not inside other directives we already captured
		for _, m := range reVariable.FindAllStringSubmatch(line, -1) {
			v := m[1]
			if !varSet[v] {
				varSet[v] = true
				tpl.Variables = append(tpl.Variables, v)
			}
		}

		// Custom functions: {{ funcName arg }} where funcName is not a keyword
		for _, m := range reCustomFunc.FindAllStringSubmatch(line, -1) {
			funcName := m[1]
			if !keywords[funcName] && !funcSet[funcName] {
				funcSet[funcName] = true
				tpl.CustomFuncs = append(tpl.CustomFuncs, funcName)
			}
		}
	}

	tpl.TotalLines = lineNum
	return tpl, nil
}

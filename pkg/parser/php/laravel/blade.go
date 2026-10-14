package laravel

import (
	"bufio"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/doITmagic/rag-code-mcp/internal/logger"
)

// Compiled regex patterns for Blade directives
var (
	reExtends          = regexp.MustCompile(`@extends\(\s*['"]([^'"]+)['"]`)
	reSection          = regexp.MustCompile(`@section\(\s*['"]([^'"]+)['"]`)
	reYield            = regexp.MustCompile(`@yield\(\s*['"]([^'"]+)['"]`)
	reInclude          = regexp.MustCompile(`@include(?:If)?\(\s*['"]([^'"]+)['"]`)
	reIncludeCond      = regexp.MustCompile(`@include(?:When|Unless)\(\s*(?:[^,'"]+,\s*)?['"]([^'"]+)['"]`)
	reIncludeFirstArr  = regexp.MustCompile(`@includeFirst\(\s*\[([^\]]+)\]`)
	reIncludeFirstStr  = regexp.MustCompile(`@includeFirst\(\s*['"]([^'"]+)['"]`)
	reComponent        = regexp.MustCompile(`@component\(\s*['"]([^'"]+)['"]`)
	reXComponent       = regexp.MustCompile(`<x-([a-zA-Z0-9_.-]+)`)
	reLivewire         = regexp.MustCompile(`@livewire\(\s*['"]([^'"]+)['"]`)
	reLivewireTag      = regexp.MustCompile(`<livewire:([a-zA-Z0-9_.-]+)`)
	reEach             = regexp.MustCompile(`@each\(\s*['"]([^'"]+)['"]`)
	rePushStack        = regexp.MustCompile(`@(?:push|stack)\(\s*['"]([^'"]+)['"]`)
	reProps            = regexp.MustCompile(`(?s)@props\(\s*\[(.*?)\]\s*\)`)
)

// BladeAnalyzer parses Blade template files and extracts directives.
type BladeAnalyzer struct{}

// NewBladeAnalyzer creates a new BladeAnalyzer.
func NewBladeAnalyzer() *BladeAnalyzer {
	return &BladeAnalyzer{}
}

// Analyze parses the given Blade template files, extracting directives.
// Files that cannot be read are logged and skipped (no error returned).
func (ba *BladeAnalyzer) Analyze(filePaths []string) []BladeTemplate {
	var templates []BladeTemplate

	for _, fp := range filePaths {
		tpl, err := ba.analyzeFile(fp)
		if err != nil {
			logger.Instance.Debug("[BLADE] skip %s: %v", filepath.Base(fp), err)
			continue
		}
		templates = append(templates, tpl)
	}

	return templates
}

// analyzeFile parses a single Blade file.
func (ba *BladeAnalyzer) analyzeFile(filePath string) (BladeTemplate, error) {
	f, err := os.Open(filePath)
	if err != nil {
		return BladeTemplate{}, err
	}
	defer f.Close()

	tpl := BladeTemplate{
		Name:     bladeViewName(filePath),
		FilePath: filePath,
	}

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 64*1024), 1024*1024) // Allow lines up to 1MB
	lineNum := 0
	var propsBuf strings.Builder
	inMultilineProps := false

	for scanner.Scan() {
		lineNum++
		line := scanner.Text()

		// Handle multi-line @props([...]) accumulation
		if inMultilineProps {
			propsBuf.WriteString("\n")
			propsBuf.WriteString(line)
			combined := propsBuf.String()
			if matches := reProps.FindAllStringSubmatch(combined, -1); len(matches) > 0 {
				for _, m := range matches {
					props := parsePropsArray(m[1])
					tpl.Props = append(tpl.Props, props...)
				}
				propsBuf.Reset()
				inMultilineProps = false
			}
		} else if matches := reProps.FindAllStringSubmatch(line, -1); len(matches) > 0 {
			for _, m := range matches {
				props := parsePropsArray(m[1])
				tpl.Props = append(tpl.Props, props...)
			}
		} else if strings.Contains(line, "@props(") {
			propsBuf.Reset()
			propsBuf.WriteString(line)
			inMultilineProps = true
		}

		// @extends
		for _, m := range reExtends.FindAllStringSubmatch(line, -1) {
			if len(m) > 1 {
				tpl.Extends = m[1]
			}
		}

		// @section
		for _, m := range reSection.FindAllStringSubmatch(line, -1) {
			if len(m) > 1 {
				tpl.Sections = append(tpl.Sections, BladeSection{
					Name:      m[1],
					Type:      "section",
					StartLine: lineNum,
				})
			}
		}

		// @yield
		for _, m := range reYield.FindAllStringSubmatch(line, -1) {
			if len(m) > 1 {
				tpl.Sections = append(tpl.Sections, BladeSection{
					Name:      m[1],
					Type:      "yield",
					StartLine: lineNum,
				})
			}
		}

		// @include / @includeIf
		for _, m := range reInclude.FindAllStringSubmatch(line, -1) {
			if len(m) > 1 {
				tpl.Includes = append(tpl.Includes, BladeInclude{
					ViewName: m[1],
					Type:     "include",
					Line:     lineNum,
				})
			}
		}

		// @includeWhen / @includeUnless
		for _, m := range reIncludeCond.FindAllStringSubmatch(line, -1) {
			if len(m) > 1 {
				tpl.Includes = append(tpl.Includes, BladeInclude{
					ViewName: m[1],
					Type:     "include",
					Line:     lineNum,
				})
			}
		}

		// @includeFirst(['a', 'b']) or @includeFirst('a')
		for _, m := range reIncludeFirstArr.FindAllStringSubmatch(line, -1) {
			if len(m) > 1 {
				for _, view := range parsePropsArray(m[1]) {
					tpl.Includes = append(tpl.Includes, BladeInclude{
						ViewName: view,
						Type:     "include",
						Line:     lineNum,
					})
				}
			}
		}
		for _, m := range reIncludeFirstStr.FindAllStringSubmatch(line, -1) {
			if len(m) > 1 {
				tpl.Includes = append(tpl.Includes, BladeInclude{
					ViewName: m[1],
					Type:     "include",
					Line:     lineNum,
				})
			}
		}

		// @component
		for _, m := range reComponent.FindAllStringSubmatch(line, -1) {
			if len(m) > 1 {
				tpl.Includes = append(tpl.Includes, BladeInclude{
					ViewName: m[1],
					Type:     "component",
					Line:     lineNum,
				})
			}
		}

		// <x-...> Blade component tags
		for _, m := range reXComponent.FindAllStringSubmatch(line, -1) {
			if len(m) > 1 && m[1] != "slot" && !strings.HasPrefix(m[1], "slot:") {
				tpl.Includes = append(tpl.Includes, BladeInclude{
					ViewName: m[1],
					Type:     "component",
					Line:     lineNum,
				})
			}
		}

		// @livewire / <livewire:...>
		for _, m := range reLivewire.FindAllStringSubmatch(line, -1) {
			if len(m) > 1 {
				tpl.Includes = append(tpl.Includes, BladeInclude{
					ViewName: m[1],
					Type:     "livewire",
					Line:     lineNum,
				})
			}
		}
		for _, m := range reLivewireTag.FindAllStringSubmatch(line, -1) {
			if len(m) > 1 {
				tpl.Includes = append(tpl.Includes, BladeInclude{
					ViewName: m[1],
					Type:     "livewire",
					Line:     lineNum,
				})
			}
		}

		// @each
		for _, m := range reEach.FindAllStringSubmatch(line, -1) {
			if len(m) > 1 {
				tpl.Includes = append(tpl.Includes, BladeInclude{
					ViewName: m[1],
					Type:     "each",
					Line:     lineNum,
				})
			}
		}

		// @push / @stack
		for _, m := range rePushStack.FindAllStringSubmatch(line, -1) {
			if len(m) > 1 {
				tpl.Stacks = appendUnique(tpl.Stacks, m[1])
			}
		}
	}

	tpl.TotalLines = lineNum

	return tpl, scanner.Err()
}

// bladeViewName converts a file path to Laravel dot notation.
// Example: /project/resources/views/layouts/app.blade.php → layouts.app
func bladeViewName(filePath string) string {
	// Normalize to forward slashes
	fp := filepath.ToSlash(filePath)

	// Try to find resources/views/ in the path
	marker := "resources/views/"
	idx := strings.LastIndex(fp, marker)
	if idx >= 0 {
		relative := fp[idx+len(marker):]
		// Remove .blade.php extension
		relative = strings.TrimSuffix(relative, ".blade.php")
		return strings.ReplaceAll(relative, "/", ".")
	}

	// Fallback: use basename without extension
	base := filepath.Base(filePath)
	return strings.TrimSuffix(base, ".blade.php")
}

// parsePropsArray extracts prop names from a @props([...]) content string.
// Supports both simple values ("'title', 'color'") and key-value defaults ("'color' => 'blue'").
func parsePropsArray(raw string) []string {
	var props []string
	parts := strings.Split(raw, ",")
	for _, p := range parts {
		if idx := strings.Index(p, "=>"); idx >= 0 {
			p = p[:idx]
		}
		p = strings.TrimSpace(p)
		p = strings.Trim(p, "'\"")
		if p != "" {
			props = append(props, p)
		}
	}
	return props
}

// appendUnique appends s to slice only if not already present.
func appendUnique(slice []string, s string) []string {
	for _, existing := range slice {
		if existing == s {
			return slice
		}
	}
	return append(slice, s)
}

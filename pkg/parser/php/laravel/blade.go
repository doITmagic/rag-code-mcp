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
	reExtends   = regexp.MustCompile(`@extends\(\s*['"]([^'"]+)['"]`)
	reSection   = regexp.MustCompile(`@section\(\s*['"]([^'"]+)['"]`)
	reYield     = regexp.MustCompile(`@yield\(\s*['"]([^'"]+)['"]`)
	reInclude   = regexp.MustCompile(`@include\(\s*['"]([^'"]+)['"]`)
	reComponent = regexp.MustCompile(`@component\(\s*['"]([^'"]+)['"]`)
	reEach      = regexp.MustCompile(`@each\(\s*['"]([^'"]+)['"]`)
	rePushStack = regexp.MustCompile(`@(?:push|stack)\(\s*['"]([^'"]+)['"]`)
	reProps     = regexp.MustCompile(`@props\(\s*\[(.*?)\]\s*\)`)
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
	for scanner.Scan() {
		lineNum++
		line := scanner.Text()

		// @extends
		if m := reExtends.FindStringSubmatch(line); len(m) > 1 {
			tpl.Extends = m[1]
		}

		// @section
		if m := reSection.FindStringSubmatch(line); len(m) > 1 {
			tpl.Sections = append(tpl.Sections, BladeSection{
				Name:      m[1],
				Type:      "section",
				StartLine: lineNum,
			})
		}

		// @yield
		if m := reYield.FindStringSubmatch(line); len(m) > 1 {
			tpl.Sections = append(tpl.Sections, BladeSection{
				Name:      m[1],
				Type:      "yield",
				StartLine: lineNum,
			})
		}

		// @include
		if m := reInclude.FindStringSubmatch(line); len(m) > 1 {
			tpl.Includes = append(tpl.Includes, BladeInclude{
				ViewName: m[1],
				Type:     "include",
				Line:     lineNum,
			})
		}

		// @component
		if m := reComponent.FindStringSubmatch(line); len(m) > 1 {
			tpl.Includes = append(tpl.Includes, BladeInclude{
				ViewName: m[1],
				Type:     "component",
				Line:     lineNum,
			})
		}

		// @each
		if m := reEach.FindStringSubmatch(line); len(m) > 1 {
			tpl.Includes = append(tpl.Includes, BladeInclude{
				ViewName: m[1],
				Type:     "each",
				Line:     lineNum,
			})
		}

		// @push / @stack
		if m := rePushStack.FindStringSubmatch(line); len(m) > 1 {
			tpl.Stacks = appendUnique(tpl.Stacks, m[1])
		}

		// @props
		if m := reProps.FindStringSubmatch(line); len(m) > 1 {
			props := parsePropsArray(m[1])
			tpl.Props = append(tpl.Props, props...)
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
// Input: "'title', 'color'" → Output: ["title", "color"]
func parsePropsArray(raw string) []string {
	var props []string
	parts := strings.Split(raw, ",")
	for _, p := range parts {
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

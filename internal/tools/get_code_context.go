package tools

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// GetCodeContextTool reads code from a file with surrounding context lines
type GetCodeContextTool struct{}

// NewGetCodeContextTool creates a new code context tool
func NewGetCodeContextTool() *GetCodeContextTool {
	return &GetCodeContextTool{}
}

func (t *GetCodeContextTool) Name() string {
	return "get_code_context"
}

func (t *GetCodeContextTool) Description() string {
	return "Read code from a specific file location with surrounding context lines"
}

func (t *GetCodeContextTool) Execute(ctx context.Context, args map[string]interface{}) (string, error) {
	filePath, ok := args["file_path"].(string)
	if !ok || filePath == "" {
		return "", fmt.Errorf("file_path is required")
	}

	startLine, ok := args["start_line"].(float64)
	if !ok {
		return "", fmt.Errorf("start_line is required")
	}

	endLine, ok := args["end_line"].(float64)
	if !ok {
		return "", fmt.Errorf("end_line is required")
	}

	// Optional context lines (default: 5)
	contextLines := 5
	if ctx, ok := args["context_lines"].(float64); ok {
		contextLines = int(ctx)
	}

	resolvedPath, err := resolvePath(filePath)
	if err != nil {
		return "", err
	}

	// Read file content
	content, err := os.ReadFile(resolvedPath)
	if err != nil {
		return "", fmt.Errorf("failed to read file: %w", err)
	}

	lines := strings.Split(string(content), "\n")
	totalLines := len(lines)

	// Convert to int and validate
	start := int(startLine)
	end := int(endLine)

	if start < 1 {
		start = 1
	}
	if end > totalLines {
		end = totalLines
	}
	if start > end {
		return "", fmt.Errorf("start_line (%d) must be <= end_line (%d)", start, end)
	}

	// Calculate context range
	contextStart := start - contextLines
	if contextStart < 1 {
		contextStart = 1
	}

	contextEnd := end + contextLines
	if contextEnd > totalLines {
		contextEnd = totalLines
	}

	// Build response with line numbers
	var response strings.Builder
	response.WriteString(fmt.Sprintf("# %s\n\n", filepath.Base(resolvedPath)))
	response.WriteString(fmt.Sprintf("**File:** `%s`\n", resolvedPath))
	response.WriteString(fmt.Sprintf("**Lines:** %d-%d (with %d lines context)\n", start, end, contextLines))
	response.WriteString(fmt.Sprintf("**Total file lines:** %d\n\n", totalLines))

	response.WriteString("```go\n")

	// Add context before (dimmed)
	if contextStart < start {
		for i := contextStart; i < start; i++ {
			response.WriteString(fmt.Sprintf("%4d │ %s\n", i, lines[i-1]))
		}
		if contextStart < start {
			response.WriteString("     ┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄\n")
		}
	}

	// Add main content (highlighted)
	for i := start; i <= end; i++ {
		response.WriteString(fmt.Sprintf("%4d ┃ %s\n", i, lines[i-1]))
	}

	// Add context after (dimmed)
	if end < contextEnd {
		response.WriteString("     ┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄\n")
		for i := end + 1; i <= contextEnd; i++ {
			response.WriteString(fmt.Sprintf("%4d │ %s\n", i, lines[i-1]))
		}
	}

	response.WriteString("```\n")

	return response.String(), nil
}

func resolvePath(path string) (string, error) {
	cleanPath := filepath.Clean(path)
	if filepath.IsAbs(cleanPath) {
		if _, err := os.Stat(cleanPath); err == nil {
			return cleanPath, nil
		}
		return "", fmt.Errorf("file not found: %s", cleanPath)
	}

	if _, err := os.Stat(cleanPath); err == nil {
		return cleanPath, nil
	}

	if execPath, err := os.Executable(); err == nil {
		dir := filepath.Dir(execPath)
		for i := 0; i < 5; i++ {
			candidate := filepath.Join(dir, cleanPath)
			if _, err := os.Stat(candidate); err == nil {
				return candidate, nil
			}
			dir = filepath.Dir(dir)
		}
	}

	return "", fmt.Errorf("file not found: %s", cleanPath)
}

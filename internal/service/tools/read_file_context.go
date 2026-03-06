package tools

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/doITmagic/rag-code-mcp/internal/logger"
	"github.com/doITmagic/rag-code-mcp/internal/service/engine"
	"github.com/doITmagic/rag-code-mcp/pkg/parser"
	"github.com/doITmagic/rag-code-mcp/pkg/telemetry"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type ReadFileContextTool struct {
	engine *engine.Engine
}

func NewReadFileContextTool(eng *engine.Engine) *ReadFileContextTool {
	return &ReadFileContextTool{
		engine: eng,
	}
}

func (t *ReadFileContextTool) Name() string { return "rag_read_file_context" }
func (t *ReadFileContextTool) Description() string {
	return "Read specific lines from a file with surrounding smart AST context. " +
		"Requires an exact file path and line number."
}

type ReadFileContextInput struct {
	FilePath     string `json:"file_path"`
	LineNumber   int    `json:"line_number,omitempty"`
	StartLine    int    `json:"start_line,omitempty"`
	EndLine      int    `json:"end_line,omitempty"`
	ContextLines int    `json:"context_lines,omitempty"`
}

func (t *ReadFileContextTool) Register(server *mcp.Server) {
	mcp.AddTool(server, &mcp.Tool{
		Name:        t.Name(),
		Description: t.Description(),
	}, func(ctx context.Context, req *mcp.CallToolRequest, input ReadFileContextInput) (*mcp.CallToolResult, any, error) {
		args := map[string]interface{}{
			"file_path":     input.FilePath,
			"line_number":   input.LineNumber,
			"start_line":    input.StartLine,
			"end_line":      input.EndLine,
			"context_lines": input.ContextLines,
		}

		logger.Instance.Debug("rag_read_file_context invoked with params: %+v", args)

		start := time.Now()
		result, err := t.Execute(ctx, args)
		if err != nil {
			logger.Instance.Error("rag_read_file_context failed (%v): %v", time.Since(start), err)
			res := &mcp.CallToolResult{}
			res.SetError(err)
			return res, nil, nil
		}

		logger.Instance.Info("rag_read_file_context completed in %v", time.Since(start))
		logger.Instance.Debug("rag_read_file_context result size (bytes): %d", len(result))
		return &mcp.CallToolResult{
			Content: []mcp.Content{&mcp.TextContent{Text: result}},
		}, nil, nil
	})
}

func (t *ReadFileContextTool) Execute(ctx context.Context, args map[string]interface{}) (string, error) {
	filePath, _ := args["file_path"].(string)
	if filePath == "" {
		resp := ToolResponse{Status: "error", Error: "file_path parameter is required"}
		return resp.JSON()
	}

	wctx, err := t.engine.DetectContext(ctx, filePath)
	if err != nil {
		resp := ToolResponse{Status: "error", Error: fmt.Sprintf("failed to detect workspace for file: %v", err)}
		return resp.JSON()
	}

	absPath, err := filepath.Abs(filePath)
	if err != nil {
		resp := ToolResponse{Status: "error", Error: fmt.Sprintf("invalid file path: %v", err)}
		return resp.JSON()
	}

	// Make sure the file actually exists
	if _, err := os.Stat(absPath); os.IsNotExist(err) {
		resp := ToolResponse{Status: "error", Error: fmt.Sprintf("file not found: %s", absPath)}
		return resp.JSON()
	}

	// Normalize input boundaries
	lineNum, _ := parseLineArg(args["line_number"])
	startLine, _ := parseLineArg(args["start_line"])
	endLine, _ := parseLineArg(args["end_line"])

	if lineNum > 0 {
		startLine = lineNum
		endLine = lineNum
	}
	if startLine == 0 {
		startLine = 1
	}
	if endLine == 0 || endLine < startLine {
		endLine = startLine
	}

	ctxLines, _ := parseLineArg(args["context_lines"])
	if ctxLines == 0 {
		ctxLines = 5 // default fallback context
	}

	// Size protection measures
	if ctxLines > 50 {
		ctxLines = 50
	}
	const maxLinesLimit = 300
	if endLine-startLine > maxLinesLimit {
		endLine = startLine + maxLinesLimit
	}

	// Try AST-based context
	if result, found := t.tryASTContext(ctx, absPath, startLine, endLine); found {
		return t.buildResponse(wctx, result), nil
	}

	// Fallback to naive streaming
	result, err := t.streamLines(absPath, startLine, endLine, ctxLines)
	if err != nil {
		resp := ToolResponse{Status: "error", Error: fmt.Sprintf("failed to read file lines: %v", err)}
		return resp.JSON()
	}

	return t.buildResponse(wctx, result), nil
}

type CodeContextResult struct {
	FilePath    string            `json:"file_path"`
	Language    string            `json:"language,omitempty"`
	ContextType string            `json:"context_type"` // "ast" or "naive"
	StartLine   int               `json:"start_line"`
	EndLine     int               `json:"end_line"`
	SymbolName  string            `json:"symbol_name,omitempty"`
	SymbolType  string            `json:"symbol_type,omitempty"`
	CodeSnippet string            `json:"code_snippet"`
	Relations   []parser.Relation `json:"relations,omitempty"`
}

func parseLineArg(val interface{}) (int, bool) {
	switch v := val.(type) {
	case float64:
		return int(v), true
	case int:
		return v, true
	case int64:
		return int(v), true
	}
	return 0, false
}

// tryASTContext attempts to resolve the smallest logical block enclosing the lines
func (t *ReadFileContextTool) tryASTContext(ctx context.Context, path string, start, end int) (CodeContextResult, bool) {
	var result CodeContextResult
	analyzer := parser.GetByFile(path)
	if analyzer == nil {
		logger.Instance.Debug("Skipping AST context: no analyzer found for %s", path)
		return result, false
	}

	res, err := analyzer.Analyze(ctx, path)
	if err != nil || res == nil || len(res.Symbols) == 0 {
		logger.Instance.Debug("Skipping AST context: could not parse file %s or no symbols found", path)
		return result, false
	}

	logger.Instance.Debug("AST scan found %d symbols to analyze", len(res.Symbols))

	var best *parser.Symbol
	for i := range res.Symbols {
		sym := &res.Symbols[i]
		// Filter by exact file path (as Go parses entire packages at times)
		if sym.FilePath != "" && filepath.Base(sym.FilePath) != filepath.Base(path) {
			continue
		}
		// Check intersection logic
		if sym.StartLine <= start && sym.EndLine >= end {
			if best == nil {
				best = sym
			} else {
				// pick the tighter/nested symbol
				if (sym.EndLine - sym.StartLine) < (best.EndLine - best.StartLine) {
					best = sym
				}
			}
		}
	}

	if best != nil && best.Content != "" {
		if (best.EndLine - best.StartLine) > 500 {
			return result, false
		}

		// Prepend line numbers and mark exact targets
		lines := strings.Split(best.Content, "\n")
		var builder strings.Builder
		currentLine := best.StartLine
		for i, line := range lines {
			if i == len(lines)-1 && line == "" {
				break
			}
			marker := "   │ "
			if currentLine >= start && currentLine <= end {
				marker = "-> ┃ "
			}
			builder.WriteString(fmt.Sprintf("%4d %s%s\n", currentLine, marker, line))
			currentLine++
		}

		result = CodeContextResult{
			FilePath:    path,
			Language:    best.Language,
			ContextType: "ast",
			StartLine:   best.StartLine,
			EndLine:     best.EndLine,
			SymbolName:  best.Name,
			SymbolType:  string(best.Type),
			CodeSnippet: builder.String(),
			Relations:   best.Relations,
		}
		return result, true
	}

	return result, false
}

// streamLines reads only the necessary fallback lines efficiently
func (t *ReadFileContextTool) streamLines(path string, start, end, ctxLines int) (CodeContextResult, error) {
	logger.Instance.Debug("Executing streamLines fallback for file: %s", path)

	var result CodeContextResult
	f, err := os.Open(path)
	if err != nil {
		return result, err
	}
	defer f.Close()

	// Check if potentially binary (read first 512 bytes)
	buf := make([]byte, 512)
	n, _ := f.Read(buf)
	for i := 0; i < n; i++ {
		if buf[i] == 0 {
			return result, fmt.Errorf("file appears to be binary, cannot read as text")
		}
	}
	// Seek back to start
	if _, err := f.Seek(0, 0); err != nil {
		return result, fmt.Errorf("failed to seek file: %w", err)
	}

	minLine := start - ctxLines
	if minLine < 1 {
		minLine = 1
	}
	maxLine := end + ctxLines

	scanner := bufio.NewScanner(f)
	currentLine := 1
	var builder strings.Builder

	for scanner.Scan() {
		if currentLine >= minLine && currentLine <= maxLine {
			// Add a simple marker for the actual targeted lines
			marker := "   │ "
			if currentLine >= start && currentLine <= end {
				marker = "-> ┃ "
			}
			builder.WriteString(fmt.Sprintf("%4d %s%s\n", currentLine, marker, scanner.Text()))
		}
		if currentLine >= maxLine {
			break
		}
		currentLine++
	}

	if err := scanner.Err(); err != nil {
		return result, err
	}

	result = CodeContextResult{
		FilePath:    path,
		ContextType: "naive",
		StartLine:   minLine,
		EndLine:     maxLine,
		CodeSnippet: builder.String(),
	}
	return result, nil
}

func (t *ReadFileContextTool) buildResponse(wctx *engine.WorkspaceContext, res CodeContextResult) string {
	resp := ToolResponse{
		Status:  "success",
		Data:    res,
		Message: fmt.Sprintf("Extracted %s context for lines %d-%d from %s", res.ContextType, res.StartLine, res.EndLine, res.FilePath),
	}
	if wctx != nil {
		resp.Context = ContextMetadata{
			WorkspaceRoot:    wctx.Root,
			DetectionSource:  wctx.DetectionSource,
			IndexingProgress: BuildIndexingProgress(t.engine, wctx.ID, wctx.Root),
		}
	}

	baselineBytes := int64(0)
	if info, err := os.Stat(res.FilePath); err == nil {
		baselineBytes = info.Size()
	}
	// Measure extracted snippet relative to baseline file length
	actualBytes := int64(len(res.CodeSnippet))
	resp.Context.Telemetry = telemetry.CalculateSavings(baselineBytes, actualBytes)

	jsonStr, err := resp.JSON()
	if err != nil {
		return `{"status":"error","error":"JSON marshalling failed"}`
	}
	return jsonStr
}

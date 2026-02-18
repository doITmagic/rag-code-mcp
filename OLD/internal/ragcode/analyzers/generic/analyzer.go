package generic

import (
	"bufio"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/doITmagic/rag-code-mcp/internal/codetypes"
)

// CodeAnalyzer implements a simple line-based chunker for languages
// that don't have a specialized AST-based analyzer yet.
type CodeAnalyzer struct {
	Language  string
	ChunkSize int // Max lines per chunk, default 100
}

// NewCodeAnalyzer creates a new generic analyzer instance.
func NewCodeAnalyzer(lang string) *CodeAnalyzer {
	return &CodeAnalyzer{
		Language:  lang,
		ChunkSize: 100,
	}
}

// AnalyzePaths walks provided paths and extracts basic chunks from files.
func (a *CodeAnalyzer) AnalyzePaths(paths []string) ([]codetypes.CodeChunk, error) {
	var chunks []codetypes.CodeChunk

	for _, root := range paths {
		err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() {
				if a.shouldSkipDir(path, root) {
					return filepath.SkipDir
				}
				return nil
			}

			// We assume the caller filter files by language before calling the analyzer
			// or the analyzer is language-specific.
			fileChunks, ferr := a.analyzeFile(path)
			if ferr != nil {
				// Log error but continue
				return nil
			}
			chunks = append(chunks, fileChunks...)
			return nil
		})
		if err != nil {
			return nil, err
		}
	}

	return chunks, nil
}

func (a *CodeAnalyzer) analyzeFile(path string) ([]codetypes.CodeChunk, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	var chunks []codetypes.CodeChunk
	var currentLines []string
	startLine := 1
	lineNum := 0

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		lineNum++
		currentLines = append(currentLines, scanner.Text())

		if len(currentLines) >= a.ChunkSize {
			chunks = append(chunks, a.createChunk(path, currentLines, startLine, lineNum))
			currentLines = nil
			startLine = lineNum + 1
		}
	}

	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("error reading file %s: %w", path, err)
	}

	if len(currentLines) > 0 {
		chunks = append(chunks, a.createChunk(path, currentLines, startLine, lineNum))
	}

	return chunks, nil
}

func (a *CodeAnalyzer) createChunk(path string, lines []string, start, end int) codetypes.CodeChunk {
	content := strings.Join(lines, "\n")
	base := filepath.Base(path)
	name := fmt.Sprintf("%s:%d-%d", base, start, end)

	return codetypes.CodeChunk{
		Type:      "file_chunk",
		Name:      name,
		Language:  a.Language,
		FilePath:  path,
		StartLine: start,
		EndLine:   end,
		Signature: fmt.Sprintf("// File: %s, Lines: %d-%d", base, start, end),
		Code:      content,
		Metadata: map[string]any{
			"is_generic": true,
		},
	}
}

func (a *CodeAnalyzer) shouldSkipDir(path, root string) bool {
	if path == root {
		return false
	}
	base := filepath.Base(path)
	if strings.HasPrefix(base, ".") {
		return true
	}
	switch base {
	case "node_modules", "vendor", "dist", "build":
		return true
	default:
		return false
	}
}

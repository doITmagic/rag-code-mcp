package tools

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/doITmagic/rag-code-mcp/internal/codetypes"
	"github.com/doITmagic/rag-code-mcp/internal/memory"
)

// readFileLines reads specific lines from a file
func readFileLines(filePath string, startLine, endLine int) (string, error) {
	content, err := os.ReadFile(filePath)
	if err != nil {
		return "", err
	}

	lines := strings.Split(string(content), "\n")
	if startLine < 1 || endLine > len(lines) || startLine > endLine {
		return "", fmt.Errorf("invalid line range: %d-%d (file has %d lines)", startLine, endLine, len(lines))
	}

	// Lines are 1-indexed
	selectedLines := lines[startLine-1 : endLine]
	return strings.Join(selectedLines, "\n"), nil
}

// buildSymbolDescriptorsFromDocs converts memory.Document hits into a list of
// SymbolDescriptor values. When Content is a CodeChunk JSON, it is parsed and
// mapped; otherwise the raw content is stored as description/snippet.
func buildSymbolDescriptorsFromDocs(docs []memory.Document) []codetypes.SymbolDescriptor {
	out := make([]codetypes.SymbolDescriptor, 0, len(docs))
	for _, doc := range docs {
		var desc codetypes.SymbolDescriptor
		var chunk codetypes.CodeChunk
		if err := json.Unmarshal([]byte(doc.Content), &chunk); err == nil && chunk.Name != "" {
			desc.Language = chunk.Language
			desc.Kind = chunk.Type
			desc.Name = chunk.Name
			desc.Namespace = chunk.Package
			desc.Package = chunk.Package
			desc.Signature = chunk.Signature
			desc.Description = chunk.Docstring
			desc.Location = codetypes.SymbolLocation{
				FilePath:  chunk.FilePath,
				StartLine: chunk.StartLine,
				EndLine:   chunk.EndLine,
			}
			if chunk.Code != "" {
				if desc.Metadata == nil {
					desc.Metadata = make(map[string]any)
				}
				desc.Metadata["snippet"] = chunk.Code
			}
		} else {
			// Fallback: treat content as opaque text
			desc.Kind = "document"
			desc.Description = truncateString(doc.Content, 400)
		}

		// Merge underlying document metadata (scores, file, etc.)
		if doc.Metadata != nil {
			if desc.Metadata == nil {
				desc.Metadata = make(map[string]any)
			}
			for k, v := range doc.Metadata {
				desc.Metadata[k] = v
			}
		}

		out = append(out, desc)
	}
	return out
}

func truncateString(s string, max int) string {
	if len(s) <= max {
		return s
	}
	if max <= 0 {
		return ""
	}
	return s[:max]
}

// inferLanguageFromPath infers programming language from file path
func inferLanguageFromPath(filePath string) string {
	ext := strings.ToLower(filepath.Ext(filePath))

	switch ext {
	case ".go":
		return "go"
	case ".py":
		return "python"
	case ".js", ".ts", ".jsx", ".tsx", ".mjs":
		return "javascript"
	case ".php":
		return "php"
	case ".html", ".htm":
		return "html"
	case ".rs":
		return "rust"
	case ".java", ".kt":
		return "java"
	case ".rb":
		return "ruby"
	case ".swift":
		return "swift"
	case ".c", ".h", ".cpp", ".hpp", ".cc", ".cxx":
		return "cpp"
	case ".cs":
		return "csharp"
	default:
		return ""
	}
}

// extractFilePathFromParams extracts file path from common parameter names
func extractFilePathFromParams(params map[string]interface{}) string {
	pathParams := []string{
		"file_path",
		"filePath",
		"path",
		"file",
		"source_file",
		"target_file",
	}

	for _, param := range pathParams {
		if value, ok := params[param]; ok {
			if path, ok := value.(string); ok && path != "" {
				return path
			}
		}
	}

	return ""
}

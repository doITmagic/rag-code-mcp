package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/doITmagic/rag-code-mcp/internal/codetypes"
	"github.com/doITmagic/rag-code-mcp/internal/llm"
	"github.com/doITmagic/rag-code-mcp/internal/memory"
	"github.com/doITmagic/rag-code-mcp/internal/workspace"
)

// FindImplementationsTool finds where a function/interface is used or implemented
type FindImplementationsTool struct {
	longTermMemory   memory.LongTermMemory
	embedder         llm.Provider
	workspaceManager *workspace.Manager
}

// NewFindImplementationsTool creates a new implementations finder tool
func NewFindImplementationsTool(ltm memory.LongTermMemory, embedder llm.Provider) *FindImplementationsTool {
	return &FindImplementationsTool{
		longTermMemory: ltm,
		embedder:       embedder,
	}
}

// SetWorkspaceManager sets the workspace manager for workspace-aware searching
func (t *FindImplementationsTool) SetWorkspaceManager(wm *workspace.Manager) {
	t.workspaceManager = wm
}

func (t *FindImplementationsTool) Name() string {
	return "find_implementations"
}

func (t *FindImplementationsTool) Description() string {
	return "Find implementations, usages, or references to a function, method, or interface"
}

func (t *FindImplementationsTool) Execute(ctx context.Context, args map[string]interface{}) (string, error) {
	symbolName, ok := args["symbol_name"].(string)
	if !ok || symbolName == "" {
		return "", fmt.Errorf("symbol_name is required")
	}

	// Optional package filter
	packagePath := ""
	if pkg, ok := args["package"].(string); ok {
		packagePath = pkg
	}

	// file_path is required for workspace detection
	filePath := extractFilePathFromParams(args)
	if filePath == "" {
		return "", fmt.Errorf("file_path parameter is required for find_implementations. Please provide a file path from your workspace")
	}

	// Try workspace detection if workspace manager is available
	var searchMemory memory.LongTermMemory
	var workspacePath string
	var collectionName string

	if t.workspaceManager != nil {
		workspaceInfo, err := t.workspaceManager.DetectWorkspace(args)
		if err == nil && workspaceInfo != nil {
			workspacePath = workspaceInfo.Root

			// Detect language from file path or use first detected language
			language := inferLanguageFromPath(filePath)
			if language == "" && len(workspaceInfo.Languages) > 0 {
				language = workspaceInfo.Languages[0]
			}
			if language == "" {
				language = workspaceInfo.ProjectType
			}

			collectionName = workspaceInfo.CollectionNameForLanguage(language)
			mem, err := t.workspaceManager.GetMemoryForWorkspaceLanguage(ctx, workspaceInfo, language)
			if err == nil && mem != nil {
				// Check if indexing is in progress
				indexKey := workspaceInfo.ID + "-" + language
				if t.workspaceManager.IsIndexing(indexKey) {
					return fmt.Sprintf("⏳ Workspace '%s' language '%s' is currently being indexed in the background.\n"+
						"Please try again in a few moments.\n"+
						"Workspace: %s\n"+
						"Language: %s\n"+
						"Collection: %s",
						workspaceInfo.Root, language, workspaceInfo.Root, language, collectionName), nil
				}

				// Check if collection exists before proceeding
				if msg, err := CheckCollectionStatus(ctx, mem, collectionName, workspacePath); err != nil || msg != "" {
					if err != nil {
						return "", err
					}
					return msg, nil
				}

				searchMemory = mem
			}
		}
	}

	// Use workspace-specific memory or fall back to default
	if searchMemory == nil {
		searchMemory = t.longTermMemory
	}

	if searchMemory == nil {
		return "", fmt.Errorf("no long-term memory configured")
	}

	// Search for usages/implementations
	// We search for code that might contain this symbol
	query := fmt.Sprintf("%s implementation usage", symbolName)
	if packagePath != "" {
		query = fmt.Sprintf("%s in package %s", query, packagePath)
	}

	queryEmbedding, err := t.embedder.Embed(ctx, query)
	if err != nil {
		return "", fmt.Errorf("failed to generate query embedding: %w", err)
	}

	// Prefer SearchCodeOnly to exclude markdown documentation
	type CodeSearcher interface {
		SearchCodeOnly(ctx context.Context, query []float64, limit int) ([]memory.Document, error)
	}

	var results []memory.Document
	if codeSearcher, ok := searchMemory.(CodeSearcher); ok {
		results, err = codeSearcher.SearchCodeOnly(ctx, queryEmbedding, 50)
	} else {
		results, err = searchMemory.Search(ctx, queryEmbedding, 50)
	}
	if err != nil {
		return "", fmt.Errorf("search failed: %w", err)
	}

	// Check if workspace search returned no results (might be empty collection)
	if len(results) == 0 && workspacePath != "" && collectionName != "" {
		if msg, err := CheckSearchResults(0, collectionName, workspacePath); err != nil || msg != "" {
			if err != nil {
				return "", err
			}
			return msg, nil
		}
	}

	// Find chunks that contain the symbol in their code
	var implementations []Implementation
	seenKeys := make(map[string]bool)

	for _, result := range results {
		var chunk codetypes.CodeChunk
		if err := json.Unmarshal([]byte(result.Content), &chunk); err != nil {
			continue
		}

		// Skip if this IS the definition itself
		if chunk.Name == symbolName {
			continue
		}

		// Check if code contains the symbol
		if !strings.Contains(chunk.Code, symbolName) {
			continue
		}

		// Apply package filter if specified
		if packagePath != "" && !strings.Contains(chunk.Package, packagePath) {
			continue
		}

		key := fmt.Sprintf("%s:%s:%d", chunk.FilePath, chunk.Name, chunk.StartLine)
		if seenKeys[key] {
			continue
		}
		seenKeys[key] = true

		// Count occurrences
		occurrences := strings.Count(chunk.Code, symbolName)

		impl := Implementation{
			Name:        chunk.Name,
			Type:        chunk.Type,
			Package:     chunk.Package,
			FilePath:    chunk.FilePath,
			StartLine:   chunk.StartLine,
			EndLine:     chunk.EndLine,
			Occurrences: occurrences,
			Snippet:     extractSnippet(chunk.Code, symbolName, 2),
		}

		implementations = append(implementations, impl)
	}

	if len(implementations) == 0 {
		if workspacePath != "" {
			return fmt.Sprintf("🔍 No implementations or usages found for '%s' in workspace '%s'", symbolName, workspacePath), nil
		}
		return fmt.Sprintf("No implementations or usages found for '%s'", symbolName), nil
	}

	// Sort by number of occurrences (most used first)
	sort.Slice(implementations, func(i, j int) bool {
		return implementations[i].Occurrences > implementations[j].Occurrences
	})

	// Build response
	var response strings.Builder
	if workspacePath != "" {
		response.WriteString(fmt.Sprintf("# 🔍 Usages of `%s` in workspace '%s'\n\n", symbolName, workspacePath))
	} else {
		response.WriteString(fmt.Sprintf("# Usages of `%s`\n\n", symbolName))
	}
	response.WriteString(fmt.Sprintf("**Found in:** %d locations\n\n", len(implementations)))

	for i, impl := range implementations {
		if i >= 20 { // Limit to top 20
			response.WriteString(fmt.Sprintf("\n... and %d more\n", len(implementations)-i))
			break
		}

		response.WriteString(fmt.Sprintf("## %d. `%s` (%s)\n\n", i+1, impl.Name, impl.Type))
		response.WriteString(fmt.Sprintf("**Package:** %s\n", impl.Package))
		response.WriteString(fmt.Sprintf("**Location:** `%s:%d-%d`\n", impl.FilePath, impl.StartLine, impl.EndLine))
		response.WriteString(fmt.Sprintf("**Occurrences:** %d\n\n", impl.Occurrences))

		if impl.Snippet != "" {
			response.WriteString("**Code snippet:**\n```go\n")
			response.WriteString(impl.Snippet)
			response.WriteString("\n```\n\n")
		}
	}

	return response.String(), nil
}

type Implementation struct {
	Name        string
	Type        string
	Package     string
	FilePath    string
	StartLine   int
	EndLine     int
	Occurrences int
	Snippet     string
}

// extractSnippet extracts a few lines around the first occurrence of symbol
func extractSnippet(code, symbol string, contextLines int) string {
	lines := strings.Split(code, "\n")

	// Find first occurrence
	firstLine := -1
	for i, line := range lines {
		if strings.Contains(line, symbol) {
			firstLine = i
			break
		}
	}

	if firstLine == -1 {
		return ""
	}

	// Extract context
	start := firstLine - contextLines
	if start < 0 {
		start = 0
	}

	end := firstLine + contextLines + 1
	if end > len(lines) {
		end = len(lines)
	}

	snippet := lines[start:end]
	return strings.Join(snippet, "\n")
}

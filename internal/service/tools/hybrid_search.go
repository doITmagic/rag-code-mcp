package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strings"

	"github.com/doITmagic/rag-code-mcp/internal/memory"
	"github.com/doITmagic/rag-code-mcp/internal/workspace"
	"github.com/doITmagic/rag-code-mcp/pkg/llm"
)

// HybridSearchTool combines basic lexical matching with semantic scoring from vector search
// to get more relevant results when exact matches exist.
type HybridSearchTool struct {
	memory           memory.LongTermMemory
	embedder         llm.Provider
	workspaceManager *workspace.Manager
	searchLimit      int
}

// NewHybridSearchTool creates a new hybrid search tool. Accepts the main code memory and embedding provider.
func NewHybridSearchTool(mem memory.LongTermMemory, embedder llm.Provider) *HybridSearchTool {
	return &HybridSearchTool{
		memory:      mem,
		embedder:    embedder,
		searchLimit: 5,
	}
}

// SetSearchLimit sets the default search limit
func (t *HybridSearchTool) SetSearchLimit(limit int) {
	if limit > 0 {
		t.searchLimit = limit
	}
}

// SetWorkspaceManager sets the workspace manager for workspace-aware searching
func (t *HybridSearchTool) SetWorkspaceManager(wm *workspace.Manager) {
	t.workspaceManager = wm
}

// Name returns the MCP tool name.
func (t *HybridSearchTool) Name() string { return "rag_hybrid_search" }

// Description provides a description for the tool.
func (t *HybridSearchTool) Description() string {
	return "Combined keyword + semantic search - use ONLY when you need EXACT matches (variable names, error messages, specific identifiers). Returns complete source code with file path, line numbers, and metadata. Use rag_search_code FIRST for general exploration; use this when rag_search_code misses exact terms. Supports Go, PHP, Python, HTML. IMPORTANT: Always provide the 'file_path' of the file you are currently working on for better context detection.\nExample: { \"query\": \"func ProcessOrder\", \"file_path\": \"/path/to/project/orders.go\" }"
}

type hybridScore struct {
	doc      memory.Document
	combined float64
	semantic float64
	lexical  float64
}

// Execute runs the hybrid search.
func (t *HybridSearchTool) Execute(ctx context.Context, params map[string]interface{}) (string, error) {
	query, ok := params["query"].(string)
	if !ok || strings.TrimSpace(query) == "" {
		return "", fmt.Errorf("query parameter is required")
	}

	limit := t.searchLimit
	if v, ok := params["limit"].(float64); ok {
		limit = int(v)
	} else if v, ok := params["limit"].(int); ok {
		limit = v
	}
	if limit <= 0 {
		limit = t.searchLimit
	}

	// Optional output format: json (default) or markdown
	outputFormat := "json"
	if of, ok := params["output_format"].(string); ok && of != "" {
		outputFormat = strings.ToLower(of)
	}

	// Try workspace detection
	var workspaceMem memory.LongTermMemory
	var workspacePath string
	var collectionName string

	var workspaceInfo *workspace.Info
	if t.workspaceManager != nil {
		var err error
		workspaceInfo, err = DetectAndRegisterWorkspace(t.workspaceManager, params)
		if err != nil {
			return HandleWorkspaceDetectionError(err, "")
		}

		if workspaceInfo != nil {
			workspacePath = workspaceInfo.Root

			// Detect language from file path or use first detected language
			language := ""
			filePath := extractFilePathFromParams(params)
			if filePath != "" {
				language = inferLanguageFromPath(filePath)
			}
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
					return AttachAIWarning(fmt.Sprintf("⏳ Workspace '%s' language '%s' is currently being indexed in the background.\n"+
						"Please try again in a few moments.\n"+
						"Workspace: %s\n"+
						"Language: %s\n"+
						"Collection: %s",
						workspaceInfo.Root, language, workspaceInfo.Root, language, collectionName), workspaceInfo), nil
				}

				// Check if collection exists before proceeding
				if msg, err := CheckCollectionStatus(ctx, mem, collectionName, workspacePath); err != nil || msg != "" {
					if err != nil {
						return "", err
					}
					return AttachAIWarning(msg, workspaceInfo), nil
				}

				workspaceMem = mem
			}
		}
	}

	// Use workspace-specific memory or fallback to default
	searchMemory := workspaceMem
	if searchMemory == nil {
		searchMemory = t.memory
	}

	if searchMemory == nil {
		return "", fmt.Errorf("no long-term memory configured for hybrid search")
	}

	// 1. Generate embedding for query
	queryEmbedding, err := t.embedder.Embed(ctx, query)
	if err != nil {
		return "", fmt.Errorf("failed to generate query embedding: %w", err)
	}

	// 2. Gather semantic candidates (more than the limit to allow lexical filtering)
	// Prefer SearchCodeOnly to exclude markdown documentation

	// Optional: include documentation in results
	includeDocs := false
	if inc, ok := params["include_docs"].(bool); ok {
		includeDocs = inc
	}

	fetchLimit := int(math.Max(float64(limit*5), 10))
	var docs []memory.Document

	// If include_docs is true, use generic Search (returns all types)
	// If include_docs is false (default), try to use SearchCodeOnly
	if includeDocs {
		docs, err = searchMemory.Search(ctx, queryEmbedding, fetchLimit)
	} else if codeSearcher, ok := searchMemory.(CodeSearcher); ok {
		docs, err = codeSearcher.SearchCodeOnly(ctx, queryEmbedding, fetchLimit)
	} else {
		// Fallback if SearchCodeOnly not implemented
		docs, err = searchMemory.Search(ctx, queryEmbedding, fetchLimit)
	}
	if err != nil {
		errLower := strings.ToLower(err.Error())
		if strings.Contains(errLower, "dimension mismatch") ||
			(strings.Contains(errLower, "expected:") && (strings.Contains(errLower, "vector") || strings.Contains(errLower, "dimension"))) {
			return "", fmt.Errorf("❌ Vector dimension mismatch in collection '%s'.\n\n"+
				"This usually happens when you change the embedding model (e.g., from nomic to mxbai).\n"+
				"To fix this, please delete and recreate the index by running the 'index_workspace' tool with 'recreate': true.\n\n"+
				"Error details: %v",
				collectionName, err)
		}
		return "", fmt.Errorf("search failed: %w", err)
	}

	if len(docs) == 0 {
		// Check if this is a workspace search with empty collection
		if workspaceMem != nil && collectionName != "" {
			if msg, err := CheckSearchResults(0, collectionName, workspacePath); err != nil || msg != "" {
				if err != nil {
					return "", err
				}
				return AttachAIWarning(msg, workspaceInfo), nil
			}
		}

		if outputFormat == "markdown" {
			if workspaceMem != nil {
				return AttachAIWarning(fmt.Sprintf("No relevant code found in workspace '%s'.", workspacePath), workspaceInfo), nil
			}
			return AttachAIWarning("No relevant code found.", workspaceInfo), nil
		}
		return AttachAIWarning("[]", workspaceInfo), nil
	}

	lowerQuery := strings.ToLower(query)
	tokens := filterTokens(strings.Fields(lowerQuery))

	maxLexical := 0.0
	matches := make([]hybridScore, 0, len(docs))

	for _, doc := range docs {
		content := strings.ToLower(fmt.Sprintf("%v", doc.Content))
		lexicalScore := lexicalMatchScore(content, tokens)
		semanticScore := 0.0
		if sc, ok := doc.Metadata["score"].(float64); ok {
			semanticScore = sc
		}

		if lexicalScore > 0 {
			if lexicalScore > maxLexical {
				maxLexical = lexicalScore
			}
			matches = append(matches, hybridScore{doc: doc, semantic: semanticScore, lexical: lexicalScore})
		}
	}

	// If no lexical matches, fall back to top semantic results
	if len(matches) == 0 {
		topSemantic := docs
		if len(topSemantic) > limit {
			topSemantic = topSemantic[:limit]
		}
		if outputFormat == "markdown" {
			return AttachAIWarning(formatHybridResults(topSemantic, false, workspaceMem != nil, workspacePath), workspaceInfo), nil
		}
		descriptors := buildSymbolDescriptorsFromDocs(topSemantic)
		data, err := json.MarshalIndent(descriptors, "", "  ")
		if err != nil {
			return "", fmt.Errorf("failed to marshal rag_hybrid_search results: %w", err)
		}
		return AttachAIWarning(string(data), workspaceInfo), nil
	}

	// Combine scores (60% semantic + 40% normalized lexical)
	for i := range matches {
		lexicalNorm := 0.0
		if maxLexical > 0 {
			lexicalNorm = matches[i].lexical / maxLexical
		}
		matches[i].combined = 0.6*matches[i].semantic + 0.4*lexicalNorm
	}

	sort.Slice(matches, func(i, j int) bool {
		return matches[i].combined > matches[j].combined
	})

	if len(matches) > limit {
		matches = matches[:limit]
	}

	finalDocs := make([]memory.Document, 0, len(matches))
	for _, res := range matches {
		// Attach combined scores for transparency
		if res.doc.Metadata == nil {
			res.doc.Metadata = make(map[string]interface{})
		}
		res.doc.Metadata["hybrid_score"] = res.combined
		res.doc.Metadata["semantic_score"] = res.semantic
		res.doc.Metadata["lexical_score"] = res.lexical
		finalDocs = append(finalDocs, res.doc)
	}

	if outputFormat == "markdown" {
		return AttachAIWarning(formatHybridResults(finalDocs, true, workspaceMem != nil, workspacePath), workspaceInfo), nil
	}

	descriptors := buildSymbolDescriptorsFromDocs(finalDocs)
	// Add workspace info to metadata of each descriptor for AI verification
	for i := range descriptors {
		if descriptors[i].Metadata == nil {
			descriptors[i].Metadata = make(map[string]any)
		}
		if workspacePath != "" {
			descriptors[i].Metadata["workspace_root"] = workspacePath
		}
	}

	data, err := json.MarshalIndent(descriptors, "", "  ")
	if err != nil {
		return "", fmt.Errorf("failed to marshal rag_hybrid_search results: %w", err)
	}
	return AttachAIWarning(string(data), workspaceInfo), nil
}

func filterTokens(tokens []string) []string {
	filtered := make([]string, 0, len(tokens))
	for _, tok := range tokens {
		tok = strings.TrimSpace(tok)
		if tok != "" {
			filtered = append(filtered, tok)
		}
	}
	return filtered
}

func lexicalMatchScore(content string, tokens []string) float64 {
	if len(tokens) == 0 {
		return 0
	}
	score := 0.0
	for _, token := range tokens {
		score += float64(strings.Count(content, token))
	}
	return score
}

func formatHybridResults(docs []memory.Document, includeScores bool, isWorkspaceSearch bool, workspacePath string) string {
	if len(docs) == 0 {
		if isWorkspaceSearch {
			return fmt.Sprintf("No relevant code found in workspace '%s'.", workspacePath)
		}
		return "No relevant code found."
	}
	var sb strings.Builder
	if isWorkspaceSearch {
		sb.WriteString(fmt.Sprintf("🔍 Hybrid search found %d snippet(s) in workspace '%s':\n\n", len(docs), workspacePath))
	} else {
		sb.WriteString(fmt.Sprintf("Hybrid search found %d snippet(s):\n\n", len(docs)))
	}
	for i, doc := range docs {
		if includeScores {
			sb.WriteString(fmt.Sprintf("--- Result %d (hybrid %.4f | semantic %.4f | lexical %.1f) ---\n",
				i+1,
				getFloat(doc.Metadata["hybrid_score"]),
				getFloat(doc.Metadata["semantic_score"]),
				getFloat(doc.Metadata["lexical_score"])))
		} else {
			sb.WriteString(fmt.Sprintf("--- Result %d ---\n", i+1))
		}
		sb.WriteString(fmt.Sprintf("%v\n\n", doc.Content))
	}
	return sb.String()
}

func getFloat(val interface{}) float64 {
	if f, ok := val.(float64); ok {
		return f
	}
	if f, ok := val.(float32); ok {
		return float64(f)
	}
	return 0
}

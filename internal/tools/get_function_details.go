package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"unicode"

	"github.com/doITmagic/rag-code-mcp/internal/codetypes"
	"github.com/doITmagic/rag-code-mcp/internal/llm"
	"github.com/doITmagic/rag-code-mcp/internal/memory"
	"github.com/doITmagic/rag-code-mcp/internal/ragcode/analyzers/php"
	laravel "github.com/doITmagic/rag-code-mcp/internal/ragcode/analyzers/php/laravel"
	"github.com/doITmagic/rag-code-mcp/internal/workspace"
)

// GetFunctionDetailsTool returns complete details about a function or method
type GetFunctionDetailsTool struct {
	longTermMemory   memory.LongTermMemory
	embedder         llm.Provider
	workspaceManager *workspace.Manager
}

// NewGetFunctionDetailsTool creates a new function details tool
func NewGetFunctionDetailsTool(ltm memory.LongTermMemory, embedder llm.Provider) *GetFunctionDetailsTool {
	return &GetFunctionDetailsTool{
		longTermMemory: ltm,
		embedder:       embedder,
	}
}

// SetWorkspaceManager sets the workspace manager for workspace-aware searching
func (t *GetFunctionDetailsTool) SetWorkspaceManager(wm *workspace.Manager) {
	t.workspaceManager = wm
}

func (t *GetFunctionDetailsTool) Name() string {
	return "get_function_details"
}

func (t *GetFunctionDetailsTool) Description() string {
	return "Get complete details about a function or method including signature, parameters, return values, and code body"
}

func (t *GetFunctionDetailsTool) Execute(ctx context.Context, args map[string]interface{}) (string, error) {
	functionName, ok := args["function_name"].(string)
	if !ok || functionName == "" {
		return "", fmt.Errorf("function_name is required")
	}

	// Optional package filter
	packagePath := ""
	if pkg, ok := args["package"].(string); ok {
		packagePath = pkg
	}

	// Optional output format: markdown (default) or json
	outputFormat := "markdown"
	if of, ok := args["output_format"].(string); ok && of != "" {
		outputFormat = strings.ToLower(of)
	}

	// file_path is required for workspace detection
	filePath := extractFilePathFromParams(args)
	if filePath == "" {
		return "", fmt.Errorf("file_path parameter is required for get_function_details. Please provide a file path from your workspace")
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

	// Search for the function in the vector database
	query := fmt.Sprintf("function %s definition", functionName)
	if packagePath != "" {
		query = fmt.Sprintf("function %s in package %s", functionName, packagePath)
	}

	// Generate query embedding
	queryEmbedding, err := t.embedder.Embed(ctx, query)
	if err != nil {
		return "", fmt.Errorf("failed to generate query embedding: %w", err)
	}

	// Use a larger search window to avoid missing definitions when usages dominate
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

	if len(results) == 0 {
		// Check if this is a workspace search with empty collection
		if workspacePath != "" && collectionName != "" {
			if msg, err := CheckSearchResults(0, collectionName, workspacePath); err != nil || msg != "" {
				if err != nil {
					return "", err
				}
				return msg, nil
			}
			return fmt.Sprintf("Function '%s' not found in workspace '%s'", functionName, workspacePath), nil
		}
		return fmt.Sprintf("Function '%s' not found", functionName), nil
	}

	// Find exact match
	var bestMatch *memory.Document
	for _, result := range results {
		var chunk codetypes.CodeChunk
		if err := json.Unmarshal([]byte(result.Content), &chunk); err != nil {
			continue
		}

		// Check if this is a function chunk
		if chunk.Type != "function" && chunk.Type != "method" {
			continue
		}

		// Check name match
		if chunk.Name != functionName {
			continue
		}

		// Check package match if specified
		if packagePath != "" && !strings.Contains(chunk.Package, packagePath) {
			continue
		}

		bestMatch = &result
		break
	}

	if bestMatch == nil {
		return fmt.Sprintf("Function '%s' not found (searched %d chunks)", functionName, len(results)), nil
	}

	var chunk codetypes.CodeChunk
	if err := json.Unmarshal([]byte(bestMatch.Content), &chunk); err != nil {
		return "", fmt.Errorf("failed to parse chunk: %w", err)
	}

	// Read actual code body from file
	codeBody := chunk.Code
	if codeBody == "" && chunk.FilePath != "" && chunk.StartLine > 0 && chunk.EndLine > 0 {
		body, err := readFileLines(chunk.FilePath, chunk.StartLine, chunk.EndLine)
		if err == nil {
			codeBody = body
		}
	}

	// PHP: use PHP analyzer directly on the source file to build a rich function/method view
	if chunk.Language == "php" {
		return t.buildPHPFunctionResponse(&chunk, codeBody, outputFormat)
	}

	// Default (Go and others): optional JSON output, otherwise keep existing
	// markdown behaviour using the CodeChunk data.
	if strings.ToLower(outputFormat) == "json" {
		// Go: enrich descriptor using metadata from CodeChunk (receiver,
		// parameters, returns), so AI are-aware of full function shape.
		var desc codetypes.FunctionDescriptor
		if chunk.Language == "go" {
			desc = buildGoFunctionDescriptor(&chunk, codeBody)
		} else {
			desc = codetypes.FunctionDescriptor{
				Language:    chunk.Language,
				Kind:        chunk.Type,
				Name:        chunk.Name,
				Namespace:   chunk.Package,
				Signature:   chunk.Signature,
				Description: chunk.Docstring,
				Location: codetypes.SymbolLocation{
					FilePath:  chunk.FilePath,
					StartLine: chunk.StartLine,
					EndLine:   chunk.EndLine,
				},
				Code: codeBody,
			}
		}
		data, err := json.MarshalIndent(desc, "", "  ")
		if err != nil {
			return "", fmt.Errorf("failed to marshal Go function descriptor: %w", err)
		}
		return string(data), nil
	}

	// Markdown behaviour (Go and others)
	var response strings.Builder
	response.WriteString(fmt.Sprintf("# %s\n\n", chunk.Name))
	response.WriteString(fmt.Sprintf("**Type:** %s\n", chunk.Type))
	response.WriteString(fmt.Sprintf("**Package:** %s\n", chunk.Package))
	response.WriteString(fmt.Sprintf("**Signature:** `%s`\n\n", chunk.Signature))

	if chunk.Docstring != "" {
		response.WriteString(fmt.Sprintf("**Description:**\n%s\n\n", chunk.Docstring))
	}

	response.WriteString(fmt.Sprintf("**Location:** `%s:%d-%d`\n\n", chunk.FilePath, chunk.StartLine, chunk.EndLine))

	if codeBody != "" {
		response.WriteString("**Code:**\n```go\n")
		response.WriteString(codeBody)
		response.WriteString("\n```\n")
	}

	return response.String(), nil
}

// buildGoFunctionDescriptor constructs a richer FunctionDescriptor for Go
// functions/methods using CodeChunk metadata produced by the Go analyzer
// (receiver, parameters, returns).
func buildGoFunctionDescriptor(chunk *codetypes.CodeChunk, codeBody string) codetypes.FunctionDescriptor {
	fd := codetypes.FunctionDescriptor{
		Language:    chunk.Language,
		Kind:        chunk.Type,
		Name:        chunk.Name,
		Namespace:   chunk.Package,
		Signature:   chunk.Signature,
		Description: chunk.Docstring,
		Location: codetypes.SymbolLocation{
			FilePath:  chunk.FilePath,
			StartLine: chunk.StartLine,
			EndLine:   chunk.EndLine,
		},
		Code: codeBody,
	}

	// Go-specific enrichment based on analyzer metadata
	if strings.ToLower(chunk.Language) != "go" {
		return fd
	}

	// Visibility: exported vs unexported symbol
	if chunk.Name != "" {
		first, _ := utf8DecodeRuneInString(chunk.Name)
		if unicode.IsUpper(first) {
			fd.Visibility = "exported"
		}
	}

	if chunk.Metadata != nil {
		// Receiver / method kind
		if recv, ok := chunk.Metadata["receiver"].(string); ok && recv != "" {
			fd.Receiver = recv
			fd.Kind = "method"
		}

		// Parameters
		if rawParams, ok := chunk.Metadata["params"]; ok {
			switch v := rawParams.(type) {
			case []codetypes.ParamInfo:
				for _, p := range v {
					fd.Parameters = append(fd.Parameters, codetypes.ParamDescriptor{
						Name: p.Name,
						Type: p.Type,
					})
				}
			case []interface{}:
				for _, item := range v {
					if m, ok := item.(map[string]interface{}); ok {
						name, _ := m["name"].(string)
						typ, _ := m["type"].(string)
						fd.Parameters = append(fd.Parameters, codetypes.ParamDescriptor{
							Name: name,
							Type: typ,
						})
					}
				}
			}
		}

		// Returns
		if rawReturns, ok := chunk.Metadata["returns"]; ok {
			switch v := rawReturns.(type) {
			case []codetypes.ReturnInfo:
				for _, r := range v {
					fd.Returns = append(fd.Returns, codetypes.ReturnDescriptor{
						Type:        r.Type,
						Description: r.Description,
						SourceHint:  "type_hint",
					})
				}
			case []interface{}:
				for _, item := range v {
					if m, ok := item.(map[string]interface{}); ok {
						typ, _ := m["type"].(string)
						desc, _ := m["description"].(string)
						fd.Returns = append(fd.Returns, codetypes.ReturnDescriptor{
							Type:        typ,
							Description: desc,
							SourceHint:  "type_hint",
						})
					}
				}
			}
		}
	}

	return fd
}

// utf8DecodeRuneInString is a tiny helper so we don't import the entire utf8
// package interface here.
func utf8DecodeRuneInString(s string) (rune, int) {
	if s == "" {
		return 0, 0
	}
	r := []rune(s)
	return r[0], len(string(r[0]))
}

// buildPHPFunctionResponse builds a rich view for a PHP function or method
// using the PHP CodeAnalyzer data (parameters, returns, flags, location).
//
// outputFormat can be "markdown" (default) or "json". The JSON form returns a
// codetypes.FunctionDescriptor encoded as JSON.
func (t *GetFunctionDetailsTool) buildPHPFunctionResponse(chunk *codetypes.CodeChunk, codeBody, outputFormat string) (string, error) {
	format := strings.ToLower(outputFormat)
	if format == "" {
		format = "markdown"
	}

	// Helper to build a FunctionDescriptor from MethodInfo/FunctionInfo.
	buildDescriptor := func(methodInfo *php.MethodInfo, funcInfo *php.FunctionInfo, className, namespace string, eloquentModel *laravel.EloquentModel) codetypes.FunctionDescriptor {
		fd := codetypes.FunctionDescriptor{
			Language:  chunk.Language,
			Kind:      chunk.Type,
			Name:      chunk.Name,
			Namespace: namespace,
			Receiver:  className,
			Location: codetypes.SymbolLocation{
				FilePath:  chunk.FilePath,
				StartLine: chunk.StartLine,
				EndLine:   chunk.EndLine,
			},
			Code: codeBody,
		}

		var sig string
		if methodInfo != nil {
			fd.Visibility = methodInfo.Visibility
			fd.IsStatic = methodInfo.IsStatic
			fd.IsAbstract = methodInfo.IsAbstract
			fd.IsFinal = methodInfo.IsFinal
			fd.Description = methodInfo.Description
			sig = methodInfo.Signature
			if sig == "" {
				visibility := methodInfo.Visibility
				if visibility == "" {
					visibility = "public"
				}
				var prefixParts []string
				if methodInfo.IsAbstract {
					prefixParts = append(prefixParts, "abstract")
				}
				if methodInfo.IsFinal {
					prefixParts = append(prefixParts, "final")
				}
				prefixParts = append(prefixParts, visibility)
				if methodInfo.IsStatic {
					prefixParts = append(prefixParts, "static")
				}
				prefix := strings.Join(prefixParts, " ")
				sig = fmt.Sprintf("%s function %s()", prefix, methodInfo.Name)
			}
			// Parameters
			for _, p := range methodInfo.Parameters {
				typeStr := p.Type
				if typeStr == "" {
					typeStr = "mixed"
				}
				fd.Parameters = append(fd.Parameters, codetypes.ParamDescriptor{
					Name: p.Name,
					Type: typeStr,
				})
			}
			// Returns
			if len(methodInfo.Returns) > 0 {
				for _, r := range methodInfo.Returns {
					typeStr := r.Type
					if typeStr == "" {
						typeStr = "mixed"
					}
					fd.Returns = append(fd.Returns, codetypes.ReturnDescriptor{
						Type:        typeStr,
						Description: r.Description,
						SourceHint:  "phpdoc",
					})
				}
			} else if methodInfo.ReturnType != "" {
				fd.Returns = append(fd.Returns, codetypes.ReturnDescriptor{
					Type:       methodInfo.ReturnType,
					SourceHint: "type_hint",
				})
			}
		} else if funcInfo != nil {
			fd.Description = funcInfo.Description
			sig = funcInfo.Signature
			if sig == "" {
				sig = fmt.Sprintf("function %s()", funcInfo.Name)
			}
			for _, p := range funcInfo.Parameters {
				typeStr := p.Type
				if typeStr == "" {
					typeStr = "mixed"
				}
				fd.Parameters = append(fd.Parameters, codetypes.ParamDescriptor{
					Name: p.Name,
					Type: typeStr,
				})
			}
			if len(funcInfo.Returns) > 0 {
				for _, r := range funcInfo.Returns {
					typeStr := r.Type
					if typeStr == "" {
						typeStr = "mixed"
					}
					fd.Returns = append(fd.Returns, codetypes.ReturnDescriptor{
						Type:        typeStr,
						Description: r.Description,
						SourceHint:  "phpdoc",
					})
				}
			} else if funcInfo.ReturnType != "" {
				fd.Returns = append(fd.Returns, codetypes.ReturnDescriptor{
					Type:       funcInfo.ReturnType,
					SourceHint: "type_hint",
				})
			}
		} else {
			sig = chunk.Signature
			if sig == "" {
				sig = fmt.Sprintf("function %s()", chunk.Name)
			}
			fd.Description = chunk.Docstring
		}

		// If this is an Eloquent model relation method, inject a relation return type
		if eloquentModel != nil && methodInfo != nil {
			if rel := findRelationForMethod(eloquentModel, methodInfo.Name); rel != nil {
				relationType := formatRelationReturnType(rel)
				fd.Returns = append(fd.Returns, codetypes.ReturnDescriptor{
					Type:        relationType,
					Description: "Laravel Eloquent relation",
					SourceHint:  "inferred_relation",
				})
			}
		}

		fd.Signature = sig
		return fd
	}

	// Fallback if we don't have a file path
	if chunk.FilePath == "" {
		if format == "json" {
			// Minimal descriptor from the chunk only
			desc := codetypes.FunctionDescriptor{
				Language:    chunk.Language,
				Kind:        chunk.Type,
				Name:        chunk.Name,
				Namespace:   chunk.Package,
				Signature:   chunk.Signature,
				Description: chunk.Docstring,
				Location: codetypes.SymbolLocation{
					FilePath:  chunk.FilePath,
					StartLine: chunk.StartLine,
					EndLine:   chunk.EndLine,
				},
				Code: codeBody,
			}
			data, err := json.MarshalIndent(desc, "", "  ")
			if err != nil {
				return "", fmt.Errorf("failed to marshal PHP function descriptor: %w", err)
			}
			return string(data), nil
		}
	}

	analyzer := php.NewCodeAnalyzer()
	if _, err := analyzer.AnalyzeFile(chunk.FilePath); err != nil {
		// Degrade gracefully to a simple representation
		if format == "json" {
			desc := codetypes.FunctionDescriptor{
				Language:    chunk.Language,
				Kind:        chunk.Type,
				Name:        chunk.Name,
				Namespace:   chunk.Package,
				Signature:   chunk.Signature,
				Description: chunk.Docstring,
				Location: codetypes.SymbolLocation{
					FilePath:  chunk.FilePath,
					StartLine: chunk.StartLine,
					EndLine:   chunk.EndLine,
				},
				Code: codeBody,
			}
			data, err2 := json.MarshalIndent(desc, "", "  ")
			if err2 != nil {
				return "", fmt.Errorf("failed to marshal PHP function descriptor: %w", err2)
			}
			return string(data), nil
		}

		var response strings.Builder
		response.WriteString(fmt.Sprintf("# %s\n\n", chunk.Name))
		response.WriteString(fmt.Sprintf("**Type:** %s\n", chunk.Type))
		response.WriteString(fmt.Sprintf("**Namespace:** %s\n", chunk.Package))
		response.WriteString(fmt.Sprintf("**Location:** `%s:%d-%d`\n\n", chunk.FilePath, chunk.StartLine, chunk.EndLine))
		if chunk.Signature != "" {
			response.WriteString(fmt.Sprintf("**Signature:** `%s`\n\n", chunk.Signature))
		}
		if codeBody != "" {
			response.WriteString("**Code:**\n```php\n")
			response.WriteString(codeBody)
			response.WriteString("\n```\n")
		}
		return response.String(), nil
	}

	var methodInfo *php.MethodInfo
	var funcInfo *php.FunctionInfo
	var className string
	var namespace string
	var eloquentModel *laravel.EloquentModel

	// Locate the corresponding MethodInfo or FunctionInfo
	for _, pkg := range analyzer.GetPackages() {
		if chunk.Package != "" && pkg.Namespace != "" && pkg.Namespace != chunk.Package {
			continue
		}
		// Methods: search in classes, interfaces, traits
		if chunk.Type == "method" {
			for _, cls := range pkg.Classes {
				for i := range cls.Methods {
					m := &cls.Methods[i]
					if m.Name == chunk.Name {
						if chunk.StartLine > 0 && m.StartLine > 0 && chunk.StartLine != m.StartLine {
							continue
						}
						methodInfo = m
						className = cls.Name
						namespace = cls.Namespace
						// Try to resolve Eloquent model for richer relation info
						if eloquentModel == nil {
							eloquentModel = findEloquentModelForClass(pkg, cls.Name)
						}
						break
					}
				}
				if methodInfo != nil {
					break
				}
			}
			if methodInfo != nil {
				break
			}
			// Optionally search interfaces and traits as well
			for _, iface := range pkg.Interfaces {
				for i := range iface.Methods {
					m := &iface.Methods[i]
					if m.Name == chunk.Name {
						methodInfo = m
						className = iface.Name
						namespace = iface.Namespace
						break
					}
				}
				if methodInfo != nil {
					break
				}
			}
			if methodInfo != nil {
				break
			}
			for _, trait := range pkg.Traits {
				for i := range trait.Methods {
					m := &trait.Methods[i]
					if m.Name == chunk.Name {
						methodInfo = m
						className = trait.Name
						namespace = trait.Namespace
						break
					}
				}
				if methodInfo != nil {
					break
				}
			}
			if methodInfo != nil {
				break
			}
		} else {
			// Global function
			for i := range pkg.Functions {
				fn := &pkg.Functions[i]
				if fn.Name == chunk.Name {
					if chunk.StartLine > 0 && fn.StartLine > 0 && chunk.StartLine != fn.StartLine {
						continue
					}
					funcInfo = fn
					namespace = fn.Namespace
					break
				}
			}
			if funcInfo != nil {
				break
			}
		}
	}

	// JSON output
	if format == "json" {
		desc := buildDescriptor(methodInfo, funcInfo, className, namespace, eloquentModel)
		data, err := json.MarshalIndent(desc, "", "  ")
		if err != nil {
			return "", fmt.Errorf("failed to marshal PHP function descriptor: %w", err)
		}
		return string(data), nil
	}

	// Markdown output: preserve existing behaviour
	var response strings.Builder

	kind := chunk.Type
	if kind == "method" {
		kind = "method"
	} else {
		kind = "function"
	}

	// Header
	response.WriteString(fmt.Sprintf("# %s\n\n", chunk.Name))
	response.WriteString(fmt.Sprintf("**Kind:** %s\n", kind))
	if namespace == "" {
		namespace = chunk.Package
	}
	response.WriteString(fmt.Sprintf("**Namespace:** %s\n", namespace))
	if className != "" {
		response.WriteString(fmt.Sprintf("**Class:** %s\n", className))
	}

	// Signature
	var sig string
	if methodInfo != nil {
		sig = methodInfo.Signature
		if sig == "" {
			visibility := methodInfo.Visibility
			if visibility == "" {
				visibility = "public"
			}
			var prefixParts []string
			if methodInfo.IsAbstract {
				prefixParts = append(prefixParts, "abstract")
			}
			if methodInfo.IsFinal {
				prefixParts = append(prefixParts, "final")
			}
			prefixParts = append(prefixParts, visibility)
			if methodInfo.IsStatic {
				prefixParts = append(prefixParts, "static")
			}
			prefix := strings.Join(prefixParts, " ")
			sig = fmt.Sprintf("%s function %s()", prefix, methodInfo.Name)
		}
	} else if funcInfo != nil {
		sig = funcInfo.Signature
		if sig == "" {
			sig = fmt.Sprintf("function %s()", funcInfo.Name)
		}
	} else {
		sig = chunk.Signature
		if sig == "" {
			sig = fmt.Sprintf("function %s()", chunk.Name)
		}
	}
	response.WriteString(fmt.Sprintf("**Signature:** `%s`\n\n", sig))

	// Description
	if methodInfo != nil && methodInfo.Description != "" {
		response.WriteString(fmt.Sprintf("**Description:**\n%s\n\n", methodInfo.Description))
	} else if funcInfo != nil && funcInfo.Description != "" {
		response.WriteString(fmt.Sprintf("**Description:**\n%s\n\n", funcInfo.Description))
	} else if chunk.Docstring != "" {
		response.WriteString(fmt.Sprintf("**Description:**\n%s\n\n", chunk.Docstring))
	}

	// Location
	response.WriteString(fmt.Sprintf("**Location:** `%s:%d-%d`\n\n", chunk.FilePath, chunk.StartLine, chunk.EndLine))

	// Parameters
	if methodInfo != nil && len(methodInfo.Parameters) > 0 {
		response.WriteString("**Parameters:**\n")
		for _, p := range methodInfo.Parameters {
			typeStr := p.Type
			if typeStr == "" {
				typeStr = "mixed"
			}
			response.WriteString(fmt.Sprintf("- `$%s`: %s\n", p.Name, typeStr))
		}
		response.WriteString("\n")
	} else if funcInfo != nil && len(funcInfo.Parameters) > 0 {
		response.WriteString("**Parameters:**\n")
		for _, p := range funcInfo.Parameters {
			typeStr := p.Type
			if typeStr == "" {
				typeStr = "mixed"
			}
			response.WriteString(fmt.Sprintf("- `$%s`: %s\n", p.Name, typeStr))
		}
		response.WriteString("\n")
	}

	// Returns
	var returns []codetypes.ReturnInfo
	var returnType string
	if methodInfo != nil {
		returns = methodInfo.Returns
		returnType = methodInfo.ReturnType
	} else if funcInfo != nil {
		returns = funcInfo.Returns
		returnType = funcInfo.ReturnType
	}
	// If this is an Eloquent model relation method, inject a relation return type when missing
	if methodInfo != nil && eloquentModel != nil && findRelationForMethod(eloquentModel, methodInfo.Name) != nil {
		rel := findRelationForMethod(eloquentModel, methodInfo.Name)
		relationType := formatRelationReturnType(rel)
		if len(returns) == 0 && returnType == "" {
			returns = append(returns, codetypes.ReturnInfo{Type: relationType, Description: "Laravel Eloquent relation"})
		} else {
			returns = append(returns, codetypes.ReturnInfo{Type: relationType, Description: "Laravel Eloquent relation"})
		}
	}

	if len(returns) > 0 {
		response.WriteString("**Returns:**\n")
		for _, r := range returns {
			typeStr := r.Type
			if typeStr == "" {
				typeStr = "mixed"
			}
			if r.Description != "" {
				response.WriteString(fmt.Sprintf("- `%s` - %s\n", typeStr, r.Description))
			} else {
				response.WriteString(fmt.Sprintf("- `%s`\n", typeStr))
			}
		}
		response.WriteString("\n")
	} else if returnType != "" {
		response.WriteString("**Returns:**\n")
		response.WriteString(fmt.Sprintf("- `%s`\n\n", returnType))
	}

	// Code snippet
	if codeBody != "" {
		response.WriteString("**Code:**\n```php\n")
		response.WriteString(codeBody)
		response.WriteString("\n```\n")
	}

	return response.String(), nil
}

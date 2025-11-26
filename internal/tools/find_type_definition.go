package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/doITmagic/rag-code-mcp/internal/codetypes"
	"github.com/doITmagic/rag-code-mcp/internal/llm"
	"github.com/doITmagic/rag-code-mcp/internal/memory"
	"github.com/doITmagic/rag-code-mcp/internal/ragcode/analyzers/golang"
	"github.com/doITmagic/rag-code-mcp/internal/ragcode/analyzers/php"
	laravel "github.com/doITmagic/rag-code-mcp/internal/ragcode/analyzers/php/laravel"
	"github.com/doITmagic/rag-code-mcp/internal/workspace"
)

// FindTypeDefinitionTool finds and returns complete type definitions (struct/interface)
type FindTypeDefinitionTool struct {
	longTermMemory   memory.LongTermMemory
	embedder         llm.Provider
	workspaceManager *workspace.Manager
}

// NewFindTypeDefinitionTool creates a new type definition finder tool
func NewFindTypeDefinitionTool(ltm memory.LongTermMemory, embedder llm.Provider) *FindTypeDefinitionTool {
	return &FindTypeDefinitionTool{
		longTermMemory: ltm,
		embedder:       embedder,
	}
}

// SetWorkspaceManager sets the workspace manager for workspace-aware searching
func (t *FindTypeDefinitionTool) SetWorkspaceManager(wm *workspace.Manager) {
	t.workspaceManager = wm
}

func (t *FindTypeDefinitionTool) Name() string {
	return "find_type_definition"
}

func (t *FindTypeDefinitionTool) Description() string {
	return "Find class/struct/interface definition - returns complete type source code with all fields, methods, and inheritance chain. Use when you need to understand a data model or see what methods a type has. Returns the full type definition ready to read. Works for Go structs/interfaces, PHP classes, Python classes."
}

func (t *FindTypeDefinitionTool) Execute(ctx context.Context, args map[string]interface{}) (string, error) {
	typeName, ok := args["type_name"].(string)
	if !ok || typeName == "" {
		return "", fmt.Errorf("type_name is required")
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
		return "", fmt.Errorf("file_path parameter is required for find_type_definition. Please provide a file path from your workspace")
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

	// Detect language from file path to build appropriate query
	language := inferLanguageFromPath(filePath)

	// Search for the type in the vector database
	// Use language-appropriate keywords for better semantic matching
	var query string
	switch language {
	case "python":
		query = fmt.Sprintf("class %s definition python", typeName)
	case "php":
		query = fmt.Sprintf("class %s definition php", typeName)
	default:
		query = fmt.Sprintf("type %s definition struct interface", typeName)
	}
	if packagePath != "" {
		query = fmt.Sprintf("%s in package %s", query, packagePath)
	}

	// Generate query embedding
	queryEmbedding, err := t.embedder.Embed(ctx, query)
	if err != nil {
		return "", fmt.Errorf("failed to generate query embedding: %w", err)
	}

	// First, try exact name+type search (faster and more accurate)
	type ExactSearcher interface {
		SearchByNameAndType(ctx context.Context, name string, types []string) ([]memory.Document, error)
	}

	typeKinds := []string{"type", "class", "interface", "trait", "model"}

	var results []memory.Document
	if exactSearcher, ok := searchMemory.(ExactSearcher); ok {
		results, err = exactSearcher.SearchByNameAndType(ctx, typeName, typeKinds)
		if err == nil && len(results) > 0 {
			// Found exact match, use it directly
			goto processResults
		}
	}

	// Fallback to semantic search if exact search didn't find anything
	{
		type CodeSearcher interface {
			SearchCodeOnly(ctx context.Context, query []float64, limit int) ([]memory.Document, error)
		}

		if codeSearcher, ok := searchMemory.(CodeSearcher); ok {
			results, err = codeSearcher.SearchCodeOnly(ctx, queryEmbedding, 50)
		} else {
			results, err = searchMemory.Search(ctx, queryEmbedding, 50)
		}
		if err != nil {
			return "", fmt.Errorf("search failed: %w", err)
		}
	}

processResults:

	if len(results) == 0 {
		// Check if this is a workspace search with empty collection
		if workspacePath != "" && collectionName != "" {
			if msg, err := CheckSearchResults(0, collectionName, workspacePath); err != nil || msg != "" {
				if err != nil {
					return "", err
				}
				return msg, nil
			}
			return fmt.Sprintf("Type '%s' not found in workspace '%s'", typeName, workspacePath), nil
		}
		return fmt.Sprintf("Type '%s' not found", typeName), nil
	}

	// Find exact match (must be type chunk)
	// Support both Go types ("type") and PHP/other language types ("class", "interface", "trait")
	var bestMatch *memory.Document
	for _, result := range results {
		var chunk codetypes.CodeChunk
		if err := json.Unmarshal([]byte(result.Content), &chunk); err != nil {
			continue
		}

		// Check if this is a type-like chunk (Go: type, PHP: class/interface/trait)
		isTypeChunk := chunk.Type == "type" || chunk.Type == "class" || chunk.Type == "interface" || chunk.Type == "trait" || chunk.Type == "model"

		if !isTypeChunk {
			continue
		}

		// Check name match
		if chunk.Name != typeName {
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
		return fmt.Sprintf("Type '%s' not found (searched %d chunks)", typeName, len(results)), nil
	}

	var chunk codetypes.CodeChunk
	if err := json.Unmarshal([]byte(bestMatch.Content), &chunk); err != nil {
		return "", fmt.Errorf("failed to parse chunk: %w", err)
	}

	// Read actual code body from file if not in chunk
	codeBody := chunk.Code
	if codeBody == "" && chunk.FilePath != "" && chunk.StartLine > 0 && chunk.EndLine > 0 {
		body, err := readFileLines(chunk.FilePath, chunk.StartLine, chunk.EndLine)
		if err == nil {
			codeBody = body
		}
	}

	// PHP: use PHP analyzer directly on the source file to build a rich type view
	if chunk.Language == "php" {
		return t.buildPHPTypeResponse(&chunk, codeBody, outputFormat)
	}

	// Parse TypeInfo from chunk metadata if available (Go path)
	var typeInfo *golang.TypeInfo
	if metaJSON, ok := bestMatch.Metadata["type_info"].(string); ok {
		var ti golang.TypeInfo
		if err := json.Unmarshal([]byte(metaJSON), &ti); err == nil {
			typeInfo = &ti
		}
	}

	// Default (Go and others): optional JSON output, otherwise markdown using
	// Go TypeInfo metadata when available.
	if strings.ToLower(outputFormat) == "json" {
		desc := codetypes.ClassDescriptor{
			Language:    chunk.Language,
			Kind:        chunk.Type,
			Name:        chunk.Name,
			Namespace:   chunk.Package,
			Package:     chunk.Package,
			Signature:   chunk.Signature,
			Description: chunk.Docstring,
			Location: codetypes.SymbolLocation{
				FilePath:  chunk.FilePath,
				StartLine: chunk.StartLine,
				EndLine:   chunk.EndLine,
			},
		}

		// Enrich with field and method info when available
		if typeInfo != nil {
			if typeInfo.Kind == "struct" && len(typeInfo.Fields) > 0 {
				for _, f := range typeInfo.Fields {
					fd := codetypes.FieldDescriptor{
						Name:        f.Name,
						Type:        f.Type,
						Tag:         f.Tag,
						Description: f.Description,
					}
					desc.Fields = append(desc.Fields, fd)
				}
			}
			if len(typeInfo.Methods) > 0 {
				for _, m := range typeInfo.Methods {
					md := codetypes.FunctionDescriptor{
						Language:    chunk.Language,
						Kind:        "method",
						Name:        "", // method name may not be present in TypeInfo; rely on signature
						Namespace:   chunk.Package,
						Receiver:    chunk.Name,
						Signature:   m.Signature,
						Description: m.Description,
					}
					desc.Methods = append(desc.Methods, md)
				}
			}
		}

		data, err := json.MarshalIndent(desc, "", "  ")
		if err != nil {
			return "", fmt.Errorf("failed to marshal Go type descriptor: %w", err)
		}
		return string(data), nil
	}

	// Markdown output using Go TypeInfo metadata when available
	var response strings.Builder
	response.WriteString(fmt.Sprintf("# %s\n\n", chunk.Name))
	response.WriteString(fmt.Sprintf("**Kind:** %s\n", chunk.Type))
	response.WriteString(fmt.Sprintf("**Package:** %s\n", chunk.Package))

	if chunk.Docstring != "" {
		response.WriteString(fmt.Sprintf("\n**Description:**\n%s\n", chunk.Docstring))
	}

	response.WriteString(fmt.Sprintf("\n**Location:** `%s:%d-%d`\n\n", chunk.FilePath, chunk.StartLine, chunk.EndLine))

	if typeInfo != nil {
		// Show fields for structs
		if typeInfo.Kind == "struct" && len(typeInfo.Fields) > 0 {
			response.WriteString("**Fields:**\n")
			for _, field := range typeInfo.Fields {
				response.WriteString(fmt.Sprintf("- `%s %s`", field.Name, field.Type))
				if field.Tag != "" {
					response.WriteString(fmt.Sprintf(" `%s`", field.Tag))
				}
				if field.Description != "" {
					response.WriteString(fmt.Sprintf(" - %s", field.Description))
				}
				response.WriteString("\n")
			}
			response.WriteString("\n")
		}

		// Show methods
		if len(typeInfo.Methods) > 0 {
			response.WriteString("**Methods:**\n")
			for _, method := range typeInfo.Methods {
				response.WriteString(fmt.Sprintf("- `%s`", method.Signature))
				if method.Description != "" {
					response.WriteString(fmt.Sprintf(" - %s", method.Description))
				}
				response.WriteString("\n")
			}
			response.WriteString("\n")
		}
	}

	if codeBody != "" {
		response.WriteString("**Code:**\n```go\n")
		response.WriteString(codeBody)
		response.WriteString("\n```\n")
	}

	return response.String(), nil
}

// findEloquentModelForClass looks up the EloquentModel for a given PHP class name within a PackageInfo.
func findEloquentModelForClass(pkg *php.PackageInfo, className string) *laravel.EloquentModel {
	analyzer := laravel.NewEloquentAnalyzer(pkg)
	for _, m := range analyzer.AnalyzeModels() {
		if m.ClassName == className {
			return &m
		}
	}
	return nil
}

// findRelationForMethod finds an EloquentRelation matching a given method name.
func findRelationForMethod(model *laravel.EloquentModel, methodName string) *laravel.EloquentRelation {
	for i := range model.Relations {
		rel := &model.Relations[i]
		if rel.Name == methodName {
			return rel
		}
	}
	return nil
}

// formatRelationReturnType formats an EloquentRelation into a human-readable return type,
// e.g. "HasMany<App\Act>" or "BelongsToMany<App\Lawyer>".
func formatRelationReturnType(rel *laravel.EloquentRelation) string {
	base := rel.Type
	switch rel.Type {
	case "hasOne":
		base = "HasOne"
	case "hasMany":
		base = "HasMany"
	case "belongsTo":
		base = "BelongsTo"
	case "belongsToMany":
		base = "BelongsToMany"
	case "hasManyThrough":
		base = "HasManyThrough"
	case "morphTo":
		base = "MorphTo"
	case "morphMany":
		base = "MorphMany"
	case "morphToMany":
		base = "MorphToMany"
	case "morphedByMany":
		base = "MorphedByMany"
	}
	if rel.RelatedModel != "" {
		return fmt.Sprintf("%s<%s>", base, rel.RelatedModel)
	}
	return base
}

// buildPHPTypeResponse builds a rich type definition view for a PHP class/interface/trait
// by re-analyzing the source file with the PHP CodeAnalyzer. This avoids relying on
// vector metadata only and allows us to show fields and methods similar to Go's TypeInfo.
//
// outputFormat can be "markdown" (default) or "json". The JSON form returns a
// codetypes.ClassDescriptor encoded as JSON.
func (t *FindTypeDefinitionTool) buildPHPTypeResponse(chunk *codetypes.CodeChunk, codeBody, outputFormat string) (string, error) {
	format := strings.ToLower(outputFormat)
	if format == "" {
		format = "markdown"
	}

	// Helper to build a ClassDescriptor from whatever information we have.
	buildDescriptor := func(classInfo *php.ClassInfo, eloquentModel *laravel.EloquentModel) codetypes.ClassDescriptor {
		desc := codetypes.ClassDescriptor{
			Language:  chunk.Language,
			Kind:      chunk.Type,
			Name:      chunk.Name,
			Namespace: chunk.Package,
			Package:   chunk.Package,
			Location: codetypes.SymbolLocation{
				FilePath:  chunk.FilePath,
				StartLine: chunk.StartLine,
				EndLine:   chunk.EndLine,
			},
		}

		// Full name & signature
		if classInfo != nil {
			desc.FullName = classInfo.FullName
		}

		// Signature
		signature := chunk.Signature
		if classInfo != nil {
			if classInfo.Extends != "" {
				signature = fmt.Sprintf("class %s extends %s", classInfo.Name, classInfo.Extends)
			} else {
				signature = "class " + classInfo.Name
			}
			if len(classInfo.Implements) > 0 {
				signature += " implements " + strings.Join(classInfo.Implements, ", ")
			}
		} else if signature == "" {
			signature = "class " + chunk.Name
		}
		desc.Signature = signature

		// Description
		if classInfo != nil && classInfo.Description != "" {
			desc.Description = classInfo.Description
		} else if chunk.Docstring != "" {
			desc.Description = chunk.Docstring
		}

		// Fields
		if classInfo != nil {
			for _, prop := range classInfo.Properties {
				visibility := prop.Visibility
				if visibility == "" {
					visibility = "public"
				}
				typeStr := prop.Type
				if typeStr == "" {
					typeStr = "mixed"
				}
				fd := codetypes.FieldDescriptor{
					Name:        prop.Name,
					Type:        typeStr,
					Visibility:  visibility,
					Description: prop.Description,
				}
				desc.Fields = append(desc.Fields, fd)
			}
		}

		// Methods
		if classInfo != nil {
			for _, method := range classInfo.Methods {
				visibility := method.Visibility
				if visibility == "" {
					visibility = "public"
				}
				md := codetypes.FunctionDescriptor{
					Language:    chunk.Language,
					Kind:        "method",
					Name:        method.Name,
					Namespace:   classInfo.Namespace,
					Receiver:    classInfo.Name,
					Signature:   method.Signature,
					Description: method.Description,
					Location: codetypes.SymbolLocation{
						FilePath:  method.FilePath,
						StartLine: method.StartLine,
						EndLine:   method.EndLine,
					},
					Visibility: visibility,
					IsStatic:   method.IsStatic,
					IsAbstract: method.IsAbstract,
					IsFinal:    method.IsFinal,
					Code:       method.Code,
				}

				// Fallback signature if missing
				if md.Signature == "" {
					var prefixParts []string
					if method.IsAbstract {
						prefixParts = append(prefixParts, "abstract")
					}
					if method.IsFinal {
						prefixParts = append(prefixParts, "final")
					}
					prefixParts = append(prefixParts, visibility)
					if method.IsStatic {
						prefixParts = append(prefixParts, "static")
					}
					prefix := strings.Join(prefixParts, " ")
					md.Signature = fmt.Sprintf("%s function %s()", prefix, method.Name)
				}

				// Parameters
				for _, p := range method.Parameters {
					typeStr := p.Type
					if typeStr == "" {
						typeStr = "mixed"
					}
					md.Parameters = append(md.Parameters, codetypes.ParamDescriptor{
						Name: p.Name,
						Type: typeStr,
					})
				}

				// Returns
				if len(method.Returns) > 0 {
					for _, r := range method.Returns {
						typeStr := r.Type
						if typeStr == "" {
							typeStr = "mixed"
						}
						md.Returns = append(md.Returns, codetypes.ReturnDescriptor{
							Type:        typeStr,
							Description: r.Description,
							SourceHint:  "phpdoc",
						})
					}
				} else if method.ReturnType != "" {
					md.Returns = append(md.Returns, codetypes.ReturnDescriptor{
						Type:       method.ReturnType,
						SourceHint: "type_hint",
					})
				}

				// If this is an Eloquent model, try to infer relation return type
				if eloquentModel != nil {
					if rel := findRelationForMethod(eloquentModel, method.Name); rel != nil {
						relationType := formatRelationReturnType(rel)
						md.Returns = append(md.Returns, codetypes.ReturnDescriptor{
							Type:        relationType,
							Description: "Laravel Eloquent relation",
							SourceHint:  "inferred_relation",
						})
					}
				}

				desc.Methods = append(desc.Methods, md)
			}
		}

		// Laravel model-specific data
		if eloquentModel != nil {
			desc.Kind = "model"
			desc.Table = eloquentModel.Table
			desc.Fillable = eloquentModel.Fillable
			desc.Hidden = eloquentModel.Hidden
			desc.Visible = eloquentModel.Visible
			desc.Appends = eloquentModel.Appends
			desc.Casts = eloquentModel.Casts

			for _, s := range eloquentModel.Scopes {
				desc.Scopes = append(desc.Scopes, s.Name)
			}
			for _, attr := range eloquentModel.Attributes {
				desc.Attributes = append(desc.Attributes, attr.Name)
			}
			for _, rel := range eloquentModel.Relations {
				desc.Relations = append(desc.Relations, codetypes.RelationDescriptor{
					Name:          rel.Name,
					RelationKind:  rel.Type,
					RelatedSymbol: rel.RelatedModel,
					ForeignKey:    rel.ForeignKey,
					LocalKey:      rel.LocalKey,
				})
			}
			// Add basic framework tags so consumers can detect model types easily
			desc.Tags = append(desc.Tags, "framework:laravel", "laravel:model")
		}

		return desc
	}

	// If we don't have a file path, fall back to a simple view based on the chunk only.
	if chunk.FilePath == "" {
		if format == "json" {
			// Minimal descriptor based on the chunk
			desc := buildDescriptor(nil, nil)
			data, err := json.MarshalIndent(desc, "", "  ")
			if err != nil {
				return "", fmt.Errorf("failed to marshal PHP type descriptor: %w", err)
			}
			return string(data), nil
		}

		var response strings.Builder
		response.WriteString(fmt.Sprintf("# %s\n\n", chunk.Name))
		response.WriteString("**Kind:** class\n")
		response.WriteString(fmt.Sprintf("**Namespace:** %s\n", chunk.Package))
		if chunk.Signature != "" {
			response.WriteString(fmt.Sprintf("**Signature:** `%s`\n", chunk.Signature))
		}
		response.WriteString(fmt.Sprintf("\n**Location:** `%s:%d-%d`\n\n", chunk.FilePath, chunk.StartLine, chunk.EndLine))
		if codeBody != "" {
			response.WriteString("**Code:**\n```php\n")
			response.WriteString(codeBody)
			response.WriteString("\n```\n")
		}
		return response.String(), nil
	}

	// Re-run the PHP analyzer on the source file to reconstruct ClassInfo
	analyzer := php.NewCodeAnalyzer()
	if _, err := analyzer.AnalyzeFile(chunk.FilePath); err != nil {
		// If analyzer fails, degrade gracefully
		if format == "json" {
			desc := buildDescriptor(nil, nil)
			data, err2 := json.MarshalIndent(desc, "", "  ")
			if err2 != nil {
				return "", fmt.Errorf("failed to marshal PHP type descriptor: %w", err2)
			}
			return string(data), nil
		}

		var response strings.Builder
		response.WriteString(fmt.Sprintf("# %s\n\n", chunk.Name))
		response.WriteString("**Kind:** class\n")
		response.WriteString(fmt.Sprintf("**Namespace:** %s\n", chunk.Package))
		response.WriteString(fmt.Sprintf("\n**Location:** `%s:%d-%d`\n\n", chunk.FilePath, chunk.StartLine, chunk.EndLine))
		if codeBody != "" {
			response.WriteString("**Code:**\n```php\n")
			response.WriteString(codeBody)
			response.WriteString("\n```\n")
		}
		return response.String(), nil
	}

	var classInfo *php.ClassInfo
	var eloquentModel *laravel.EloquentModel
	for _, pkg := range analyzer.GetPackages() {
		// Narrow down by namespace if we have it
		if chunk.Package != "" && pkg.Namespace != "" && pkg.Namespace != chunk.Package {
			continue
		}
		for i := range pkg.Classes {
			cls := pkg.Classes[i]
			if cls.Name == chunk.Name {
				classInfo = &cls
				// If this is a Laravel project, try to enrich with Eloquent relations
				eloquentModel = findEloquentModelForClass(pkg, cls.Name)
				break
			}
		}
		if classInfo != nil {
			break
		}
	}

	// JSON output: return a structured descriptor
	if format == "json" {
		desc := buildDescriptor(classInfo, eloquentModel)
		data, err := json.MarshalIndent(desc, "", "  ")
		if err != nil {
			return "", fmt.Errorf("failed to marshal PHP type descriptor: %w", err)
		}
		return string(data), nil
	}

	// Markdown output: preserve existing behaviour
	var response strings.Builder

	// Header
	response.WriteString(fmt.Sprintf("# %s\n\n", chunk.Name))
	response.WriteString("**Kind:** class\n")
	response.WriteString(fmt.Sprintf("**Namespace:** %s\n", chunk.Package))

	// Signature
	signature := chunk.Signature
	if signature == "" {
		signature = "class " + chunk.Name
	}
	if classInfo != nil {
		if classInfo.Extends != "" {
			signature = fmt.Sprintf("class %s extends %s", classInfo.Name, classInfo.Extends)
		} else {
			signature = "class " + classInfo.Name
		}
		if len(classInfo.Implements) > 0 {
			signature += " implements " + strings.Join(classInfo.Implements, ", ")
		}
	}
	response.WriteString(fmt.Sprintf("**Signature:** `%s`\n", signature))

	if chunk.Docstring != "" {
		response.WriteString(fmt.Sprintf("\n**Description:**\n%s\n", chunk.Docstring))
	} else if classInfo != nil && classInfo.Description != "" {
		response.WriteString(fmt.Sprintf("\n**Description:**\n%s\n", classInfo.Description))
	}

	response.WriteString(fmt.Sprintf("\n**Location:** `%s:%d-%d`\n\n", chunk.FilePath, chunk.StartLine, chunk.EndLine))

	// Fields (properties)
	if classInfo != nil && len(classInfo.Properties) > 0 {
		response.WriteString("**Fields:**\n")
		for _, prop := range classInfo.Properties {
			visibility := prop.Visibility
			if visibility == "" {
				visibility = "public"
			}
			typeStr := prop.Type
			if typeStr == "" {
				typeStr = "mixed"
			}
			response.WriteString(fmt.Sprintf("- `%s %s $%s`", visibility, typeStr, prop.Name))
			if prop.Description != "" {
				response.WriteString(fmt.Sprintf(" - %s", prop.Description))
			}
			response.WriteString("\n")
		}
		response.WriteString("\n")
	}

	// Methods
	if classInfo != nil && len(classInfo.Methods) > 0 {
		response.WriteString("**Methods:**\n")
		for _, method := range classInfo.Methods {
			// Prefer the full PHP method signature if available
			sig := method.Signature
			if sig == "" {
				// Fallback to a detailed representation including flags
				visibility := method.Visibility
				if visibility == "" {
					visibility = "public"
				}
				var prefixParts []string
				// Order is not critical for readability, but we keep it conventional
				if method.IsAbstract {
					prefixParts = append(prefixParts, "abstract")
				}
				if method.IsFinal {
					prefixParts = append(prefixParts, "final")
				}
				prefixParts = append(prefixParts, visibility)
				if method.IsStatic {
					prefixParts = append(prefixParts, "static")
				}
				prefix := strings.Join(prefixParts, " ")
				sig = fmt.Sprintf("%s function %s()", prefix, method.Name)
			}
			response.WriteString(fmt.Sprintf("- `%s`", sig))
			if method.Description != "" {
				response.WriteString(fmt.Sprintf(" - %s", method.Description))
			}
			response.WriteString("\n")

			// Location for quick navigation insight
			if method.FilePath != "" && method.StartLine > 0 {
				response.WriteString(fmt.Sprintf("  - Location: `%s:%d-%d`\n", method.FilePath, method.StartLine, method.EndLine))
			}

			// Parameters
			if len(method.Parameters) > 0 {
				response.WriteString("  - Parameters:\n")
				for _, p := range method.Parameters {
					typeStr := p.Type
					if typeStr == "" {
						typeStr = "mixed"
					}
					response.WriteString(fmt.Sprintf("    - `$%s`: %s\n", p.Name, typeStr))
				}
			}

			// Returns
			if len(method.Returns) > 0 {
				response.WriteString("  - Returns:\n")
				for _, r := range method.Returns {
					typeStr := r.Type
					if typeStr == "" {
						typeStr = "mixed"
					}
					if r.Description != "" {
						response.WriteString(fmt.Sprintf("    - `%s` - %s\n", typeStr, r.Description))
					} else {
						response.WriteString(fmt.Sprintf("    - `%s`\n", typeStr))
					}
				}
			} else if method.ReturnType != "" {
				response.WriteString("  - Returns:\n")
				response.WriteString(fmt.Sprintf("    - `%s`\n", method.ReturnType))
			} else if eloquentModel != nil {
				// If this is an Eloquent model, try to infer relation return type
				if rel := findRelationForMethod(eloquentModel, method.Name); rel != nil {
					relationType := formatRelationReturnType(rel)
					response.WriteString("  - Returns:\n")
					response.WriteString(fmt.Sprintf("    - `%s`\n", relationType))
				}
			}

			response.WriteString("\n")
		}
	}

	// Laravel relations section (if any)
	if eloquentModel != nil && len(eloquentModel.Relations) > 0 {
		response.WriteString("**Laravel Relations:**\n")
		for _, rel := range eloquentModel.Relations {
			relationType := formatRelationReturnType(&rel)
			response.WriteString(fmt.Sprintf("- `%s`: `%s`", rel.Name, relationType))
			if rel.ForeignKey != "" || rel.LocalKey != "" {
				keys := []string{}
				if rel.ForeignKey != "" {
					keys = append(keys, fmt.Sprintf("foreignKey=%s", rel.ForeignKey))
				}
				if rel.LocalKey != "" {
					keys = append(keys, fmt.Sprintf("localKey=%s", rel.LocalKey))
				}
				response.WriteString(fmt.Sprintf(" (%s)", strings.Join(keys, ", ")))
			}
			response.WriteString("\n")
		}
		response.WriteString("\n")
	}

	// Code snippet
	if codeBody != "" {
		response.WriteString("**Code:**\n```php\n")
		response.WriteString(codeBody)
		response.WriteString("\n```\n")
	}

	return response.String(), nil
}

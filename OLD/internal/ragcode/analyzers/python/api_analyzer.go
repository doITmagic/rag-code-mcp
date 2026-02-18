//go:build ignore
// +build ignore

package python

import (
	"fmt"
	"io/fs"
	"path/filepath"
	"strings"

	"github.com/doITmagic/rag-code-mcp/internal/codetypes"
)

// APIAnalyzerImpl implements APIAnalyzer by reusing ModuleInfo from CodeAnalyzer.
// LEGACY: This is kept for backward compatibility. New code should use PathAnalyzer.
type APIAnalyzerImpl struct {
	codeAnalyzer *CodeAnalyzer
}

// NewAPIAnalyzer creates a new API analyzer using the existing CodeAnalyzer.
func NewAPIAnalyzer(codeAnalyzer *CodeAnalyzer) *APIAnalyzerImpl {
	return &APIAnalyzerImpl{codeAnalyzer: codeAnalyzer}
}

// AnalyzeAPIPaths walks the provided paths, analyzes Python modules, and returns APIChunks.
func (a *APIAnalyzerImpl) AnalyzeAPIPaths(paths []string) ([]codetypes.APIChunk, error) {
	chunks := make([]codetypes.APIChunk, 0)
	visited := make(map[string]bool)

	for _, root := range paths {
		err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return nil
			}
			if d.IsDir() {
				base := filepath.Base(path)
				if base == "__pycache__" || base == ".venv" || base == "venv" ||
					base == ".git" || strings.HasPrefix(base, ".") {
					if path != root {
						return filepath.SkipDir
					}
				}
				return nil
			}
			if !strings.HasSuffix(d.Name(), ".py") || strings.HasPrefix(d.Name(), "test_") {
				return nil
			}

			if visited[path] {
				return nil
			}
			visited[path] = true

			moduleChunks, perr := a.codeAnalyzer.AnalyzeFile(path)
			if perr != nil {
				return nil
			}

			// Convert CodeChunks to APIChunks
			for _, chunk := range moduleChunks {
				apiChunk := convertCodeChunkToAPIChunk(chunk)
				chunks = append(chunks, apiChunk)
			}
			return nil
		})
		if err != nil {
			return nil, err
		}
	}

	return chunks, nil
}

// convertCodeChunkToAPIChunk converts a CodeChunk to an APIChunk
func convertCodeChunkToAPIChunk(chunk codetypes.CodeChunk) codetypes.APIChunk {
	apiChunk := codetypes.APIChunk{
		Kind:        chunk.Type,
		Language:    chunk.Language,
		Name:        chunk.Name,
		Package:     chunk.Package,
		PackagePath: chunk.Package,
		Signature:   chunk.Signature,
		Description: chunk.Docstring,
		FilePath:    chunk.FilePath,
		StartLine:   chunk.StartLine,
		EndLine:     chunk.EndLine,
		Code:        chunk.Code,
		IsExported:  isExportedPython(chunk.Name),
	}

	// Extract additional metadata
	if chunk.Metadata != nil {
		if className, ok := chunk.Metadata["class_name"].(string); ok {
			apiChunk.ContainerName = className
		}
		if isStatic, ok := chunk.Metadata["is_static"].(bool); ok && isStatic {
			apiChunk.Tags = append(apiChunk.Tags, "static")
		}
		if isAsync, ok := chunk.Metadata["is_async"].(bool); ok && isAsync {
			apiChunk.Tags = append(apiChunk.Tags, "async")
		}
		if isAbstract, ok := chunk.Metadata["is_abstract"].(bool); ok && isAbstract {
			apiChunk.Tags = append(apiChunk.Tags, "abstract")
		}
	}

	return apiChunk
}

// isExportedPython checks if a Python symbol is "exported" (public)
// In Python, names starting with underscore are considered private
func isExportedPython(name string) bool {
	return !strings.HasPrefix(name, "_")
}

// convertModuleInfoToAPIChunks converts ModuleInfo to APIChunks
func convertModuleInfoToAPIChunks(module *ModuleInfo) []codetypes.APIChunk {
	chunks := make([]codetypes.APIChunk, 0)

	// Convert classes
	for _, class := range module.Classes {
		chunk := codetypes.APIChunk{
			Kind:        "class",
			Language:    "python",
			Name:        class.Name,
			Package:     module.Name,
			PackagePath: module.Path,
			Signature:   buildClassSignature(class),
			Description: class.Description,
			IsExported:  isExportedPython(class.Name),
			FilePath:    class.FilePath,
			StartLine:   class.StartLine,
			EndLine:     class.EndLine,
			Code:        class.Code,
		}

		// Add methods as children
		for _, method := range class.Methods {
			methodChunk := codetypes.APIChunk{
				Kind:          kindForMethod(method),
				Language:      "python",
				Name:          method.Name,
				Package:       module.Name,
				Signature:     method.Signature,
				Description:   method.Description,
				Parameters:    method.Parameters,
				Returns:       method.Returns,
				IsExported:    isExportedPython(method.Name),
				ContainerName: class.Name,
				FilePath:      method.FilePath,
				StartLine:     method.StartLine,
				EndLine:       method.EndLine,
				Code:          method.Code,
			}
			chunk.Children = append(chunk.Children, methodChunk)
		}

		chunks = append(chunks, chunk)
	}

	// Convert functions
	for _, fn := range module.Functions {
		chunk := codetypes.APIChunk{
			Kind:        "function",
			Language:    "python",
			Name:        fn.Name,
			Package:     module.Name,
			PackagePath: module.Path,
			Signature:   fn.Signature,
			Description: fn.Description,
			Parameters:  fn.Parameters,
			Returns:     fn.Returns,
			IsExported:  isExportedPython(fn.Name),
			FilePath:    fn.FilePath,
			StartLine:   fn.StartLine,
			EndLine:     fn.EndLine,
			Code:        fn.Code,
		}

		if fn.IsAsync {
			chunk.Tags = append(chunk.Tags, "async")
		}
		if fn.IsGenerator {
			chunk.Tags = append(chunk.Tags, "generator")
		}

		chunks = append(chunks, chunk)
	}

	// Convert constants
	for _, c := range module.Constants {
		chunk := codetypes.APIChunk{
			Kind:        "const",
			Language:    "python",
			Name:        c.Name,
			Package:     module.Name,
			PackagePath: module.Path,
			Signature:   fmt.Sprintf("%s = %s", c.Name, c.Value),
			Description: c.Description,
			DataType:    c.Type,
			Value:       c.Value,
			IsExported:  isExportedPython(c.Name),
			FilePath:    c.FilePath,
			StartLine:   c.StartLine,
			EndLine:     c.EndLine,
		}
		chunks = append(chunks, chunk)
	}

	// Convert variables
	for _, v := range module.Variables {
		chunk := codetypes.APIChunk{
			Kind:        "var",
			Language:    "python",
			Name:        v.Name,
			Package:     module.Name,
			PackagePath: module.Path,
			Signature:   fmt.Sprintf("%s: %s", v.Name, v.Type),
			Description: v.Description,
			DataType:    v.Type,
			IsExported:  isExportedPython(v.Name),
			FilePath:    v.FilePath,
			StartLine:   v.StartLine,
			EndLine:     v.EndLine,
		}
		chunks = append(chunks, chunk)
	}

	return chunks
}

// kindForMethod determines the kind string for a method
func kindForMethod(method MethodInfo) string {
	if method.IsProperty {
		return "property"
	}
	if method.IsStatic {
		return "staticmethod"
	}
	if method.IsClassMethod {
		return "classmethod"
	}
	return "method"
}

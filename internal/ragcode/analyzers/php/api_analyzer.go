//go:build ignore
// +build ignore

package php

import (
	"fmt"
	"io/fs"
	"path/filepath"
	"strings"

	"github.com/doITmagic/rag-code-mcp/internal/codetypes"
)

// APIAnalyzerImpl implements APIAnalyzer by reusing PackageInfo from CodeAnalyzer.
type APIAnalyzerImpl struct {
	codeAnalyzer *CodeAnalyzer
}

// NewAPIAnalyzer creates a new API analyzer using the existing CodeAnalyzer.
func NewAPIAnalyzer(codeAnalyzer *CodeAnalyzer) *APIAnalyzerImpl {
	return &APIAnalyzerImpl{codeAnalyzer: codeAnalyzer}
}

// AnalyzeAPIPaths walks the provided paths, analyzes PHP files, and returns APIChunks.
func (a *APIAnalyzerImpl) AnalyzeAPIPaths(paths []string) ([]codetypes.APIChunk, error) {
	chunks := make([]codetypes.APIChunk, 0)

	for _, root := range paths {
		err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return nil
			}
			if d.IsDir() {
				base := filepath.Base(path)
				// Skip vendor, node_modules, hidden dirs, etc.
				if base == "vendor" || base == "node_modules" || base == ".git" ||
					base == "storage" || base == "cache" || strings.HasPrefix(base, ".") {
					if path != root {
						return filepath.SkipDir
					}
				}
				return nil
			}

			// Only process .php files (not .blade.php or test files)
			if !strings.HasSuffix(d.Name(), ".php") ||
				strings.HasSuffix(d.Name(), ".blade.php") ||
				strings.HasSuffix(d.Name(), "Test.php") {
				return nil
			}

			// Analyze this PHP file
			if _, err = a.codeAnalyzer.AnalyzeFile(path); err != nil {
				// Log but don't fail - continue with other files
				return nil
			}

			// Convert CodeChunks to APIChunks
			// We need to re-analyze with PackageInfo to get full details
			pkgInfos := a.codeAnalyzer.packages
			for _, pkg := range pkgInfos {
				chunks = append(chunks, convertPackageInfoToAPIChunks(pkg)...)
			}

			return nil
		})
		if err != nil {
			return nil, err
		}
	}

	return chunks, nil
}

func convertPackageInfoToAPIChunks(pi *PackageInfo) []codetypes.APIChunk {
	chunks := make([]codetypes.APIChunk, 0)

	// Convert classes
	for _, cls := range pi.Classes {
		// Class chunk
		chunk := codetypes.APIChunk{
			Kind:        "class",
			Language:    "php",
			Name:        cls.Name,
			Package:     cls.Namespace,
			PackagePath: pi.Path,
			Signature:   buildClassSignature(cls),
			Description: cls.Description,
			IsExported:  true, // PHP classes are always "exported" if not anonymous
			FilePath:    cls.FilePath,
			StartLine:   cls.StartLine,
			EndLine:     cls.EndLine,
			Code:        cls.Code,
		}

		// Add methods as separate chunks
		methodInfos := make([]codetypes.MethodInfo, 0, len(cls.Methods))
		for _, method := range cls.Methods {
			methodInfos = append(methodInfos, codetypes.MethodInfo{
				Name:        method.Name,
				Signature:   method.Signature,
				Description: method.Description,
				Parameters:  method.Parameters,
				Returns:     method.Returns,
				IsExported:  method.Visibility == "public",
				FilePath:    method.FilePath,
				StartLine:   method.StartLine,
				EndLine:     method.EndLine,
				Code:        method.Code,
			})
		}
		chunk.Methods = methodInfos

		// Add properties as fields
		fieldInfos := make([]codetypes.FieldInfo, 0, len(cls.Properties))
		for _, prop := range cls.Properties {
			fieldInfos = append(fieldInfos, codetypes.FieldInfo{
				Name:        prop.Name,
				Type:        prop.Type,
				Description: prop.Description,
			})
		}
		chunk.Fields = fieldInfos

		chunks = append(chunks, chunk)
	}

	// Convert interfaces
	for _, iface := range pi.Interfaces {
		chunk := codetypes.APIChunk{
			Kind:        "interface",
			Language:    "php",
			Name:        iface.Name,
			Package:     iface.Namespace,
			PackagePath: pi.Path,
			Signature:   buildInterfaceSignature(iface),
			Description: iface.Description,
			IsExported:  true,
			FilePath:    iface.FilePath,
			StartLine:   iface.StartLine,
			EndLine:     iface.EndLine,
			Code:        iface.Code,
		}

		// Add methods
		methodInfos := make([]codetypes.MethodInfo, 0, len(iface.Methods))
		for _, method := range iface.Methods {
			methodInfos = append(methodInfos, codetypes.MethodInfo{
				Name:        method.Name,
				Signature:   method.Signature,
				Description: method.Description,
				Parameters:  method.Parameters,
				Returns:     method.Returns,
				IsExported:  true, // Interface methods are always public
				FilePath:    method.FilePath,
				StartLine:   method.StartLine,
				EndLine:     method.EndLine,
				Code:        method.Code,
			})
		}
		chunk.Methods = methodInfos

		chunks = append(chunks, chunk)
	}

	// Convert traits
	for _, trait := range pi.Traits {
		chunk := codetypes.APIChunk{
			Kind:        "trait",
			Language:    "php",
			Name:        trait.Name,
			Package:     trait.Namespace,
			PackagePath: pi.Path,
			Signature:   fmt.Sprintf("trait %s", trait.Name),
			Description: trait.Description,
			IsExported:  true,
			FilePath:    trait.FilePath,
			StartLine:   trait.StartLine,
			EndLine:     trait.EndLine,
			Code:        trait.Code,
		}

		// Add methods
		methodInfos := make([]codetypes.MethodInfo, 0, len(trait.Methods))
		for _, method := range trait.Methods {
			methodInfos = append(methodInfos, codetypes.MethodInfo{
				Name:        method.Name,
				Signature:   method.Signature,
				Description: method.Description,
				Parameters:  method.Parameters,
				Returns:     method.Returns,
				IsExported:  method.Visibility == "public",
				FilePath:    method.FilePath,
				StartLine:   method.StartLine,
				EndLine:     method.EndLine,
				Code:        method.Code,
			})
		}
		chunk.Methods = methodInfos

		// Add properties
		fieldInfos := make([]codetypes.FieldInfo, 0, len(trait.Properties))
		for _, prop := range trait.Properties {
			fieldInfos = append(fieldInfos, codetypes.FieldInfo{
				Name:        prop.Name,
				Type:        prop.Type,
				Description: prop.Description,
			})
		}
		chunk.Fields = fieldInfos

		chunks = append(chunks, chunk)
	}

	// Convert global functions
	for _, fn := range pi.Functions {
		chunk := codetypes.APIChunk{
			Kind:        "function",
			Language:    "php",
			Name:        fn.Name,
			Package:     fn.Namespace,
			PackagePath: pi.Path,
			Signature:   fn.Signature,
			Description: fn.Description,
			Parameters:  fn.Parameters,
			Returns:     fn.Returns,
			IsExported:  true, // Global functions are always accessible
			FilePath:    fn.FilePath,
			StartLine:   fn.StartLine,
			EndLine:     fn.EndLine,
			Code:        fn.Code,
		}
		chunks = append(chunks, chunk)
	}

	// Convert global constants
	for _, c := range pi.Constants {
		chunk := codetypes.APIChunk{
			Kind:        "const",
			Language:    "php",
			Name:        c.Name,
			Package:     pi.Namespace,
			PackagePath: pi.Path,
			Signature:   fmt.Sprintf("const %s", c.Name),
			Description: c.Description,
			DataType:    c.Type,
			Value:       c.Value,
			IsExported:  true,
			FilePath:    c.FilePath,
			StartLine:   c.StartLine,
			EndLine:     c.EndLine,
		}
		chunks = append(chunks, chunk)
	}

	return chunks
}

func buildClassSignature(cls ClassInfo) string {
	sig := "class " + cls.Name

	if cls.Extends != "" {
		sig += " extends " + cls.Extends
	}

	if len(cls.Implements) > 0 {
		sig += " implements " + strings.Join(cls.Implements, ", ")
	}

	return sig
}

func buildInterfaceSignature(iface InterfaceInfo) string {
	sig := "interface " + iface.Name

	if len(iface.Extends) > 0 {
		sig += " extends " + strings.Join(iface.Extends, ", ")
	}

	return sig
}

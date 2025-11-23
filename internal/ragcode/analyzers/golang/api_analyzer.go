//go:build ignore
// +build ignore

package golang

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

// AnalyzeAPIPaths walks the provided paths, analyzes Go packages, and returns APIChunks.
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
				if base == "vendor" || base == ".git" || base == "testdata" || strings.HasPrefix(base, ".") {
					if path != root {
						return filepath.SkipDir
					}
				}
				return nil
			}
			if !strings.HasSuffix(d.Name(), ".go") || strings.HasSuffix(d.Name(), "_test.go") {
				return nil
			}

			dir := filepath.Dir(path)
			if visited[dir] {
				return nil
			}
			visited[dir] = true

			pkgInfo, perr := a.codeAnalyzer.AnalyzePackage(dir)
			if perr != nil {
				return nil
			}

			chunks = append(chunks, convertPackageInfoToAPIChunks(pkgInfo)...)
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

	for _, fn := range pi.Functions {
		chunk := codetypes.APIChunk{
			Kind:        kindForFunction(fn),
			Language:    "go",
			Name:        fn.Name,
			Package:     pi.Name,
			PackagePath: pi.Path,
			Signature:   fn.Signature,
			Description: fn.Description,
			Parameters:  fn.Parameters,
			Returns:     fn.Returns,
			Examples:    fn.Examples,
			IsExported:  fn.IsExported,
			FilePath:    fn.FilePath,
			StartLine:   fn.StartLine,
			EndLine:     fn.EndLine,
			Code:        fn.Code,
		}
		if fn.IsMethod {
			chunk.Receiver = fn.Receiver
		}
		chunks = append(chunks, chunk)
	}

	for _, tp := range pi.Types {
		signature := fmt.Sprintf("type %s", tp.Name)
		if tp.Kind != "" {
			signature = fmt.Sprintf("type %s %s", tp.Name, tp.Kind)
		}

		chunk := codetypes.APIChunk{
			Kind:        "type",
			Language:    "go",
			Name:        tp.Name,
			Package:     pi.Name,
			PackagePath: pi.Path,
			Signature:   signature,
			Description: tp.Description,
			Fields:      tp.Fields,
			Methods:     tp.Methods,
			IsExported:  tp.IsExported,
			FilePath:    tp.FilePath,
			StartLine:   tp.StartLine,
			EndLine:     tp.EndLine,
			Code:        tp.Code,
		}
		chunks = append(chunks, chunk)
	}

	for _, c := range pi.Constants {
		chunk := codetypes.APIChunk{
			Kind:        "const",
			Language:    "go",
			Name:        c.Name,
			Package:     pi.Name,
			PackagePath: pi.Path,
			Signature:   fmt.Sprintf("const %s %s", c.Name, c.Type),
			Description: c.Description,
			DataType:    c.Type,
			Value:       c.Value,
			IsExported:  c.IsExported,
			FilePath:    c.FilePath,
			StartLine:   c.StartLine,
			EndLine:     c.EndLine,
		}
		chunks = append(chunks, chunk)
	}

	for _, v := range pi.Variables {
		chunk := codetypes.APIChunk{
			Kind:        "var",
			Language:    "go",
			Name:        v.Name,
			Package:     pi.Name,
			PackagePath: pi.Path,
			Signature:   fmt.Sprintf("var %s %s", v.Name, v.Type),
			Description: v.Description,
			DataType:    v.Type,
			IsExported:  v.IsExported,
			FilePath:    v.FilePath,
			StartLine:   v.StartLine,
			EndLine:     v.EndLine,
		}
		chunks = append(chunks, chunk)
	}

	return chunks
}

func kindForFunction(fn FunctionInfo) string {
	if fn.IsMethod {
		return "method"
	}
	return "function"
}

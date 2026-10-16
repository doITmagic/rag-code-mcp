package indexer

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/doITmagic/rag-code-mcp/pkg/parser"
)

// WorkspaceScan is a per-run inventory shared by all language collections.
// Paths are kept private so callers cannot inject files outside the scanned root.
// File metadata is checked when each language starts, rather than cached here.
type WorkspaceScan struct {
	FileCountResult
	root            string
	excludePatterns []string
	files           map[string][]string
	excluded        map[string][]string
}

// ScanWorkspace walks the workspace once, grouping supported files by language
// and computing progress totals with the same exclusions used by indexing.
// Reuse the result only within one indexing run; later runs need a fresh scan.
func (s *Service) ScanWorkspace(ctx context.Context, root string, excludePatterns []string) (*WorkspaceScan, error) {
	scan := &WorkspaceScan{
		FileCountResult: FileCountResult{
			Counts:     make(map[string]int),
			Breakdowns: make(map[string]map[string]int),
		},
		root:            filepath.Clean(root),
		excludePatterns: append([]string(nil), excludePatterns...),
		files:           make(map[string][]string),
		excluded:        make(map[string][]string),
	}
	err := filepath.WalkDir(scan.root, func(path string, d os.DirEntry, err error) error {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ctxErr
		}
		if err != nil {
			if path == scan.root {
				return err
			}
			return nil
		}
		relPath, _ := filepath.Rel(scan.root, path)
		relSlash := filepath.ToSlash(relPath)
		if d.IsDir() {
			if path == scan.root {
				return nil
			}
			name := d.Name()
			if strings.HasPrefix(name, ".") || name == "vendor" || name == "node_modules" || name == "tmp" ||
				matchesExcludePatterns(relSlash, name, scan.excludePatterns) {
				return filepath.SkipDir
			}
			return nil
		}
		a := parser.GetByFile(path)
		if a == nil {
			return nil
		}
		lang := a.Name()
		if matchesExcludePatterns(relSlash, d.Name(), scan.excludePatterns) || shouldSkipFile(path) {
			scan.excluded[lang] = append(scan.excluded[lang], path)
			return nil
		}
		scan.files[lang] = append(scan.files[lang], path)
		scan.Counts[lang]++
		ext := strings.ToLower(filepath.Ext(path))
		if ext == "" {
			ext = strings.ToLower(filepath.Base(path))
		}
		if scan.Breakdowns[lang] == nil {
			scan.Breakdowns[lang] = make(map[string]int)
		}
		scan.Breakdowns[lang][ext]++
		return nil
	})
	if err != nil {
		return scan, fmt.Errorf("failed to scan workspace: %w", err)
	}
	return scan, nil
}

// languageFiles also supports legacy callers that index all languages into a
// single collection (language == ""). Keep their lexical traversal order.
func languageFiles(files map[string][]string, language string) []string {
	if language != "" {
		return files[language]
	}
	var all []string
	for _, paths := range files {
		all = append(all, paths...)
	}
	sort.Strings(all)
	return all
}

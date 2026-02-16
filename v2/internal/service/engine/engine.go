package engine

import (
	"context"
	"crypto/md5"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/doITmagic/rag-code-mcp/v2/internal/service/indexer"
	"github.com/doITmagic/rag-code-mcp/v2/internal/service/search"
	"github.com/doITmagic/rag-code-mcp/v2/pkg/parser"
	"github.com/doITmagic/rag-code-mcp/v2/pkg/workspace"
)

// List of default markers to look for.
var defaultMarkers = []string{".git", "go.mod", "composer.json", "package.json", "requirements.txt", "pyproject.toml"}

// Engine is the high-level orchestrator for RAG operations.
type Engine struct {
indexer *indexer.Service
search  *search.Service
}

// NewEngine creates a new Engine instance.
func NewEngine(idx *indexer.Service, srv *search.Service) *Engine {
return &Engine{
indexer: idx,
search:  srv,
}
}

// WorkspaceContext provides information about a detected workspace.
type WorkspaceContext struct {
Root   string
ID     string
Branch string
}

// DetectContext detects the workspace context for a given path.
func (e *Engine) DetectContext(ctx context.Context, path string) (*WorkspaceContext, error) {
res, err := workspace.FindRoot(ctx, path, defaultMarkers, 10)
if err != nil {
return nil, fmt.Errorf("failed to find workspace root: %w", err)
}
if res == nil {
// Fallback to the directory itself if no marker found
abs, _ := filepath.Abs(path)
res = &workspace.Result{Root: abs}
}

state, _ := workspace.GetState(ctx, res.Root)
branch := "main"
if state != nil {
branch = state.Branch
}

id := fmt.Sprintf("%x", md5.Sum([]byte(res.Root)))[:8]

return &WorkspaceContext{
Root:   res.Root,
ID:     id,
Branch: branch,
}, nil
}

// IndexWorkspace indexes all files in a workspace.
func (e *Engine) IndexWorkspace(ctx context.Context, path string) error {
wctx, err := e.DetectContext(ctx, path)
if err != nil {
return err
}

// Walk and group symbols
langSymbols := make(map[string][]parser.Symbol)

err = filepath.WalkDir(wctx.Root, func(p string, d os.DirEntry, err error) error {
if err != nil {
return nil
}
if d.IsDir() {
// Skip hidden dirs (like .git, .vendor, .venv)
if strings.HasPrefix(d.Name(), ".") || d.Name() == "vendor" || d.Name() == "node_modules" || d.Name() == "venv" {
return filepath.SkipDir
}
return nil
}

analyzer := parser.GetByFile(p)
if analyzer == nil {
return nil
}

res, err := analyzer.Analyze(ctx, p)
if err != nil {
return nil
}

langSymbols[analyzer.Name()] = append(langSymbols[analyzer.Name()], res.Symbols...)
return nil
})

if err != nil {
return err
}

for lang, symbols := range langSymbols {
collection := fmt.Sprintf("ragcode-%s-%s", wctx.ID, lang)
// Add branch to metadata for each symbol before indexing
for i := range symbols {
if symbols[i].Metadata == nil {
symbols[i].Metadata = make(map[string]any)
}
symbols[i].Metadata["branch"] = wctx.Branch
}

if err := e.indexer.IndexItems(ctx, collection, symbols); err != nil {
return fmt.Errorf("failed to index %s: %w", lang, err)
}
}

return nil
}

// Query performs a search across common language collections in a workspace.
func (e *Engine) Query(ctx context.Context, path string, queryText string, limit int) ([]any, error) {
wctx, err := e.DetectContext(ctx, path)
if err != nil {
return nil, err
}

langs := []string{"go", "php", "python"}
var allResults []any

for _, lang := range langs {
collection := fmt.Sprintf("ragcode-%s-%s", wctx.ID, lang)
res, err := e.search.Search(ctx, collection, queryText, limit)
if err != nil {
continue // Collection might not exist
}
for _, r := range res {
allResults = append(allResults, r)
}
}

return allResults, nil
}

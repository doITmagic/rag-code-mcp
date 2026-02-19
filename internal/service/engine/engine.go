package engine

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/doITmagic/rag-code-mcp/internal/config"
	"github.com/doITmagic/rag-code-mcp/internal/service/indexer"
	"github.com/doITmagic/rag-code-mcp/internal/service/search"
	"github.com/doITmagic/rag-code-mcp/pkg/parser"
	"github.com/doITmagic/rag-code-mcp/pkg/storage"
	"github.com/doITmagic/rag-code-mcp/pkg/workspace/branchstate"
	"github.com/doITmagic/rag-code-mcp/pkg/workspace/contract"
	"github.com/doITmagic/rag-code-mcp/pkg/workspace/detector"
	"github.com/doITmagic/rag-code-mcp/pkg/workspace/registry"
	"github.com/doITmagic/rag-code-mcp/pkg/workspace/resolver"
)

// Engine is the high-level orchestrator for RAG operations.
type Engine struct {
	indexer  *indexer.Service
	search   *search.Service
	resolver *resolver.Resolver
	config   *config.Config

	// indexingJobs tracks active background indexing jobs.
	// Key: workspace ID, Value: start time
	indexingJobs sync.Map
}

// NewEngine creates a new Engine with all workspace dependencies wired up.
// registryPath is the path to the persistent registry file (e.g. ~/.ragcode/registry.json).
func NewEngine(idx *indexer.Service, srv *search.Service, registryPath string, cfg *config.Config) *Engine {
	det := detector.New(detector.DefaultOptions())
	branchMgr := branchstate.NewManager()

	var reg *registry.Registry
	if registryPath != "" {
		if r, err := registry.New(registryPath); err == nil {
			reg = r
		}
	}

	res := resolver.New(resolver.Dependencies{
		Detector:        det,
		Registry:        reg,
		BranchAnnotator: branchMgr,
	})

	return &Engine{
		indexer:  idx,
		search:   srv,
		resolver: res,
		config:   cfg,
	}
}

// Config returns the engine configuration.
func (e *Engine) Config() *config.Config {
	return e.config
}

// WorkspaceContext provides information about a detected workspace.
type WorkspaceContext struct {
	Root            string
	ID              string
	Branch          string
	WorktreeID      string
	MismatchRisk    string
	DetectionSource string // e.g., "explicit_file_path", "registry_fallback"
}

// DetectContext resolves the workspace context for a given path using the full resolver cascade.
// If path is empty, it falls back to the last active workspace from the registry.
func (e *Engine) DetectContext(ctx context.Context, path string) (*WorkspaceContext, error) {
	req := contract.ResolveWorkspaceRequest{}
	source := "explicit_file_path"

	if strings.TrimSpace(path) == "" {
		active, err := e.GetActiveWorkspace()
		if err != nil || active == "" {
			return nil, fmt.Errorf("❌ No workspace detected. Please provide the 'file_path' of the file you are currently working on to help detect the project context.")
		}
		req.WorkspaceRoot = active
		source = "registry_fallback"
	} else {
		abs, err := filepath.Abs(path)
		if err != nil {
			return nil, fmt.Errorf("failed to resolve path: %w", err)
		}
		req.FilePath = abs
	}

	resp, wsErr := e.resolver.Resolve(ctx, req)
	if wsErr != nil {
		return nil, fmt.Errorf("workspace detection failed: %s", wsErr.Message)
	}

	return &WorkspaceContext{
		Root:            resp.ResolvedRoot,
		ID:              resp.WorkspaceID,
		Branch:          resp.Branch,
		WorktreeID:      resp.WorktreeID,
		MismatchRisk:    resp.MismatchRisk,
		DetectionSource: source,
	}, nil
}

// GetActiveWorkspace returns the last confirmed workspace root from the resolver.
func (e *Engine) GetActiveWorkspace() (string, error) {
	return e.resolver.GetActiveWorkspace()
}

// SearchCodeResult wraps search results with workspace context.
type SearchCodeResult struct {
	Results         []storage.SearchResult
	WorkspaceRoot   string
	Collection      string
	Language        string
	MismatchRisk    string
	DetectionSource string
}

// ErrNotIndexed is returned when a workspace collection doesn't exist yet and indexing hasn't started.
type ErrNotIndexed struct {
	WorkspaceRoot string
	Collection    string
	Language      string
}

func (e *ErrNotIndexed) Error() string {
	return fmt.Sprintf("workspace '%s' is not indexed yet (collection: %s)", e.WorkspaceRoot, e.Collection)
}

// ErrIndexingInProgress is returned when a workspace is currently being indexed.
type ErrIndexingInProgress struct {
	WorkspaceRoot string
}

func (e *ErrIndexingInProgress) Error() string {
	return fmt.Sprintf("indexing in progress for %s", e.WorkspaceRoot)
}

// ErrIndexingStarted is returned when indexing was automatically triggered.
type ErrIndexingStarted struct {
	WorkspaceRoot string
}

func (e *ErrIndexingStarted) Error() string {
	return fmt.Sprintf("started background indexing for %s", e.WorkspaceRoot)
}

// SearchCode detects the workspace from filePath, resolves the correct collection,
// and performs a semantic search. includeDocs=false searches code only.
// If the collection does not exist, it triggers background indexing.
func (e *Engine) SearchCode(ctx context.Context, filePath, queryText string, limit int, includeDocs bool) (*SearchCodeResult, error) {
	wctx, err := e.DetectContext(ctx, filePath)
	if err != nil {
		return nil, err
	}

	// Detect language from file extension using the parser registry
	lang := "go" // default fallback
	if a := parser.GetByFile(filePath); a != nil {
		lang = a.Name()
	}

	collection := fmt.Sprintf("ragcode-%s-%s", wctx.ID, lang)

	exists, err := e.search.CollectionExists(ctx, collection)
	if err != nil {
		return nil, fmt.Errorf("failed to check collection: %w", err)
	}

	if !exists {
		// Check if already indexing
		if _, ok := e.indexingJobs.Load(wctx.ID); ok {
			return nil, &ErrIndexingInProgress{WorkspaceRoot: wctx.Root}
		}

		// Trigger background indexing
		e.StartIndexingAsync(wctx.Root, wctx.ID)
		return nil, &ErrIndexingStarted{WorkspaceRoot: wctx.Root}
	}

	var results []storage.SearchResult
	if includeDocs {
		results, err = e.search.Search(ctx, collection, queryText, limit)
	} else {
		results, err = e.search.SearchCodeOnly(ctx, collection, queryText, limit)
	}
	if err != nil {
		return nil, fmt.Errorf("search failed: %w", err)
	}

	return &SearchCodeResult{
		Results:         results,
		WorkspaceRoot:   wctx.Root,
		Collection:      collection,
		Language:        lang,
		MismatchRisk:    wctx.MismatchRisk,
		DetectionSource: wctx.DetectionSource,
	}, nil
}

// StartIndexingAsync starts the indexing process in a background goroutine.
func (e *Engine) StartIndexingAsync(root, id string) {
	if _, loaded := e.indexingJobs.LoadOrStore(id, time.Now()); loaded {
		return // Already running
	}

	go func() {
		defer e.indexingJobs.Delete(id)
		log.Printf("[INFO] 🚀 Starting background indexing for: %s", root)

		// Create a detached context for the background job
		ctx := context.Background()

		if err := e.IndexWorkspace(ctx, root); err != nil {
			log.Printf("[ERROR] Background indexing failed for %s: %v", root, err)
		} else {
			log.Printf("[INFO] ✅ Background indexing completed for: %s", root)
		}
	}()
}

// IndexWorkspace indexes all files in a workspace.
func (e *Engine) IndexWorkspace(ctx context.Context, path string) error {
	wctx, err := e.DetectContext(ctx, path)
	if err != nil {
		return err
	}

	langSymbols := make(map[string][]parser.Symbol)

	err = filepath.WalkDir(wctx.Root, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			name := d.Name()
			if strings.HasPrefix(name, ".") || name == "vendor" || name == "node_modules" ||
				name == "venv" || name == "__pycache__" || name == "dist" || name == "build" {
				return filepath.SkipDir
			}
			return nil
		}

		a := parser.GetByFile(p)
		if a == nil {
			return nil
		}

		res, err := a.Analyze(ctx, p)
		if err != nil {
			return nil
		}

		// Ensure map entry exists
		if _, ok := langSymbols[a.Name()]; !ok {
			langSymbols[a.Name()] = make([]parser.Symbol, 0)
		}
		langSymbols[a.Name()] = append(langSymbols[a.Name()], res.Symbols...)
		return nil
	})
	if err != nil {
		return err
	}

	for lang, symbols := range langSymbols {
		collection := fmt.Sprintf("ragcode-%s-%s", wctx.ID, lang)
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

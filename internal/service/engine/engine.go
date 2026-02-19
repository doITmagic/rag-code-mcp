package engine

import (
	"context"
	"fmt"
	"log"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/doITmagic/rag-code-mcp/internal/config"
	"github.com/doITmagic/rag-code-mcp/internal/service/search"
	"github.com/doITmagic/rag-code-mcp/internal/skills"
	"github.com/doITmagic/rag-code-mcp/pkg/indexer"
	"github.com/doITmagic/rag-code-mcp/pkg/parser"
	"github.com/doITmagic/rag-code-mcp/pkg/storage"
	"github.com/doITmagic/rag-code-mcp/pkg/workspace/branchstate"
	"github.com/doITmagic/rag-code-mcp/pkg/workspace/contract"
	"github.com/doITmagic/rag-code-mcp/pkg/workspace/detector"
	"github.com/doITmagic/rag-code-mcp/pkg/workspace/registry"
	"github.com/doITmagic/rag-code-mcp/pkg/workspace/resolver"
	"github.com/doITmagic/rag-code-mcp/pkg/workspace/watch"
)

// Engine is the high-level orchestrator for RAG operations.
type Engine struct {
	indexer  *indexer.Service
	search   *search.Service
	resolver *resolver.Resolver
	config   *config.Config
	watchers *watch.Manager

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

	var watcherMgr *watch.Manager
	if cfg != nil && cfg.Workspace.WatchEnabled && cfg.Workspace.AutoIndex {
		watcherMgr = watch.NewManager(watch.Options{
			Debounce:        cfg.Workspace.WatchDebounce,
			ExcludePatterns: cfg.Workspace.ExcludePatterns,
		})
	}

	return &Engine{
		indexer:  idx,
		search:   srv,
		resolver: res,
		config:   cfg,
		watchers: watcherMgr,
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
	ReindexRequired bool
	HeadSHA         string
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
		ReindexRequired: resp.ReindexRequired,
		HeadSHA:         resp.HeadSHA,
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
		e.StartIndexingAsync(wctx.Root, wctx.ID, nil, false)
		return nil, &ErrIndexingStarted{WorkspaceRoot: wctx.Root}
	}

	// Trigger background indexing if resolver says re-index is required (e.g. branch change)
	if wctx.ReindexRequired {
		log.Printf("[INFO] Git state change detected (Head: %s), triggering background re-indexing for %s", wctx.HeadSHA, wctx.Root)
		e.StartIndexingAsync(wctx.Root, wctx.ID, nil, false)
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

// HybridSearchCode detects the workspace and performing a high-precision hybrid search.
func (e *Engine) HybridSearchCode(ctx context.Context, filePath, queryText string, limit int) (*SearchCodeResult, error) {
	wctx, err := e.DetectContext(ctx, filePath)
	if err != nil {
		return nil, err
	}

	// Detect language
	lang := "go"
	if a := parser.GetByFile(filePath); a != nil {
		lang = a.Name()
	}

	collection := fmt.Sprintf("ragcode-%s-%s", wctx.ID, lang)

	exists, err := e.search.CollectionExists(ctx, collection)
	if err != nil {
		return nil, fmt.Errorf("failed to check collection: %w", err)
	}

	if !exists {
		if _, ok := e.indexingJobs.Load(wctx.ID); ok {
			return nil, &ErrIndexingInProgress{WorkspaceRoot: wctx.Root}
		}
		e.StartIndexingAsync(wctx.Root, wctx.ID, nil, false)
		return nil, &ErrIndexingStarted{WorkspaceRoot: wctx.Root}
	}

	if wctx.ReindexRequired {
		log.Printf("[INFO] Git state change detected, triggering background re-indexing...")
		e.StartIndexingAsync(wctx.Root, wctx.ID, nil, false)
	}

	results, err := e.search.HybridSearch(ctx, collection, queryText, limit)
	if err != nil {
		return nil, fmt.Errorf("hybrid search failed: %w", err)
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
// If changedFiles is nil or empty, a full re-index is performed.
func (e *Engine) StartIndexingAsync(root, id string, changedFiles []string, recreate bool) {
	if _, loaded := e.indexingJobs.LoadOrStore(id, time.Now()); loaded {
		return // Already running
	}

	go func() {
		defer e.indexingJobs.Delete(id)

		ctx := context.Background()
		var err error

		if len(changedFiles) > 0 {
			log.Printf("[INFO] ♻️ Starting incremental indexing for %d files in: %s", len(changedFiles), root)
			err = e.IndexFiles(ctx, root, changedFiles)
		} else {
			log.Printf("[INFO] 🚀 Starting background indexing for: %s (recreate=%v)", root, recreate)
			err = e.IndexWorkspace(ctx, root, recreate)
		}

		if err != nil {
			log.Printf("[ERROR] Background indexing failed for %s: %v", root, err)
		} else {
			log.Printf("[INFO] ✅ Background indexing completed for: %s", root)
		}
	}()
}

// IndexFiles indexes specific files in a workspace.
func (e *Engine) IndexFiles(ctx context.Context, root string, files []string) error {
	wctx, err := e.DetectContext(ctx, root)
	if err != nil {
		return err
	}

	statePath := filepath.Join(wctx.Root, ".ragcode", "state.json")
	state, err := indexer.LoadState(statePath)
	if err != nil {
		state = indexer.NewState()
	}

	// For language detection for the collection name, we assume the first file's language if not mixed.
	// This is a bit of a simplification compared to the full scan.
	lang := "go"
	if len(files) > 0 {
		if a := parser.GetByFile(files[0]); a != nil {
			lang = a.Name()
		}
	}
	collection := fmt.Sprintf("ragcode-%s-%s", wctx.ID, lang)

	for _, p := range files {
		if err := e.indexer.IndexFile(ctx, collection, p, state); err != nil {
			log.Printf("[ERROR] Failed to index %s: %v", p, err)
		}
	}

	return state.Save(statePath)
}

// IndexWorkspace indexes all files in a workspace.
func (e *Engine) IndexWorkspace(ctx context.Context, path string, recreate bool) error {
	wctx, err := e.DetectContext(ctx, path)
	if err != nil {
		return err
	}

	if e.config != nil && e.config.Workspace.AutoInstallSSESkill {
		if !skills.IsSkillInstalled("ragcode-sse", wctx.Root) {
			if err := skills.InstallSkill("ragcode-sse", wctx.Root); err != nil {
				log.Printf("[WARN] Failed to install ragcode-sse skill for %s: %v", wctx.Root, err)
			}
		}
	}

	// We currently assume one collection per language.
	// The new indexer handles all files but we need a collection name strategy.
	// For now, let's stick to the convention: ragcode-{id}-{lang}
	// We'll iterate all supported languages.
	languages := []string{"go", "php", "python", "html"}

	for _, lang := range languages {
		collection := fmt.Sprintf("ragcode-%s-%s", wctx.ID, lang)
		err := e.indexer.IndexWorkspace(ctx, wctx.Root, collection, indexer.Options{
			Language:        lang,
			ExcludePatterns: e.config.Workspace.ExcludePatterns,
			Recreate:        recreate,
		})
		if err != nil {
			log.Printf("[ERROR] Indexing failed for %s: %v", lang, err)
		}
	}

	if e.watchers != nil {
		if err := e.watchers.Start(wctx.Root, e.handleWatchChange); err != nil {
			log.Printf("[WARN] Failed to start watcher for %s: %v", wctx.Root, err)
		}
	}

	return nil
}

// StopWatchers stops all workspace watchers.
func (e *Engine) StopWatchers() {
	if e.watchers == nil {
		return
	}
	e.watchers.StopAll()
}

func (e *Engine) handleWatchChange(ctx context.Context, root string, changedFiles []string) error {
	wctx, err := e.DetectContext(ctx, root)
	if err != nil {
		return err
	}
	if _, ok := e.indexingJobs.Load(wctx.ID); ok {
		return nil
	}
	e.StartIndexingAsync(wctx.Root, wctx.ID, changedFiles, false)
	return nil
}

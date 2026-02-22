package engine

import (
	"context"
	"fmt"
	"log"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
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

	// detectionCache stores resolved WorkspaceContext with TTL to avoid
	// repeated full resolver cascades for the same path.
	detectionCache sync.Map // map[string]*detectionCacheEntry
}

// detectionCacheEntry wraps a cached WorkspaceContext with an expiry.
type detectionCacheEntry struct {
	wctx   *WorkspaceContext
	expiry time.Time
}

const detectionCacheTTL = 5 * time.Second

func (e *Engine) GetSearchService() *search.Service {
	return e.search
}

// SetResolver replaces the workspace resolver (primarily for testing).
func (e *Engine) SetResolver(res *resolver.Resolver) {
	e.resolver = res
}

// SetSearchService replaces the search service (primarily for testing).
func (e *Engine) SetSearchService(srv *search.Service) {
	e.search = srv
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

// CollectionNameFor returns the Qdrant collection name for a workspace ID and language.
func CollectionNameFor(wsID, lang string) string {
	return fmt.Sprintf("ragcode-%s-%s", wsID, lang)
}

// CollectionName returns the Qdrant collection name for the given language.
func (w *WorkspaceContext) CollectionName(lang string) string {
	return CollectionNameFor(w.ID, lang)
}

// DetectFromParams resolves workspace context from a tool args map.
// Reads file_path, workspace_root, or workspace keys (in that priority order).
func (e *Engine) DetectFromParams(ctx context.Context, params map[string]interface{}) (*WorkspaceContext, error) {
	for _, key := range []string{"file_path", "workspace_root", "workspace"} {
		if v, ok := params[key].(string); ok && strings.TrimSpace(v) != "" {
			return e.DetectContext(ctx, v)
		}
	}
	return e.DetectContext(ctx, "")
}

// DetectContext resolves the workspace context for a given path using the full resolver cascade.
// If path is empty, it falls back to the last active workspace from the registry.
// Results are cached with a 5s TTL to avoid redundant resolver invocations.
func (e *Engine) DetectContext(ctx context.Context, path string) (*WorkspaceContext, error) {
	// Normalize cache key
	cacheKey := strings.TrimSpace(path)
	if entry, ok := e.detectionCache.Load(cacheKey); ok {
		ce := entry.(*detectionCacheEntry)
		if time.Now().Before(ce.expiry) {
			return ce.wctx, nil
		}
		e.detectionCache.Delete(cacheKey)
	}

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

	wctx := &WorkspaceContext{
		Root:            resp.ResolvedRoot,
		ID:              resp.WorkspaceID,
		Branch:          resp.Branch,
		WorktreeID:      resp.WorktreeID,
		MismatchRisk:    resp.MismatchRisk,
		DetectionSource: source,
		ReindexRequired: resp.ReindexRequired,
		HeadSHA:         resp.HeadSHA,
	}

	// Don't cache entries that require reindex or are high-risk — let next call re-evaluate
	if !wctx.ReindexRequired && wctx.MismatchRisk != "high" {
		e.detectionCache.Store(cacheKey, &detectionCacheEntry{
			wctx:   wctx,
			expiry: time.Now().Add(detectionCacheTTL),
		})
	}

	return wctx, nil
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

// ErrNoCollectionsFound is returned by ExactSearchPolyglot when no collections
// exist for the given workspace, meaning the workspace has not been indexed yet.
type ErrNoCollectionsFound struct {
	WorkspaceID string
}

func (e *ErrNoCollectionsFound) Error() string {
	return fmt.Sprintf("no indexed collections found for workspace %q — run indexing first", e.WorkspaceID)
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

	collection := wctx.CollectionName(lang)

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

	collection := wctx.CollectionName(lang)

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

// ExactSearchPolyglot performs a metadata-only filter scan (Qdrant Scroll) across
// ALL language collections for a workspace in parallel.
// No embedding is generated — results are fully deterministic and fast.
// Use this for find_usages, call_hierarchy, package exports, and graph expansion.
//
// Returns ErrNoCollectionsFound when the workspace has no indexed collections yet.
func (e *Engine) ExactSearchPolyglot(ctx context.Context, wsID string, filters map[string]interface{}, limit int) ([]storage.SearchResult, error) {
	langs := parser.SupportedLanguages()
	if len(langs) == 0 {
		langs = []string{"go", "python", "php", "html"}
	}

	type trial struct {
		results []storage.SearchResult
	}

	resultsChan := make(chan trial, len(langs))
	var wg sync.WaitGroup
	var existingCollections int32

	for _, lang := range langs {
		collection := CollectionNameFor(wsID, lang)
		wg.Add(1)
		go func(coll string) {
			defer wg.Done()

			exists, err := e.search.CollectionExists(ctx, coll)
			if err != nil {
				log.Printf("[WARN] ExactSearchPolyglot: cannot check collection %s: %v", coll, err)
				return
			}
			if !exists {
				return
			}
			atomic.AddInt32(&existingCollections, 1)

			res, sErr := e.search.ExactSearch(ctx, coll, filters, limit)
			if sErr != nil {
				log.Printf("[WARN] ExactSearchPolyglot: search failed for %s: %v", coll, sErr)
				return
			}
			resultsChan <- trial{results: res}
		}(collection)
	}

	wg.Wait()
	close(resultsChan)

	if atomic.LoadInt32(&existingCollections) == 0 {
		return nil, &ErrNoCollectionsFound{WorkspaceID: wsID}
	}

	var all []storage.SearchResult
	for t := range resultsChan {
		all = append(all, t.results...)
	}
	return all, nil
}

// SearchByName performs a high-precision metadata-only lookup for a symbol by name.
// It bypasses the LLM embedder completely — ideal for graph expansion and call hierarchy.
// Equivalent to ExactSearchPolyglot with filter {"name": name}.
func (e *Engine) SearchByName(ctx context.Context, wsID, name string, limit int) ([]storage.SearchResult, error) {
	return e.ExactSearchPolyglot(ctx, wsID, map[string]interface{}{"name": name}, limit)
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
	collection := wctx.CollectionName(lang)

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

	// We'll iterate all supported languages.
	languages := parser.SupportedLanguages()

	for _, lang := range languages {
		collection := wctx.CollectionName(lang)
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

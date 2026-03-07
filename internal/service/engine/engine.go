package engine

import (
	"context"
	"fmt"
	"log"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/doITmagic/rag-code-mcp/internal/config"
	"github.com/doITmagic/rag-code-mcp/internal/service/search"
	"github.com/doITmagic/rag-code-mcp/internal/transport"
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

	// pendingIndex tracks file changes received while an indexing job is running.
	// It ensures watcher-triggered incremental indexing is lossless under rapid edits.
	pendingMu       sync.Mutex
	pendingFiles    map[string]map[string]struct{} // workspaceID -> set(filePath)
	pendingOverflow map[string]bool                // workspaceID -> too many pending changes, fallback to full scan

	progress *progressStore

	// detectionCache stores resolved WorkspaceContext with TTL to avoid
	// repeated full resolver cascades for the same path.
	detectionCache sync.Map // map[string]*detectionCacheEntry

	// resumeAttempts throttles auto-resume of interrupted indexing.
	// Key: workspace ID, Value: time.Time of last resume attempt.
	// Prevents CPU/log churn when indexing keeps failing (e.g. Ollama down).
	resumeAttempts sync.Map
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
		indexer:         idx,
		search:          srv,
		resolver:        res,
		config:          cfg,
		watchers:        watcherMgr,
		progress:        newProgressStore(),
		pendingFiles:    make(map[string]map[string]struct{}),
		pendingOverflow: make(map[string]bool),
	}
}

// GetIndexProgress returns the last known indexing progress for a workspace.
// workspaceRoot is used as a hint to load persisted status from disc if not in memory.
func (e *Engine) GetIndexProgress(workspaceID, workspaceRoot string) *IndexProgress {
	if e.progress == nil {
		return nil
	}
	return e.progress.get(workspaceID, workspaceRoot)
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
	if entry, ok := e.detectionCache.LoadAndDelete(cacheKey); ok {
		ce := entry.(*detectionCacheEntry)
		if time.Now().Before(ce.expiry) {
			// Re-store valid entry atomically before returning
			e.detectionCache.Store(cacheKey, ce)
			return ce.wctx, nil
		}
	}

	req := contract.ResolveWorkspaceRequest{}
	source := "explicit_file_path"

	if strings.TrimSpace(path) == "" {
		// Tier 2: Try workspace hint from adapter (IDE's CWD injected via X-Workspace-Hint header)
		if hint := transport.GetWorkspaceHint(ctx); hint != "" {
			abs, err := filepath.Abs(hint)
			if err == nil {
				req.FilePath = abs
				source = "hint_fallback"
			}
		}

		// Tier 3: Fall back to last active workspace from registry
		if req.FilePath == "" {
			active, err := e.GetActiveWorkspace()
			if err != nil || active == "" {
				return nil, fmt.Errorf("❌ No workspace detected. Please provide the 'file_path' of the file you are currently working on to help detect the project context.")
			}
			req.WorkspaceRoot = active
			source = "registry_fallback"
		}
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
	WorkspaceID     string
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
	WorkspaceID   string
	LastPercent   int
}

func (e *ErrIndexingInProgress) Error() string {
	if e.LastPercent > 0 && e.LastPercent < 100 {
		return fmt.Sprintf("indexing in progress for %s (resuming from %d%%)", e.WorkspaceRoot, e.LastPercent)
	}
	return fmt.Sprintf("indexing in progress for %s", e.WorkspaceRoot)
}

// ErrIndexingStarted is returned when indexing was automatically triggered.
type ErrIndexingStarted struct {
	WorkspaceRoot string
	WorkspaceID   string
}

func (e *ErrIndexingStarted) Error() string {
	return fmt.Sprintf("started background indexing for %s", e.WorkspaceRoot)
}

// SearchCode detects the workspace from filePath, resolves the correct collection,
// embeds the query ONCE, then fans out in parallel to all language collections.
// includeDocs=false searches code only. Triggers background indexing if needed.
func (e *Engine) SearchCode(ctx context.Context, filePath, queryText string, limit int, includeDocs bool) (*SearchCodeResult, error) {
	t0 := time.Now()

	wctx, err := e.DetectContext(ctx, filePath)
	if err != nil {
		return nil, err
	}
	log.Printf("[TIMER] SearchCode detect_context=%v", time.Since(t0))

	// Primary language from file extension
	primaryLang := "go"
	if a := parser.GetByFile(filePath); a != nil {
		primaryLang = a.Name()
	}

	// Ensure at least the primary collection exists before embedding (fast fail + indexing trigger)
	primaryColl := wctx.CollectionName(primaryLang)
	t1 := time.Now()

	indexerStatePath := filepath.Join(wctx.Root, ".ragcode", "state.json")
	if idxState, sErr := indexer.LoadState(indexerStatePath); sErr == nil {
		if idxState.LastPercent > 0 && idxState.LastPercent < 100 {
			if _, ok := e.indexingJobs.Load(wctx.ID); !ok {
				// Throttle auto-resume: only once per 5 minutes per workspace to avoid
				// CPU/log churn when indexing keeps failing (e.g. Ollama is down).
				const resumeCooldown = 5 * time.Minute
				now := time.Now()
				if last, loaded := e.resumeAttempts.Load(wctx.ID); !loaded || now.Sub(last.(time.Time)) > resumeCooldown {
					e.resumeAttempts.Store(wctx.ID, now)
					log.Printf("[INFO] Detectată indexare întreruptă (rămasă la %d%%). Se reia automat...", idxState.LastPercent)
					e.StartIndexingAsync(wctx.Root, wctx.ID, nil, false)
				}
			}
			// În ambele cazuri continuăm — Qdrant are deja date parțiale, iar progresul este adăugat în răspuns la nivelul MCP tool-ului
			log.Printf("[INFO] Indexing in progress (%d%%) — searching available results in Qdrant", idxState.LastPercent)
		}
	}

	exists, err := e.search.CollectionExists(ctx, primaryColl)
	log.Printf("[TIMER] SearchCode collection_exists_primary=%v (cached=%v)", time.Since(t1), time.Since(t1) < time.Millisecond)
	if err != nil {
		return nil, fmt.Errorf("failed to check collection: %w", err)
	}
	if !exists {
		if _, ok := e.indexingJobs.Load(wctx.ID); ok {
			return nil, &ErrIndexingInProgress{WorkspaceRoot: wctx.Root, WorkspaceID: wctx.ID}
		}
		e.StartIndexingAsync(wctx.Root, wctx.ID, nil, false)
		return nil, &ErrIndexingStarted{WorkspaceRoot: wctx.Root, WorkspaceID: wctx.ID}
	}

	if wctx.ReindexRequired {
		log.Printf("[INFO] Git state change detected (Head: %s), triggering background re-indexing for %s", wctx.HeadSHA, wctx.Root)
		e.StartIndexingAsync(wctx.Root, wctx.ID, nil, false)
	}

	// Embed ONCE, fan-out to all language collections in parallel
	langs := parser.SupportedLanguages()
	if len(langs) == 0 {
		log.Printf("[WARN] No parsers registered — SupportedLanguages() returned empty. Skipping fan-out.")
		return nil, fmt.Errorf("no language parsers registered")
	}

	t2 := time.Now()
	vector, err := e.search.EmbedQuery(ctx, queryText)
	log.Printf("[TIMER] SearchCode embed=%v", time.Since(t2))
	if err != nil {
		return nil, fmt.Errorf("embedding failed: %w", err)
	}

	type langResult struct {
		lang    string
		coll    string
		results []storage.SearchResult
		err     error
		elapsed time.Duration
	}

	resultsChan := make(chan langResult, len(langs))
	var wg sync.WaitGroup
	t3 := time.Now()

	for _, lang := range langs {
		coll := wctx.CollectionName(lang)
		wg.Add(1)
		go func(l, c string) {
			defer wg.Done()
			gt := time.Now()
			ok, chkErr := e.search.CollectionExists(ctx, c)
			if chkErr != nil || !ok {
				return
			}
			var res []storage.SearchResult
			var sErr error
			if includeDocs {
				res, sErr = e.search.SearchWithVector(ctx, c, vector, limit)
			} else {
				res, sErr = e.search.SearchCodeWithVector(ctx, c, vector, limit)
			}
			elapsed := time.Since(gt)
			if sErr != nil {
				log.Printf("[WARN] SearchCode: fan-out failed for %s: %v", c, sErr)
				resultsChan <- langResult{lang: l, coll: c, err: sErr, elapsed: elapsed}
				return
			}
			if len(res) > 0 {
				resultsChan <- langResult{lang: l, coll: c, results: res, elapsed: elapsed}
			}
		}(lang, coll)
	}

	wg.Wait()
	close(resultsChan)
	log.Printf("[TIMER] SearchCode fanout_total=%v (langs=%d)", time.Since(t3), len(langs))

	// Merge: primary lang results first, others appended; surface first error if no results
	var primaryResults []storage.SearchResult
	var otherResults []storage.SearchResult
	var firstErr error
	reportLang := primaryLang
	reportColl := primaryColl

	for lr := range resultsChan {
		if lr.err != nil {
			log.Printf("[TIMER] SearchCode lang=%s err elapsed=%v", lr.lang, lr.elapsed)
			if firstErr == nil {
				firstErr = lr.err
			}
			continue
		}
		log.Printf("[TIMER] SearchCode lang=%s hits=%d elapsed=%v", lr.lang, len(lr.results), lr.elapsed)
		if lr.coll == primaryColl {
			primaryResults = lr.results
		} else {
			otherResults = append(otherResults, lr.results...)
		}
	}

	all := append(primaryResults, otherResults...)

	// Global sort by score descending across all language results, then cap to limit.
	// Without this, primary-language results always win regardless of score.
	sort.Slice(all, func(i, j int) bool { return all[i].Score > all[j].Score })
	if limit > 0 && len(all) > limit {
		all = all[:limit]
	}

	// If nothing was found and there were errors, surface the error
	if len(all) == 0 && firstErr != nil {
		return nil, fmt.Errorf("search failed: %w", firstErr)
	}

	log.Printf("[TIMER] SearchCode TOTAL=%v (detect=%v embed=%v fanout=%v)",
		time.Since(t0), t1.Sub(t0), t2.Sub(t1), time.Since(t3))

	return &SearchCodeResult{
		Results:         all,
		WorkspaceRoot:   wctx.Root,
		WorkspaceID:     wctx.ID,
		Collection:      reportColl,
		Language:        reportLang,
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

	indexerStatePath := filepath.Join(wctx.Root, ".ragcode", "state.json")
	if idxState, sErr := indexer.LoadState(indexerStatePath); sErr == nil {
		if idxState.LastPercent > 0 && idxState.LastPercent < 100 {
			if _, ok := e.indexingJobs.Load(wctx.ID); !ok {
				// Throttle auto-resume: only once per 5 minutes per workspace to avoid
				// CPU/log churn when indexing keeps failing (e.g. Ollama is down).
				const resumeCooldown = 5 * time.Minute
				now := time.Now()
				if last, loaded := e.resumeAttempts.Load(wctx.ID); !loaded || now.Sub(last.(time.Time)) > resumeCooldown {
					e.resumeAttempts.Store(wctx.ID, now)
					log.Printf("[INFO] Detectată indexare întreruptă (rămasă la %d%%). Se reia automat...", idxState.LastPercent)
					e.StartIndexingAsync(wctx.Root, wctx.ID, nil, false)
				}
			}
			// În ambele cazuri continuăm — Qdrant are deja date parțiale, iar progresul este adăugat în răspuns la nivelul MCP tool-ului
			log.Printf("[INFO] Indexing in progress (%d%%) — searching available results in Qdrant", idxState.LastPercent)
		}
	}

	exists, err := e.search.CollectionExists(ctx, collection)
	if err != nil {
		return nil, fmt.Errorf("failed to check collection: %w", err)
	}

	if !exists {
		if _, ok := e.indexingJobs.Load(wctx.ID); ok {
			return nil, &ErrIndexingInProgress{WorkspaceRoot: wctx.Root, WorkspaceID: wctx.ID}
		}
		e.StartIndexingAsync(wctx.Root, wctx.ID, nil, false)
		return nil, &ErrIndexingStarted{WorkspaceRoot: wctx.Root, WorkspaceID: wctx.ID}
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
		WorkspaceID:     wctx.ID,
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
		log.Printf("[WARN] No parsers registered — SupportedLanguages() returned empty. Skipping ExactSearch.")
		return nil, &ErrNoCollectionsFound{WorkspaceID: wsID}
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

const pendingIndexMaxFiles = 200

func (e *Engine) addPendingIndexFiles(workspaceID string, files []string) {
	e.pendingMu.Lock()
	defer e.pendingMu.Unlock()

	if e.pendingOverflow[workspaceID] {
		return
	}

	set, ok := e.pendingFiles[workspaceID]
	if !ok {
		set = make(map[string]struct{})
		e.pendingFiles[workspaceID] = set
	}

	for _, f := range files {
		if strings.TrimSpace(f) == "" {
			continue
		}
		set[f] = struct{}{}
		if len(set) > pendingIndexMaxFiles {
			e.pendingOverflow[workspaceID] = true
			delete(e.pendingFiles, workspaceID)
			return
		}
	}
}

func (e *Engine) popPendingIndex(workspaceID string) (files []string, overflow bool) {
	e.pendingMu.Lock()
	defer e.pendingMu.Unlock()

	overflow = e.pendingOverflow[workspaceID]
	delete(e.pendingOverflow, workspaceID)

	set, ok := e.pendingFiles[workspaceID]
	if !ok {
		return nil, overflow
	}
	delete(e.pendingFiles, workspaceID)

	files = make([]string, 0, len(set))
	for f := range set {
		files = append(files, f)
	}
	sort.Strings(files)
	return files, overflow
}

func (e *Engine) tryStartPendingIndex(root, workspaceID string) {
	files, overflow := e.popPendingIndex(workspaceID)
	if overflow {
		log.Printf("[INFO] ♻️ Pending changes exceeded limit for %s - triggering full scan incremental indexing", root)
		e.StartIndexingAsync(root, workspaceID, nil, false)
		return
	}
	if len(files) == 0 {
		return
	}
	e.StartIndexingAsync(root, workspaceID, files, false)
}

// StartIndexingAsync starts the indexing process in a background goroutine.
// If changedFiles is nil or empty, a full re-index is performed.
func (e *Engine) StartIndexingAsync(root, id string, changedFiles []string, recreate bool) {
	if _, loaded := e.indexingJobs.LoadOrStore(id, time.Now()); loaded {
		return // Already running
	}

	jobID := fmt.Sprintf("%s-%d", id, time.Now().UnixNano())
	if e.progress != nil {
		e.progress.start(id, root, jobID, time.Now())
	}

	go func() {
		defer func() {
			e.indexingJobs.Delete(id)
			// If watcher changes came in while we were indexing, run a follow-up incremental job.
			e.tryStartPendingIndex(root, id)
		}()

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
			if e.progress != nil {
				e.progress.fail(id, root, time.Now(), err.Error())
			}
		} else {
			log.Printf("[INFO] ✅ Background indexing completed for: %s", root)
			if e.progress != nil {
				e.progress.complete(id, root, time.Now())
			}
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
		if _, indexErr := e.indexer.IndexFile(ctx, collection, p, state); indexErr != nil {
			log.Printf("[ERROR] Failed to index %s: %v", p, indexErr)
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

	// Persist workspace in registry so it's available for future fallback
	// detection (e.g. when file_path is missing). This is idempotent —
	// updates LastUsedAt if the workspace is already registered.
	e.resolver.RegisterWorkspace(wctx.Root, filepath.Base(wctx.Root))

	if e.config != nil && e.config.Workspace.AutoInstallSSESkill {
		if !skills.IsSkillInstalled("ragcode-sse", wctx.Root) {
			if err := skills.InstallSkill("ragcode-sse", wctx.Root, "agent", e.config.Skills); err != nil {
				log.Printf("[WARN] Failed to install ragcode-sse skill for %s: %v", wctx.Root, err)
			}
		}
	}

	statePath := filepath.Join(wctx.Root, ".ragcode", "state.json")
	if state, err := indexer.LoadState(statePath); err == nil {
		state.SetLastPercent(1) // Marker for "indexer is globally running"
		_ = state.Save(statePath)
	}

	// We'll iterate all supported languages.
	languages := parser.SupportedLanguages()

	var indexErrors []string
	for _, lang := range languages {
		collection := wctx.CollectionName(lang)
		progressCb := func(doneFiles, totalFiles int) {
			if e.progress != nil {
				e.progress.update(wctx.ID, lang, doneFiles, totalFiles, time.Now())
			}
		}
		err := e.indexer.IndexWorkspace(ctx, wctx.Root, collection, indexer.Options{
			Language:        lang,
			ExcludePatterns: e.config.Workspace.ExcludePatterns,
			Recreate:        recreate,
			Progress:        progressCb,
		})
		if err != nil {
			log.Printf("[ERROR] Indexing failed for %s: %v", lang, err)
			indexErrors = append(indexErrors, fmt.Sprintf("%s: %v", lang, err))
		}
	}

	if state, err := indexer.LoadState(statePath); err == nil {
		state.SetLastPercent(100)
		_ = state.Save(statePath)
	}

	if e.watchers != nil {
		if err := e.watchers.Start(wctx.Root, e.handleWatchChange); err != nil {
			log.Printf("[WARN] Failed to start watcher for %s: %v", wctx.Root, err)
		}
	}

	if len(indexErrors) > 0 {
		return fmt.Errorf("indexing failed for %d language(s): %s. Check if Ollama is running and the embedding model is available",
			len(indexErrors), strings.Join(indexErrors, "; "))
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
		e.addPendingIndexFiles(wctx.ID, changedFiles)
		return nil
	}
	e.StartIndexingAsync(wctx.Root, wctx.ID, changedFiles, false)
	return nil
}

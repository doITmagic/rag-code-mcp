package engine

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/doITmagic/rag-code-mcp/internal/config"
	"github.com/doITmagic/rag-code-mcp/internal/logger"
	"github.com/doITmagic/rag-code-mcp/internal/service/search"
	"github.com/doITmagic/rag-code-mcp/internal/skills"
	"github.com/doITmagic/rag-code-mcp/internal/transport"
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



	// detectionCache stores resolved WorkspaceContext with TTL to avoid
	// repeated full resolver cascades for the same path.
	detectionCache sync.Map // map[string]*detectionCacheEntry



	// connectTriggered tracks whether background indexing was automatically
	// triggered for a workspace ID upon initial daemon resolution.
	connectTriggered sync.Map
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

		pendingFiles:    make(map[string]map[string]struct{}),
		pendingOverflow: make(map[string]bool),
	}
}

// GetIndexStatus returns the last known indexing status for a workspace.
// Reads directly from {workspaceRoot}/.ragcode/index_status.json.
func (e *Engine) GetIndexStatus(workspaceRoot string) *indexer.IndexStatus {
	return indexer.LoadIndexStatus(workspaceRoot)
}

// ActiveIndexingJobs returns the IDs of workspaces currently being indexed.
// Useful for health checks and debugging concurrent indexing scenarios.
func (e *Engine) ActiveIndexingJobs() []string {
	var ids []string
	e.indexingJobs.Range(func(k, _ any) bool {
		ids = append(ids, k.(string))
		return true
	})
	return ids
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
	// Log incoming detection request for debugging workspace resolution
	hintFromCtx := transport.GetWorkspaceHint(ctx)
	logger.Instance.Info("[WS-DETECT] ▶ DetectContext called: path=%q, X-Workspace-Hint=%q", path, hintFromCtx)

	// Normalize cache key
	cacheKey := strings.TrimSpace(path)
	if entry, ok := e.detectionCache.LoadAndDelete(cacheKey); ok {
		ce := entry.(*detectionCacheEntry)
		if time.Now().Before(ce.expiry) {
			// Re-store valid entry atomically before returning
			e.detectionCache.Store(cacheKey, ce)
			logger.Instance.Debug("[WS-DETECT] ◀ Cache hit: root=%s, source=%s", ce.wctx.Root, ce.wctx.DetectionSource)
			return ce.wctx, nil
		}
	}

	req := contract.ResolveWorkspaceRequest{}
	source := "explicit_file_path"

	if strings.TrimSpace(path) == "" {
		logger.Instance.Info("[WS-DETECT] Tier 1 skipped: no explicit path in request params")

		// Tier 2: Try workspace hint from adapter (IDE's CWD injected via X-Workspace-Hint header)
		if hint := strings.TrimSpace(hintFromCtx); hint != "" {
			abs, err := filepath.Abs(hint)
			if err == nil {
				// Validate the path actually exists on disk before using it
				if _, statErr := os.Stat(abs); statErr == nil {
					req.FilePath = abs
					source = "hint_fallback"
					// Use hint as cache key so different IDEs don't share the same empty-string entry
					cacheKey = abs
					logger.Instance.Info("[WS-DETECT] ✅ Tier 2 matched: using X-Workspace-Hint=%s", abs)
				} else {
					logger.Instance.Warn("[WS-DETECT] Tier 2 skipped: hint path does not exist on disk: %s (err: %v)", abs, statErr)
				}
			} else {
				logger.Instance.Warn("[WS-DETECT] Tier 2 skipped: could not resolve hint to abs path: %v", err)
			}
		} else {
			logger.Instance.Info("[WS-DETECT] Tier 2 skipped: no X-Workspace-Hint header in request")
		}

		// Tier 3: Fall back to last active workspace from registry
		if req.FilePath == "" {
			active, err := e.GetActiveWorkspace()
			if err != nil || active == "" {
				logger.Instance.Warn("[WS-DETECT] ❌ All tiers exhausted — no workspace detected")
				return nil, fmt.Errorf("❌ No workspace detected. Please provide the 'file_path' of the file you are currently working on to help detect the project context.")
			}
			req.WorkspaceRoot = active
			source = "registry_fallback"
			logger.Instance.Info("[WS-DETECT] ✅ Tier 3 matched: using registry fallback=%s", active)
		}
	} else {
		abs, err := filepath.Abs(path)
		if err != nil {
			return nil, fmt.Errorf("failed to resolve path: %w", err)
		}
		req.FilePath = abs
		logger.Instance.Info("[WS-DETECT] ✅ Tier 1 matched: using explicit path=%s", abs)
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

	// If the resolver applied a more specific override (e.g. nested_workspace_override),
	// surface it so the agent can see exactly what happened in the response.
	if resp.PathResolutionSource != "" && resp.PathResolutionSource != source {
		wctx.DetectionSource = resp.PathResolutionSource
		logger.Instance.Info("[DAEMON] [WS-DETECT] ◀ Resolver override: source changed %s → %s (root: %s)",
			source, resp.PathResolutionSource, wctx.Root)
	}

	logger.Instance.Info("[DAEMON] [WS-DETECT] ◀ Resolved: root=%s, id=%s, branch=%s, source=%s, risk=%s",
		wctx.Root, wctx.ID, wctx.Branch, source, wctx.MismatchRisk)

	// Inform the adapter of the resolved workspace root via response header.
	// The adapter reads this header once and caches it as sticky X-Workspace-Root
	// for all subsequent requests — eliminating repeated resolver cascades.
	transport.SetResponseHeader(ctx, "X-Resolved-Workspace", wctx.Root)

	// Auto-trigger background indexing for the resolved workspace so the
	// knowledge base is warmed up or synced with disk immediately.
	// We use connectTriggered to ensure this only happens ONCE per WorkspaceID
	// per daemon lifetime, preventing full index scans on every cache miss.
	// recreate=false ensures incremental indexing — only new/changed files are processed.
	if _, triggered := e.connectTriggered.LoadOrStore(wctx.ID, true); !triggered {
		if _, alreadyRunning := e.indexingJobs.Load(wctx.ID); !alreadyRunning {
			logger.Instance.Info("[DAEMON] [WS-DETECT] Auto-triggering incremental index for workspace: %s", wctx.Root)
			go e.StartIndexingAsync(wctx.Root, wctx.ID, nil, false)
		}
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

// CheckAndReindexOnConnect resolves a workspace from the adapter's first hint
// CheckAndReindexOnConnect resolves a workspace from a hint path and triggers
// background re-indexing if branch state changed (e.g. after git pull).
// Returns the resolved workspace root, or "" if resolution fails.
//
// Not called from daemon middleware directly — kept as a utility for
// programmatic use and testing. The adapter's sticky workspace mechanism
// relies on DetectContext + X-Resolved-Workspace header instead.
func (e *Engine) CheckAndReindexOnConnect(hint string) string {
	ctx := context.Background()
	wctx, err := e.DetectContext(ctx, hint)
	if err != nil {
		logger.Instance.Warn("[STICKY] CheckAndReindexOnConnect: failed to resolve hint=%q: %v", hint, err)
		return ""
	}

	logger.Instance.Info("[STICKY] Workspace resolved on connect: root=%s, id=%s, branch=%s, reindex=%v",
		wctx.Root, wctx.ID, wctx.Branch, wctx.ReindexRequired)

	// Trigger background re-indexing if branch state changed.
	// StartIndexingAsync handles de-dup internally via LoadOrStore.
	if wctx.ReindexRequired {
		logger.Instance.Info("[STICKY] Branch state changed — triggering background re-index for %s", filepath.Base(wctx.Root))
		e.StartIndexingAsync(wctx.Root, wctx.ID, nil, false)
	}

	return wctx.Root
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
	logger.Instance.Debug("[TIMER] SearchCode detect_context=%v", time.Since(t0))

	// Primary language from file extension
	primaryLang := "go"
	if a := parser.GetByFile(filePath); a != nil {
		primaryLang = a.Name()
	}

	primaryColl := wctx.CollectionName(primaryLang)
	t1 := time.Now()



	// Check if the primary collection exists.
	// If not, trigger background indexing but do NOT block — the fan-out below
	// will search any other language collections that do exist.
	primaryExists, err := e.search.CollectionExists(ctx, primaryColl)
	logger.Instance.Debug("[TIMER] SearchCode collection_exists_primary=%v (cached=%v)", time.Since(t1), time.Since(t1) < time.Millisecond)
	if err != nil {
		return nil, fmt.Errorf("failed to check collection: %w", err)
	}

	needsTriggerIndexing := false
	if !primaryExists {
		needsTriggerIndexing = true
	}

	if wctx.ReindexRequired {
		logger.Instance.Info("[IDX] Git state change detected (Head: %s), triggering background re-indexing for %s", wctx.HeadSHA, wctx.Root)
		needsTriggerIndexing = true
	}

	if needsTriggerIndexing {
		if _, alreadyRunning := e.indexingJobs.Load(wctx.ID); !alreadyRunning {
			e.StartIndexingAsync(wctx.Root, wctx.ID, nil, false)
		}
	}

	// Embed ONCE, fan-out to all language collections in parallel
	langs := parser.SupportedLanguages()
	if len(langs) == 0 {
		logger.Instance.Warn("[IDX] No parsers registered — SupportedLanguages() returned empty. Skipping fan-out.")
		return nil, fmt.Errorf("no language parsers registered")
	}

	t2 := time.Now()
	vector, err := e.search.EmbedQuery(ctx, queryText)
	logger.Instance.Debug("[TIMER] SearchCode embed=%v", time.Since(t2))
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
				logger.Instance.Warn("[IDX] SearchCode: fan-out failed for %s: %v", c, sErr)
				resultsChan <- langResult{lang: l, coll: c, err: sErr, elapsed: elapsed}
				return
			}
			resultsChan <- langResult{lang: l, coll: c, results: res, elapsed: elapsed}
		}(lang, coll)
	}

	wg.Wait()
	close(resultsChan)
	logger.Instance.Debug("[TIMER] SearchCode fanout_total=%v (langs=%d)", time.Since(t3), len(langs))

	// Merge: primary lang results first, others appended; surface first error if no results
	var primaryResults []storage.SearchResult
	var otherResults []storage.SearchResult
	var firstErr error
	var existingCollections int
	reportLang := primaryLang
	reportColl := primaryColl

	for lr := range resultsChan {
		if lr.err != nil {
			logger.Instance.Debug("[TIMER] SearchCode lang=%s err elapsed=%v", lr.lang, lr.elapsed)
			if firstErr == nil {
				firstErr = lr.err
			}
			continue
		}
		existingCollections++
		logger.Instance.Debug("[TIMER] SearchCode lang=%s hits=%d elapsed=%v", lr.lang, len(lr.results), lr.elapsed)
		if lr.coll == primaryColl {
			primaryResults = lr.results
		} else {
			otherResults = append(otherResults, lr.results...)
		}
	}

	all := append(primaryResults, otherResults...)

	// If no collections exist at all, the workspace is truly not indexed.
	// Before returning an error, try a direct AST-based fallback search.
	if existingCollections == 0 && len(all) == 0 {
		// Ensure background indexing is triggered
		if needsTriggerIndexing {
			if _, alreadyRunning := e.indexingJobs.Load(wctx.ID); !alreadyRunning {
				e.StartIndexingAsync(wctx.Root, wctx.ID, nil, false)
			}
		}

		// Fallback: direct AST search on filesystem — no Qdrant needed
		fallbackResults := e.FallbackDirectSearch(ctx, wctx.Root, queryText, limit)
		if len(fallbackResults) > 0 {
			return &SearchCodeResult{
				Results:         fallbackResults,
				WorkspaceRoot:   wctx.Root,
				WorkspaceID:     wctx.ID,
				Collection:      "fallback",
				Language:        primaryLang,
				MismatchRisk:    wctx.MismatchRisk,
				DetectionSource: wctx.DetectionSource,
			}, nil
		}

		// Fallback also empty — report indexing status
		if _, alreadyRunning := e.indexingJobs.Load(wctx.ID); alreadyRunning {
			return nil, &ErrIndexingInProgress{WorkspaceRoot: wctx.Root, WorkspaceID: wctx.ID}
		}
		if needsTriggerIndexing {
			return nil, &ErrIndexingStarted{WorkspaceRoot: wctx.Root, WorkspaceID: wctx.ID}
		}
		if firstErr != nil {
			return nil, fmt.Errorf("search failed: %w", firstErr)
		}
		return nil, &ErrNotIndexed{WorkspaceRoot: wctx.Root, Collection: primaryColl, Language: primaryLang}
	}

	// Global sort by score descending across all language results, then cap to limit.
	// Without this, primary-language results always win regardless of score.
	sort.Slice(all, func(i, j int) bool { return all[i].Score > all[j].Score })
	if limit > 0 && len(all) > limit {
		all = all[:limit]
	}

	// If vector search returned nothing but collections exist, supplement with fallback
	if len(all) == 0 {
		fallbackResults := e.FallbackDirectSearch(ctx, wctx.Root, queryText, limit)
		if len(fallbackResults) > 0 {
			all = fallbackResults
		} else if firstErr != nil {
			return nil, fmt.Errorf("search failed: %w", firstErr)
		}
	}

	logger.Instance.Debug("[TIMER] SearchCode TOTAL=%v (detect=%v embed=%v fanout=%v)",
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



	exists, err := e.search.CollectionExists(ctx, collection)
	if err != nil {
		return nil, fmt.Errorf("failed to check collection: %w", err)
	}

	if !exists {
		// Trigger background indexing but do NOT block — SmartSearch runs
		// SearchCode (with fan-out) in parallel and will provide results
		// from any available language collections.
		if _, alreadyRunning := e.indexingJobs.Load(wctx.ID); !alreadyRunning {
			e.StartIndexingAsync(wctx.Root, wctx.ID, nil, false)
		}
		logger.Instance.Info("[IDX] ws=%s HybridSearch: collection %s not found — returning empty (indexing in background)", filepath.Base(wctx.Root), collection)
		return nil, nil
	}

	if wctx.ReindexRequired {
		logger.Instance.Info("[IDX] Git state change detected, triggering background re-indexing for ws=%s", filepath.Base(wctx.Root))
		if _, alreadyRunning := e.indexingJobs.Load(wctx.ID); !alreadyRunning {
			e.StartIndexingAsync(wctx.Root, wctx.ID, nil, false)
		}
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
		logger.Instance.Warn("[IDX] No parsers registered — SupportedLanguages() returned empty. Skipping ExactSearch.")
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
				logger.Instance.Warn("[IDX] ExactSearchPolyglot: cannot check collection %s: %v", coll, err)
				return
			}
			if !exists {
				return
			}
			atomic.AddInt32(&existingCollections, 1)

			res, sErr := e.search.ExactSearch(ctx, coll, filters, limit)
			if sErr != nil {
				logger.Instance.Warn("[IDX] ExactSearchPolyglot: search failed for %s: %v", coll, sErr)
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
		logger.Instance.Info("[IDX] ♻️ Pending changes exceeded limit for ws=%s — triggering full scan", filepath.Base(root))
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

	// Count active jobs after adding this one — warn if multiple workspaces are indexing
	// simultaneously (they serialize against each other at Ollama level).
	var activeCount int
	e.indexingJobs.Range(func(_, _ any) bool { activeCount++; return true })
	if activeCount > 1 {
		logger.Instance.Warn("[IDX] ⚠️ %d workspaces indexing simultaneously — Ollama requests will serialize implicitly (ws=%s)", activeCount, filepath.Base(root))
	}

	indexer.SaveIndexStatus(root, &indexer.IndexStatus{State: "starting", StartedAt: time.Now().UTC().Format(time.RFC3339)})

	go func() {
		defer func() {
			e.indexingJobs.Delete(id)
			// If watcher changes came in while we were indexing, run a follow-up incremental job.
			e.tryStartPendingIndex(root, id)
		}()

		ctx := context.Background()
		var err error

		if len(changedFiles) > 0 {
			logger.Instance.Info("[IDX] ♻️ ws=%s Starting incremental indexing for %d files", filepath.Base(root), len(changedFiles))
			err = e.IndexFiles(ctx, root, changedFiles)
		} else {
			logger.Instance.Info("[IDX] 🚀 ws=%s Starting background indexing (recreate=%v)", filepath.Base(root), recreate)
			err = e.IndexWorkspace(ctx, root, recreate)
		}

		if err != nil {
			logger.Instance.Error("[IDX] ws=%s Background indexing failed: %v", filepath.Base(root), err)
			s := indexer.LoadIndexStatus(root)
			if s == nil {
				s = &indexer.IndexStatus{State: "starting"}
			}
			s.State = "failed"
			s.Error = err.Error()
			s.EndedAt = time.Now().UTC().Format(time.RFC3339)
			if started, pErr := time.Parse(time.RFC3339, s.StartedAt); pErr == nil {
				s.Elapsed = time.Since(started).Round(time.Second).String()
			}
			indexer.SaveIndexStatus(root, s)
		} else {
			logger.Instance.Info("[IDX] ✅ ws=%s Background indexing completed", filepath.Base(root))
			s := indexer.LoadIndexStatus(root)
			if s == nil {
				s = &indexer.IndexStatus{State: "starting"}
			}
			s.State = "completed"
			s.EndedAt = time.Now().UTC().Format(time.RFC3339)
			if started, pErr := time.Parse(time.RFC3339, s.StartedAt); pErr == nil {
				s.Elapsed = time.Since(started).Round(time.Second).String()
			}
			indexer.SaveIndexStatus(root, s)
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
			logger.Instance.Warn("[IDX] Failed to index %s: %v", filepath.Base(p), indexErr)
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
				logger.Instance.Warn("[IDX] Failed to install ragcode-sse skill for ws=%s: %v", wctx.Root, err)
			}
		}
	}

	// We'll iterate all supported languages.
	languages := parser.SupportedLanguages()

	wsName := filepath.Base(wctx.Root)

	var excludePatterns []string
	if e.config != nil {
		excludePatterns = e.config.Workspace.ExcludePatterns
	}

	// Pre-count total files per language with a single WalkDir pass.
	// This gives us the real on_disk totals for accurate progress reporting,
	// instead of using len(changedFiles) which only reflects modified files.
	fileCounts := e.indexer.CountAllFiles(wctx.Root, excludePatterns)
	logger.Instance.Info("[IDX] ws=%s file counts: %v", wsName, fileCounts)

	// Pre-populate index_status.json with the real disk totals so that
	// even languages with 0 changed files still show correct on_disk counts.
	{
		s := indexer.LoadIndexStatus(wctx.Root)
		if s == nil {
			s = &indexer.IndexStatus{State: "starting", StartedAt: time.Now().UTC().Format(time.RFC3339)}
		}
		if s.Languages == nil {
			s.Languages = make(map[string]indexer.LangStatus)
		}
		for _, lang := range languages {
			s.Languages[lang] = indexer.LangStatus{OnDisk: fileCounts[lang]}
		}
		indexer.SaveIndexStatus(wctx.Root, s)
	}

	var indexErrors []string
	for _, lang := range languages {
		diskTotal := fileCounts[lang]
		collection := wctx.CollectionName(lang)
		logger.Instance.Info("[IDX] ws=%s lang=%s ▶ starting (on_disk=%d)", wsName, lang, diskTotal)
		err := e.indexer.IndexWorkspace(ctx, wctx.Root, collection, indexer.Options{
			Language:        lang,
			WorkspaceName:   wsName,
			ExcludePatterns: excludePatterns,
			Recreate:        recreate,
			Progress: func(doneFiles, totalFiles int) {
				// Throttle disk I/O: write every 10 files or on the last file
				if doneFiles%10 != 0 && doneFiles != totalFiles {
					return
				}
				if s := indexer.LoadIndexStatus(wctx.Root); s != nil {
					s.State = "running"
					if s.Languages == nil {
						s.Languages = make(map[string]indexer.LangStatus)
					}
					ls := s.Languages[lang]
					ls.OnDisk = diskTotal   // real total files on disk for this language
					ls.Changed = totalFiles // files that needed re-indexing (changedFiles)
					ls.Processed = doneFiles
					s.Languages[lang] = ls
					indexer.SaveIndexStatus(wctx.Root, s)
				}
			},
		})
		if err != nil {
			logger.Instance.Error("[IDX] ws=%s lang=%s ❌ failed: %v", wsName, lang, err)
			indexErrors = append(indexErrors, fmt.Sprintf("%s: %v", lang, err))
		} else {
			logger.Instance.Info("[IDX] ws=%s lang=%s ✓ done", wsName, lang)
		}
	}

	if e.watchers != nil {
		if err := e.watchers.Start(wctx.Root, e.handleWatchChange); err != nil {
			logger.Instance.Warn("[IDX] Failed to start watcher for ws=%s: %v", filepath.Base(wctx.Root), err)
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

// ─── Stale File Cleanup ──────────────────────────────────────────────────────

// staleCooldown prevents repeatedly deleting the same file from the DB
// across consecutive search queries. Entries expire after 10 minutes.
const staleCooldown = 10 * time.Minute

// staleCleanupCache tracks recently cleaned file paths to avoid repeated deletes.
// Key: "wsID:filePath", Value: time.Time of last cleanup.
var staleCleanupCache sync.Map

// CleanupStaleFiles removes vectors for deleted files from ALL language collections
// of a workspace. It runs asynchronously and deduplicates to avoid hammering Qdrant.
func (e *Engine) CleanupStaleFiles(wsID string, staleFiles []string) {
	if len(staleFiles) == 0 {
		return
	}

	// Deduplicate: skip files cleaned recently
	now := time.Now()
	var toClean []string
	for _, f := range staleFiles {
		cacheKey := wsID + ":" + f
		if last, ok := staleCleanupCache.Load(cacheKey); ok {
			if now.Sub(last.(time.Time)) < staleCooldown {
				continue
			}
		}
		staleCleanupCache.Store(cacheKey, now)
		toClean = append(toClean, f)
	}

	if len(toClean) == 0 {
		return
	}

	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()

		langs := parser.SupportedLanguages()
		var totalDeleted int

		for _, filePath := range toClean {
			for _, lang := range langs {
				collection := CollectionNameFor(wsID, lang)

				// Check collection exists before attempting delete
				exists, err := e.search.CollectionExists(ctx, collection)
				if err != nil || !exists {
					continue
				}

				if err := e.search.DeleteByFilter(ctx, collection, "file_path", filePath); err != nil {
					logger.Instance.Warn("[STALE] Failed to delete vectors for %s from %s: %v", filePath, collection, err)
					// Remove from cache so retry is possible next query
					staleCleanupCache.Delete(wsID + ":" + filePath)
				} else {
					totalDeleted++
				}
			}
		}

		if totalDeleted > 0 {
			logger.Instance.Info("[STALE] 🧹 Cleaned %d stale file(s) (%d delete ops) for ws=%s",
				len(toClean), totalDeleted, wsID)
		}
	}()
}

package engine

import (
	"context"
	"errors"
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
	"github.com/doITmagic/rag-code-mcp/internal/service/iderules"
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
	detector *detector.Detector
	config   *config.Config
	watchers *watch.Manager

	// indexingJobs tracks active background indexing jobs.
	// Key: workspace ID, Value: start time
	indexingJobs sync.Map
	// indexingSlot keeps workspace indexing sequential. Ollama serializes embed
	// requests anyway, while parallel workspace scans multiply memory pressure.
	indexingSlot chan struct{}

	// pendingIndex tracks file changes received while an indexing job is running.
	// It ensures watcher-triggered incremental indexing is lossless under rapid edits.
	pendingMu       sync.Mutex
	pendingFiles    map[string]map[string]struct{} // workspaceID -> set(filePath)
	pendingOverflow map[string]bool                // workspaceID -> too many pending changes, fallback to full scan
	pendingRecreate map[string]bool                // workspaceID -> explicit recreate requested while a job was running

	registry *registry.Registry

	// detectionCache stores resolved WorkspaceContext with TTL to avoid
	// repeated full resolver cascades for the same path.
	detectionCache sync.Map // map[string]*detectionCacheEntry

	// connectTriggered tracks whether background indexing was automatically
	// triggered for a workspace ID upon initial daemon resolution.
	connectTriggered sync.Map

	// lastIndexed records, per workspace ID, when an indexing job last ran or
	// finished. Paces the rescans of a workspace that has no watcher.
	lastIndexed sync.Map

	lifecycleCtx    context.Context
	lifecycleCancel context.CancelFunc
	bgWg            sync.WaitGroup
}

func (e *Engine) bgContext() context.Context {
	if e != nil && e.lifecycleCtx != nil {
		return e.lifecycleCtx
	}
	return context.Background()
}

// detectionCacheEntry wraps a cached WorkspaceContext with an expiry.
type detectionCacheEntry struct {
	wctx   *WorkspaceContext
	expiry time.Time
}

const detectionCacheTTL = 5 * time.Second

// unwatchedRescanInterval paces incremental rescans of a workspace with no
// watcher. A var so tests can shorten it.
var unwatchedRescanInterval = time.Minute

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
	detOpts := detector.DefaultOptions()
	if cfg != nil {
		detOpts.ExcludePatterns = cfg.Workspace.ExcludePatterns
	}
	det := detector.New(detOpts)
	// Watch events can arrive immediately after checkout; stale Git state writes to the wrong collection.
	branchMgr := branchstate.NewManager(branchstate.WithCacheTTL(0))

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

	if cfg != nil && idx != nil {
		batchSize := cfg.LLM.EmbedBatchSize
		if cfg.Workspace.EmbedBatchSize > 0 {
			batchSize = cfg.Workspace.EmbedBatchSize
		} else if cfg.RagCode.EmbedBatchSize > 0 {
			batchSize = cfg.RagCode.EmbedBatchSize
		}
		if batchSize > 0 {
			idx.SetEmbedBatchSize(batchSize)
		}

		workers := cfg.LLM.EmbedWorkers
		if cfg.Workspace.EmbedWorkers > 0 {
			workers = cfg.Workspace.EmbedWorkers
		} else if cfg.RagCode.EmbedWorkers > 0 {
			workers = cfg.RagCode.EmbedWorkers
		}
		if workers > 0 {
			idx.SetEmbedWorkers(workers)
		}
	}

	lifecycleCtx, lifecycleCancel := context.WithCancel(context.Background())

	return &Engine{
		indexer:      idx,
		search:       srv,
		resolver:     res,
		detector:     det,
		config:       cfg,
		watchers:     watcherMgr,
		registry:     reg,
		indexingSlot: make(chan struct{}, 1),

		pendingFiles:    make(map[string]map[string]struct{}),
		pendingOverflow: make(map[string]bool),
		pendingRecreate: make(map[string]bool),

		lifecycleCtx:    lifecycleCtx,
		lifecycleCancel: lifecycleCancel,
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

// FindAlternativeCandidates wraps detector logic to offer alternative root suggestions internally.
// Uses the engine's configured detector to ensure consistent AllowedRoots/ExcludePatterns.
func (e *Engine) FindAlternativeCandidates(root string) []string {
	return e.detector.FindAlternativeCandidates(root)
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
// Routes workspace_root through the explicit workspace root path for correct
// resolver semantics (ReasonExplicitWorkspaceRoot, confidence 1.0).
func (e *Engine) DetectFromParams(ctx context.Context, params map[string]interface{}) (*WorkspaceContext, error) {
	// Check file_path first (highest specificity)
	if v, ok := params["file_path"].(string); ok && strings.TrimSpace(v) != "" {
		return e.DetectContext(ctx, v)
	}
	// workspace_root should route through the explicit root path
	if v, ok := params["workspace_root"].(string); ok && strings.TrimSpace(v) != "" {
		return e.DetectContextAsRoot(ctx, v, true)
	}
	// workspace alias
	if v, ok := params["workspace"].(string); ok && strings.TrimSpace(v) != "" {
		return e.DetectContext(ctx, v)
	}
	return e.DetectContext(ctx, "")
}

type resolvedWorkspaceKey struct{}

type resolvedWorkspace struct {
	path      string
	workspace *WorkspaceContext
}

// WithResolvedWorkspace shares one resolution across the searches of a request.
func WithResolvedWorkspace(ctx context.Context, path string, workspace *WorkspaceContext) context.Context {
	return context.WithValue(ctx, resolvedWorkspaceKey{}, resolvedWorkspace{path, workspace})
}

// DetectContext resolves the workspace context for a given path using the full resolver cascade.
// If path is empty, it falls back to the last active workspace from the registry.
// Explicit paths without Git metadata are cached with a 5s TTL.
func (e *Engine) DetectContext(ctx context.Context, path string) (*WorkspaceContext, error) {
	if resolved, ok := ctx.Value(resolvedWorkspaceKey{}).(resolvedWorkspace); ok && resolved.path == path {
		return resolved.workspace, nil
	}
	// Log incoming detection request for debugging workspace resolution
	hintFromCtx := transport.GetWorkspaceHint(ctx)
	logger.Instance.Info("[WS-DETECT] ▶ DetectContext called: path=%q, X-Workspace-Hint=%q", path, hintFromCtx)

	req := contract.ResolveWorkspaceRequest{}
	source := "explicit_file_path"
	cacheKey := ""

	if strings.TrimSpace(path) == "" {
		logger.Instance.Info("[WS-DETECT] Tier 1 skipped: no explicit path in request params")

		// Tier 2: Try workspace hint from adapter (IDE's CWD injected via X-Workspace-Hint header)
		if hint := strings.TrimSpace(hintFromCtx); hint != "" {
			abs, err := filepath.Abs(hint)
			if err != nil {
				return nil, fmt.Errorf("cannot resolve confirmed workspace %q: %w; provide an explicit file_path to select a workspace", hint, err)
			}
			// A lost client binding must not fall through to another client's workspace.
			if _, err := os.Stat(abs); err != nil {
				return nil, fmt.Errorf("confirmed workspace %q is unavailable: %w; provide an explicit file_path to select a workspace", abs, err)
			}
			req.FilePath = abs
			source = "hint_fallback"
			logger.Instance.Info("[WS-DETECT] ✅ Tier 2 matched: using X-Workspace-Hint=%s", abs)
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
		cacheKey = abs
		logger.Instance.Info("[WS-DETECT] ✅ Tier 1 matched: using explicit file_path=%s", abs)
	}

	// Cache explicit paths only; hinted requests must recheck branch metadata.
	if cacheKey != "" {
		if entry, ok := e.detectionCache.LoadAndDelete(cacheKey); ok {
			ce := entry.(*detectionCacheEntry)
			if time.Now().Before(ce.expiry) {
				e.detectionCache.Store(cacheKey, ce)
				transport.SetResponseHeader(ctx, "X-Resolved-Workspace", ce.wctx.Root)
				logger.Instance.Debug("[WS-DETECT] ◀ Cache hit: root=%s, source=%s", ce.wctx.Root, ce.wctx.DetectionSource)
				return ce.wctx, nil
			}
		}
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
	// A registry fallback is passed to the resolver as an explicit root, so
	// its "workspace_root" is not more specific — keep the fallback label,
	// which is what triggers the fallback warning in tool responses.
	if resp.PathResolutionSource != "" && resp.PathResolutionSource != source && source != "registry_fallback" {
		wctx.DetectionSource = resp.PathResolutionSource
		logger.Instance.Info("[DAEMON] [WS-DETECT] ◀ Resolver override: source changed %s → %s (root: %s)",
			source, resp.PathResolutionSource, wctx.Root)
	}

	logger.Instance.Info("[DAEMON] [WS-DETECT] ◀ Resolved: root=%s, id=%s, branch=%s, source=%s, risk=%s",
		wctx.Root, wctx.ID, wctx.Branch, source, wctx.MismatchRisk)

	// Inform the adapter of the resolved workspace root via response header.
	// The adapter uses this root as sticky X-Workspace-Root for subsequent requests.
	transport.SetResponseHeader(ctx, "X-Resolved-Workspace", wctx.Root)

	e.autoTriggerIndex(wctx)

	// Don't cache entries that require reindex or are high-risk — let next call re-evaluate
	if cacheKey != "" && wctx.Branch == "" && !wctx.ReindexRequired && wctx.MismatchRisk != "high" {
		e.detectionCache.Store(cacheKey, &detectionCacheEntry{
			wctx:   wctx,
			expiry: time.Now().Add(detectionCacheTTL),
		})
	}

	return wctx, nil
}

// DetectContextAsRoot resolves workspace context using an explicit workspace root path.
// Unlike DetectContext (which routes through req.FilePath → marker detection),
// this method uses req.WorkspaceRoot which the resolver treats as a trusted,
// pre-validated root with ReasonExplicitWorkspaceRoot and confidence 1.0.
//
// triggerAutoIndex controls whether background indexing is auto-triggered:
//   - true  → normal connect behavior (sets connectTriggered, starts indexing)
//   - false → pure validation (resolve only, no indexing side effects)
func (e *Engine) DetectContextAsRoot(ctx context.Context, root string, triggerAutoIndex bool) (*WorkspaceContext, error) {
	if strings.TrimSpace(root) == "" {
		return e.DetectContext(ctx, "")
	}

	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve workspace root path: %w", err)
	}

	logger.Instance.Info("[WS-DETECT] ▶ DetectContextAsRoot called: root=%q triggerAutoIndex=%v", abs, triggerAutoIndex)

	req := contract.ResolveWorkspaceRequest{
		WorkspaceRoot: abs,
	}

	resp, wsErr := e.resolver.Resolve(ctx, req)
	if wsErr != nil {
		return nil, fmt.Errorf("workspace root resolution failed: %s", wsErr.Message)
	}

	wctx := &WorkspaceContext{
		Root:            resp.ResolvedRoot,
		ID:              resp.WorkspaceID,
		Branch:          resp.Branch,
		WorktreeID:      resp.WorktreeID,
		MismatchRisk:    resp.MismatchRisk,
		DetectionSource: "explicit_workspace_root",
		ReindexRequired: resp.ReindexRequired,
		HeadSHA:         resp.HeadSHA,
	}

	if resp.PathResolutionSource != "" && resp.PathResolutionSource != "workspace_root" {
		wctx.DetectionSource = resp.PathResolutionSource
	}

	logger.Instance.Info("[DAEMON] [WS-DETECT] ◀ Resolved via root: root=%s, id=%s, branch=%s, source=%s",
		wctx.Root, wctx.ID, wctx.Branch, wctx.DetectionSource)

	transport.SetResponseHeader(ctx, "X-Resolved-Workspace", wctx.Root)

	if triggerAutoIndex {
		e.autoTriggerIndex(wctx)
	}

	return wctx, nil
}

// autoTriggerIndex starts a background incremental index for a resolved
// workspace so the knowledge base is warmed up or synced with disk.
// connectTriggered limits this to once per WorkspaceID unless Git state changed,
// preventing full index scans on every cache miss. Another exception is a root
// with no watcher (start failed, the root was moved, or watch_enabled is
// false): nothing else would ever pick up new files, so the incremental index
// re-runs at most once per unwatchedRescanInterval, and IndexWorkspace
// retries the watcher when watching is enabled.
func (e *Engine) autoTriggerIndex(wctx *WorkspaceContext) {
	if e.config != nil && !e.config.Workspace.AutoIndex {
		return
	}
	// Do NOT check indexingJobs.Load here — that is a TOCTOU race window.
	// StartIndexingAsync uses indexingJobs.LoadOrStore atomically and is
	// already idempotent: if a job is running it returns immediately.
	// Calling it directly (not via `go`) avoids the extra scheduling delay
	// that previously widened the race window between ResumeIndexingOnConnect
	// and DetectContext. StartIndexingAsync spawns its own goroutine internally.
	_, triggered := e.connectTriggered.LoadOrStore(wctx.ID, true)
	if !triggered || wctx.ReindexRequired {
		logger.Instance.Info("[DAEMON] [WS-DETECT] Auto-triggering incremental index for workspace: %s", wctx.Root)
		e.startIndexing(wctx.Root, wctx.ID, nil, false, wctx.ReindexRequired)
		return
	}
	if e.watchers != nil && e.watchers.Watching(wctx.Root) {
		return
	}
	// Reserve the slot atomically so concurrent detections do not all pass
	// the age check; the stamp is refreshed when the job finishes.
	now := time.Now()
	last, loaded := e.lastIndexed.LoadOrStore(wctx.ID, now)
	if loaded {
		if now.Sub(last.(time.Time)) < unwatchedRescanInterval {
			return
		}
		if !e.lastIndexed.CompareAndSwap(wctx.ID, last, now) {
			return
		}
	}
	logger.Instance.Info("[DAEMON] [WS-DETECT] No watcher for %s, re-running incremental index", wctx.Root)
	e.StartIndexingAsync(wctx.Root, wctx.ID, nil, false)
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
	ctx := e.bgContext()
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

// ResumeIndexingOnConnect iterates through all registered workspaces, checks if their
// indexing was interrupted (no EndedAt status), and resumes indexing for the one
// that was most recently being indexed.
func (e *Engine) ResumeIndexingOnConnect() {
	if e.config != nil && !e.config.Workspace.AutoIndex {
		logger.Instance.Debug("[DAEMON] ResumeIndexingOnConnect: auto_index disabled")
		return
	}

	if e.registry == nil {
		logger.Instance.Debug("[DAEMON] ResumeIndexingOnConnect: no registry configured")
		return
	}

	entries := e.registry.List()
	if len(entries) == 0 {
		return
	}

	var roots []string
	idMap := make(map[string]string)
	for _, entry := range entries {
		roots = append(roots, entry.Root)
		idMap[entry.Root] = entry.ID
	}

	bestRoot := indexer.GetLastInterruptedWorkspace(roots)
	if bestRoot != "" {
		logger.Instance.Info("[DAEMON] Resuming incomplete indexing for workspace: %s", filepath.Base(bestRoot))
		// Mark as triggered BEFORE calling StartIndexingAsync so that DetectContext's
		// auto-trigger (connectTriggered.LoadOrStore) sees this workspace as already
		// handled and does NOT start a second concurrent indexing job.
		e.connectTriggered.Store(idMap[bestRoot], true)
		e.StartIndexingAsync(bestRoot, idMap[bestRoot], nil, false)
	} else {
		logger.Instance.Debug("[DAEMON] ResumeIndexingOnConnect: no incomplete indexing jobs found")
	}
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
		time.Since(t0), t1.Sub(t0), t3.Sub(t2), time.Since(t3))

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
func (e *Engine) HybridSearchCode(ctx context.Context, filePath, queryText string, limit int, includeDocs bool) (*SearchCodeResult, error) {
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
	type target struct {
		collection  string
		includeDocs bool
	}
	targets := []target{{collection: collection, includeDocs: lang == "docs"}}
	if includeDocs && lang != "docs" {
		targets = append(targets, target{collection: wctx.CollectionName("docs"), includeDocs: true})
	}
	available := targets[:0]
	for _, target := range targets {
		exists, err := e.search.CollectionExists(ctx, target.collection)
		if err != nil {
			return nil, fmt.Errorf("failed to check collection: %w", err)
		}
		if exists {
			available = append(available, target)
		}
	}

	if len(available) == 0 {
		// Trigger background indexing but do NOT block — SmartSearch runs
		// SearchCode (with fan-out) in parallel and will provide results
		// from any available language collections.
		if _, alreadyRunning := e.indexingJobs.Load(wctx.ID); !alreadyRunning {
			e.StartIndexingAsync(wctx.Root, wctx.ID, nil, false)
		}
		logger.Instance.Info("[IDX] ws=%s HybridSearch: requested collections not found — returning empty (indexing in background)", filepath.Base(wctx.Root))
		return nil, nil
	}

	if wctx.ReindexRequired {
		logger.Instance.Info("[IDX] Git state change detected, triggering background re-indexing for ws=%s", filepath.Base(wctx.Root))
		if _, alreadyRunning := e.indexingJobs.Load(wctx.ID); !alreadyRunning {
			e.StartIndexingAsync(wctx.Root, wctx.ID, nil, false)
		}
	}

	var results []storage.SearchResult
	for _, target := range available {
		found, err := e.search.HybridSearch(ctx, target.collection, queryText, limit, target.includeDocs)
		if err != nil {
			return nil, fmt.Errorf("hybrid search failed: %w", err)
		}
		results = append(results, found...)
	}
	sort.Slice(results, func(i, j int) bool { return results[i].Score > results[j].Score })
	if limit > 0 && len(results) > limit {
		results = results[:limit]
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
	e.addPendingIndexFilesLocked(workspaceID, files)
}

// addPendingIndexFilesLocked requires pendingMu, which also guards job handoff.
func (e *Engine) addPendingIndexFilesLocked(workspaceID string, files []string) {
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

func (e *Engine) popPendingIndex(workspaceID string) (files []string, overflow, recreate bool) {
	e.pendingMu.Lock()
	defer e.pendingMu.Unlock()

	overflow = e.pendingOverflow[workspaceID]
	delete(e.pendingOverflow, workspaceID)
	recreate = e.pendingRecreate[workspaceID]
	delete(e.pendingRecreate, workspaceID)

	set, ok := e.pendingFiles[workspaceID]
	if !ok {
		return nil, overflow, recreate
	}
	delete(e.pendingFiles, workspaceID)

	files = make([]string, 0, len(set))
	for f := range set {
		files = append(files, f)
	}
	sort.Strings(files)
	return files, overflow, recreate
}

func (e *Engine) tryStartPendingIndex(root, workspaceID string) {
	files, overflow, recreate := e.popPendingIndex(workspaceID)
	if recreate || overflow {
		// Overflow only means the batch is too large to replay file by file. A
		// full incremental run picks every change up; only an explicit request
		// may drop collections, otherwise a busy first index re-embeds everything.
		logger.Instance.Info("[IDX] ♻️ ws=%s pending changes exceeded limit or recreate queued — full run (recreate=%v)", filepath.Base(root), recreate)
		e.startIndexing(root, workspaceID, nil, recreate, true)
		return
	}
	if len(files) == 0 {
		return
	}
	e.StartIndexingAsync(root, workspaceID, files, false)
}

func (e *Engine) finishIndexingJob(root, workspaceID string) {
	// Coordinate job removal with queuing in StartIndexingAsync. An event must
	// either join the current job's pending set or start a new job, never land
	// in the old pending set after it has already been drained.
	e.pendingMu.Lock()
	e.indexingJobs.Delete(workspaceID)
	e.pendingMu.Unlock()
	e.lastIndexed.Store(workspaceID, time.Now())
	if e.bgContext().Err() == nil {
		e.tryStartPendingIndex(root, workspaceID)
	}
}

// StartIndexingAsync starts the indexing process in a background goroutine.
// If changedFiles is nil or empty, a full re-index is performed.
// If a job is already running, changedFiles are queued for a follow-up job.
// If recreate=true and a job is already running, the recreate is queued and
// will start immediately after the current job finishes.
func (e *Engine) StartIndexingAsync(root, id string, changedFiles []string, recreate bool) {
	e.startIndexing(root, id, changedFiles, recreate, false)
}

// startIndexing is StartIndexingAsync with one extra rule for pending replay:
// queueFullRun keeps a full-run request when a newer job already claimed the
// workspace, instead of dropping it. Direct callers (search-triggered
// indexing) must keep the old no-op, or a failing index would requeue forever.
func (e *Engine) startIndexing(root, id string, changedFiles []string, recreate, queueFullRun bool) {
	if watch.IsInvalidRoot(root) {
		logger.Instance.Error("[IDX] ⛔ Refusing to index invalid/dangerous root: %s", root)
		return
	}

	if e.bgContext().Err() != nil {
		return
	}

	e.pendingMu.Lock()
	if _, loaded := e.indexingJobs.LoadOrStore(id, time.Now()); loaded {
		// A job is already running. If recreate=true, queue it so it fires
		// after the current job finishes (via tryStartPendingIndex/defer).
		if recreate {
			e.pendingRecreate[id] = true
			delete(e.pendingFiles, id)
			logger.Instance.Info("[IDX] ⏳ ws=%s recreate requested while indexing — queued for after current job", filepath.Base(root))
		} else if len(changedFiles) > 0 {
			e.addPendingIndexFilesLocked(id, changedFiles)
		} else if queueFullRun {
			e.pendingOverflow[id] = true
			delete(e.pendingFiles, id)
		}
		e.pendingMu.Unlock()
		return
	}
	e.pendingMu.Unlock()

	// Rule files are refreshed here rather than on every resolve: this is the
	// point where the workspace is known to be a real, confirmed root.
	// Legacy first — an old .clinerules *file* would block the new directory.
	if e.config == nil || e.config.Workspace.AutoCreateIDERules {
		iderules.RemoveLegacy(root)
		iderules.Write(root)
	}

	// Preserve existing Languages data on restart so incremental indexing
	// doesn't show processed=0 to AI consumers. Reset only lifecycle fields.
	s := indexer.LoadIndexStatus(root)
	if s == nil {
		s = &indexer.IndexStatus{}
	}
	s.StartedAt = time.Now().UTC().Format(time.RFC3339)
	s.EndedAt = ""
	s.Elapsed = ""
	s.Error = ""
	indexer.SaveIndexStatus(root, s)

	e.bgWg.Add(1)
	go func() {
		defer e.bgWg.Done()
		defer e.finishIndexingJob(root, id)

		if len(e.indexingSlot) > 0 {
			logger.Instance.Info("[IDX] ⏳ ws=%s queued behind another workspace", filepath.Base(root))
		}
		e.indexingSlot <- struct{}{}
		defer func() { <-e.indexingSlot }()

		ctx := e.bgContext()
		if ctx.Err() != nil {
			return
		}
		s.StartedAt = time.Now().UTC().Format(time.RFC3339)
		indexer.SaveIndexStatus(root, s)

		var err error

		if len(changedFiles) > 0 {
			logger.Instance.Info("[IDX] ♻️ ws=%s Starting incremental indexing for %d files", filepath.Base(root), len(changedFiles))
			err = e.IndexFiles(ctx, root, changedFiles)
		} else {
			logger.Instance.Info("[IDX] 🚀 ws=%s Starting background indexing (recreate=%v)", filepath.Base(root), recreate)
			err = e.IndexWorkspace(ctx, root, recreate)
		}

		if ctx.Err() == nil {
			finalizeIndexStatus(root, err)
		}
		if errors.Is(err, indexer.ErrWorkspaceChanged) {
			// A checkout interrupted this run; the next request must retry even with a watcher.
			e.connectTriggered.Delete(id)
		}
		if err != nil {
			logger.Instance.Error("[IDX] ws=%s Background indexing failed: %v", filepath.Base(root), err)
		} else {
			logger.Instance.Info("[IDX] ✅ ws=%s Background indexing completed", filepath.Base(root))
		}
	}()
}

// finalizeIndexStatus loads the current status, stamps EndedAt/Elapsed/Error,
// and saves it atomically. Centralises the logic that was duplicated in the
// success and error branches of StartIndexingAsync.
func finalizeIndexStatus(root string, indexErr error) {
	s := indexer.LoadIndexStatus(root)
	if s == nil {
		s = &indexer.IndexStatus{}
	}
	s.EndedAt = time.Now().UTC().Format(time.RFC3339)
	if started, pErr := time.Parse(time.RFC3339, s.StartedAt); pErr == nil {
		s.Elapsed = time.Since(started).Round(time.Second).String()
	}
	if indexErr != nil {
		s.Error = indexErr.Error()
	}
	indexer.SaveIndexStatus(root, s)
}

// IndexFiles indexes specific files in a workspace.
func (e *Engine) IndexFiles(ctx context.Context, root string, files []string) (resultErr error) {
	wctx, err := e.DetectContextAsRoot(ctx, root, false)
	if err != nil {
		return err
	}
	if wctx.ReindexRequired {
		return e.IndexWorkspace(ctx, root, false)
	}
	ctx = indexer.WithGitState(ctx, wctx.Root, wctx.Branch, wctx.HeadSHA)

	states := make(map[string]*indexer.State)
	saveLoadedStates := func() error {
		var saveErrors []error
		for col, st := range states {
			if saveErr := st.Save(indexer.StatePath(wctx.Root, col)); saveErr != nil {
				logger.Instance.Warn("[IDX] Failed to save state for %s before full workspace fallback: %v", col, saveErr)
				saveErrors = append(saveErrors, saveErr)
			}
		}
		return errors.Join(saveErrors...)
	}
	defer func() {
		if errors.Is(resultErr, indexer.ErrWorkspaceChanged) {
			resultErr = errors.Join(resultErr, saveLoadedStates())
		}
	}()

	var excludePatterns []string
	if e.config != nil {
		excludePatterns = e.config.Workspace.ExcludePatterns
	}

	// Loaded at most once per batch: a build clean deletes thousands of files.
	var trackedDirs map[string]struct{}
	wasIndexedDir := func(p string) bool {
		if trackedDirs == nil {
			trackedDirs = e.trackedDirectories(wctx)
		}
		_, ok := trackedDirs[p]
		return ok
	}

	// Resolve the collection per file: one watcher batch can mix languages,
	// and taking the first file's language sent the rest to the wrong one.
	for _, p := range files {
		info, statErr := os.Stat(p)
		isDir := statErr == nil && info.IsDir()
		// The watcher's exclusions are looser than the scanner's (tmp/, globs):
		// skip what a full run would never index instead of indexing it now
		// and dropping it on the next run, or rescanning for a mkdir in tmp/.
		if indexer.ExcludedPath(wctx.Root, p, isDir, excludePatterns) {
			continue
		}
		// Moving a populated directory into the workspace, out of it, or into
		// an excluded directory can emit only the directory's own path; no
		// per-file events are guaranteed. Rediscover with the usual exclusions
		// so new files are indexed and vanished ones lose their vectors.
		if isDir || os.IsNotExist(statErr) && wasIndexedDir(p) {
			saveLoadedStates()
			return e.IndexWorkspace(ctx, root, false)
		}
		a := parser.GetByFile(p)
		if a == nil {
			continue
		}
		collection := wctx.CollectionName(a.Name())
		state := states[collection]
		if state == nil {
			exists, err := e.search.CollectionExists(ctx, collection)
			if err != nil {
				saveLoadedStates()
				return err
			}
			if !exists {
				saveLoadedStates()
				return e.IndexWorkspace(ctx, root, false)
			}
			statePath := indexer.StatePath(wctx.Root, collection)
			if _, err := os.Stat(statePath); os.IsNotExist(err) {
				saveLoadedStates()
				return e.IndexWorkspace(ctx, root, false)
			}
			state, err = indexer.LoadState(statePath)
			if err != nil {
				saveLoadedStates()
				return err
			}
			states[collection] = state
		}
		// A deleted file cannot be analysed; drop its vectors instead of
		// leaving them until the next full run or a search that notices them.
		if os.IsNotExist(statErr) {
			if err := e.indexer.RemoveFile(ctx, collection, p, state); err != nil {
				if errors.Is(err, indexer.ErrWorkspaceChanged) {
					return err
				}
				logger.Instance.Warn("[IDX] Failed to remove deleted %s: %v", filepath.Base(p), err)
			}
			continue
		}
		if _, indexErr := e.indexer.IndexFile(ctx, collection, p, state); indexErr != nil {
			if errors.Is(indexErr, indexer.ErrWorkspaceChanged) {
				return indexErr
			}
			logger.Instance.Warn("[IDX] Failed to index %s: %v", filepath.Base(p), indexErr)
		}
	}

	for collection, state := range states {
		if err := state.Save(indexer.StatePath(wctx.Root, collection)); err != nil {
			return err
		}
	}
	e.refreshIndexCounts(wctx.Root)
	return nil
}

// trackedDirectories returns every directory below root that holds a file
// tracked by any collection state, so a vanished path can be recognised as a
// formerly indexed directory with one map lookup. It reads each state once;
// callers hold the result for a whole batch, which may be thousands of paths.
func (e *Engine) trackedDirectories(wctx *WorkspaceContext) map[string]struct{} {
	dirs := make(map[string]struct{})
	for _, lang := range parser.SupportedLanguages() {
		st, err := indexer.LoadState(indexer.StatePath(wctx.Root, wctx.CollectionName(lang)))
		if err != nil || st == nil {
			continue
		}
		for tracked := range st.Files {
			for dir := filepath.Dir(tracked); len(dir) > len(wctx.Root); dir = filepath.Dir(dir) {
				if _, seen := dirs[dir]; seen {
					break
				}
				dirs[dir] = struct{}{}
			}
		}
	}
	return dirs
}

// refreshIndexCounts recounts files on disk after an incremental run. Watcher
// changes (adds, edits, deletes) go through IndexFiles, which never touched
// index_status.json, so deleted files kept being reported as indexed. Once
// the changed files are processed, everything on disk is indexed.
func (e *Engine) refreshIndexCounts(root string) {
	s := indexer.LoadIndexStatus(root)
	if s == nil {
		return
	}
	var excludePatterns []string
	if e.config != nil {
		excludePatterns = e.config.Workspace.ExcludePatterns
	}
	counts := e.indexer.CountAllFiles(root, excludePatterns)
	for lang, ls := range s.Languages {
		ls.OnDisk = counts.Counts[lang]
		ls.Processed = ls.OnDisk
		ls.Changed = 0
		ls.Breakdown = counts.Breakdowns[lang]
		s.Languages[lang] = ls
	}
	indexer.SaveIndexStatus(root, s)
}

// IndexWorkspace indexes all files in a workspace.
//
// path is an already-confirmed root: it is resolved as-is, never re-detected
// by markers, which could land on a parent (a monorepo root, or home) and
// register and index that instead.
func (e *Engine) IndexWorkspace(ctx context.Context, path string, recreate bool) error {
	wctx, err := e.DetectContextAsRoot(ctx, path, false)
	if err != nil {
		return err
	}
	if watch.IsInvalidRoot(wctx.Root) {
		return fmt.Errorf("refusing to index invalid root %s", wctx.Root)
	}
	ctx = indexer.WithGitState(ctx, wctx.Root, wctx.Branch, wctx.HeadSHA)
	if err := ctx.Err(); err != nil {
		return err
	}

	// Background jobs already hold this marker. Direct synchronous callers need
	// one too, so early watcher events queue instead of indexing concurrently.
	e.pendingMu.Lock()
	_, alreadyRunning := e.indexingJobs.LoadOrStore(wctx.ID, time.Now())
	e.pendingMu.Unlock()
	if !alreadyRunning {
		defer e.finishIndexingJob(wctx.Root, wctx.ID)
	}

	// Subscribe before discovery: a new file may appear after the inventory is
	// collected, while another language spends minutes generating embeddings.
	// Queued events are replayed once this run has saved its collection states.
	// A watcher failure (e.g. inotify limits) must not block indexing itself.
	if e.watchers != nil {
		if err := e.watchers.Start(wctx.Root, e.handleWatchChange); err != nil {
			logger.Instance.Warn("[IDX] Failed to start watcher for ws=%s: %v", filepath.Base(wctx.Root), err)
		}
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

	// Share one inventory across all language collections. Counting separately
	// and walking again for every language multiplies I/O on data-heavy projects.
	scan, err := e.indexer.ScanWorkspace(ctx, wctx.Root, excludePatterns)
	if err != nil {
		return err
	}
	fileCounts := scan.FileCountResult
	logger.Instance.Info("[IDX] ws=%s file counts: %v (breakdowns: %v)", wsName, fileCounts.Counts, fileCounts.Breakdowns)

	// Index source code before docs/config and generic data, even when generated
	// JSON or Markdown outnumbers the source. Within each tier, dominant languages
	// go first; break ties by name for a deterministic order.
	sort.Slice(languages, func(i, j int) bool {
		left, right := languages[i], languages[j]
		if lp, rp := indexingLanguagePriority(left), indexingLanguagePriority(right); lp != rp {
			return lp < rp
		}
		if fileCounts.Counts[left] != fileCounts.Counts[right] {
			return fileCounts.Counts[left] > fileCounts.Counts[right]
		}
		return left < right
	})
	logger.Instance.Info("[IDX] ws=%s indexing order: %v", wsName, languages)

	// Load or create a shared in-memory IndexStatus for the entire indexing run.
	// This avoids calling LoadIndexStatus (JSON read + parse) on every Progress tick.
	// The single *IndexStatus is updated in-place; only SaveIndexStatus (atomic write)
	// hits the disk, and only every 10 files.
	s := indexer.LoadIndexStatus(wctx.Root)
	if s == nil {
		s = &indexer.IndexStatus{StartedAt: time.Now().UTC().Format(time.RFC3339)}
	}
	if s.Languages == nil {
		s.Languages = make(map[string]indexer.LangStatus)
	}
	// Pre-populate real on_disk counts and extension breakdowns so languages
	// with 0 changed files still appear, and consumers see sub-type detail.
	for _, l := range languages {
		entry := s.Languages[l]
		entry.OnDisk = fileCounts.Counts[l]
		if bd, ok := fileCounts.Breakdowns[l]; ok {
			entry.Breakdown = bd
		}
		s.Languages[l] = entry
	}
	indexer.SaveIndexStatus(wctx.Root, s)

	var indexErrors []string
	for _, lang := range languages {
		if err := ctx.Err(); err != nil {
			return err
		}
		diskTotal := fileCounts.Counts[lang]
		collection := wctx.CollectionName(lang)
		logger.Instance.Info("[IDX] ws=%s lang=%s ▶ starting (on_disk=%d)", wsName, lang, diskTotal)

		// ticked records whether the indexer reported any progress; it does not
		// when no file of this language changed.
		ticked := false
		// lastStatusWrite throttles status writes by time rather than by file
		// count: on a slow embedder a count-based gate leaves index_status.json
		// reading "processed: 0" for minutes while indexing is in fact running.
		var lastStatusWrite time.Time

		err := e.indexer.IndexWorkspace(ctx, wctx.Root, collection, indexer.Options{
			Language:        lang,
			WorkspaceName:   wsName,
			ExcludePatterns: excludePatterns,
			Recreate:        recreate,
			Scan:            scan,
			Progress: func(doneFiles, totalFiles int) {
				// Throttle disk I/O: at most one write every 2s, plus the last file.
				if doneFiles != totalFiles && time.Since(lastStatusWrite) < 2*time.Second {
					return
				}
				lastStatusWrite = time.Now()
				ticked = true
				ls := s.Languages[lang]
				ls.OnDisk = diskTotal   // real total files on disk
				ls.Changed = totalFiles // files that needed re-indexing this run
				ls.Processed = processedCount(diskTotal, totalFiles, doneFiles)
				s.Languages[lang] = ls
				indexer.SaveIndexStatus(wctx.Root, s)
			},
		})
		if errors.Is(err, indexer.ErrWorkspaceChanged) {
			return err
		}
		if err == nil && !ticked {
			// Nothing changed: every file on disk is already indexed. Without
			// this the previous (possibly inflated) count would stick.
			ls := s.Languages[lang]
			ls.OnDisk = diskTotal
			ls.Changed = 0
			ls.Processed = diskTotal
			s.Languages[lang] = ls
			indexer.SaveIndexStatus(wctx.Root, s)
		}
		if err != nil {
			logger.Instance.Error("[IDX] ws=%s lang=%s ❌ failed: %v", wsName, lang, err)
			indexErrors = append(indexErrors, fmt.Sprintf("%s: %v", lang, err))
		} else {
			logger.Instance.Info("[IDX] ws=%s lang=%s ✓ done", wsName, lang)
		}
	}

	if len(indexErrors) > 0 {
		return fmt.Errorf("indexing failed for %d language(s): %s. Check if Ollama is running and the embedding model is available",
			len(indexErrors), strings.Join(indexErrors, "; "))
	}

	return nil
}

func indexingLanguagePriority(language string) int {
	switch language {
	case "docs":
		return 1
	case "generic":
		return 2
	default:
		return 0
	}
}

// processedCount is how many of the diskTotal files are indexed partway through
// a run. The indexer only queues files that are new or changed (totalFiles);
// every other file on disk is already indexed, so the count is everything
// except what is still queued. Adding doneFiles to the previous count instead
// counted a modified file twice and never dropped deleted ones.
func processedCount(diskTotal, totalFiles, doneFiles int) int {
	n := diskTotal - (totalFiles - doneFiles)
	if n < 0 {
		return 0
	}
	if n > diskTotal {
		return diskTotal
	}
	return n
}

// Close cancels the engine's background lifecycle context, stops all workspace watchers,
// and waits for in-flight background indexing goroutines to exit cleanly.
func (e *Engine) Close() {
	e.StopWatchers()
	e.bgWg.Wait()
}

// StopWatchers stops all workspace watchers and cancels the background lifecycle context.
func (e *Engine) StopWatchers() {
	if e.lifecycleCancel != nil {
		e.lifecycleCancel()
	}
	if e.watchers != nil {
		e.watchers.StopAll()
	}
	e.bgWg.Wait()
}

func (e *Engine) handleWatchChange(ctx context.Context, root string, changedFiles []string) error {
	wctx, err := e.DetectContextAsRoot(ctx, root, false)
	if err != nil {
		return err
	}
	if wctx.ReindexRequired {
		e.startIndexing(wctx.Root, wctx.ID, nil, false, true)
		return nil
	}
	// StartIndexingAsync atomically queues the batch if a job is already active.
	e.StartIndexingAsync(wctx.Root, wctx.ID, changedFiles, false)
	return nil
}

// ─── Stale File Cleanup ──────────────────────────────────────────────────────

// staleCooldown prevents repeatedly deleting the same file from the DB
// across consecutive search queries. Entries expire after 10 minutes.
const (
	staleCooldown        = 10 * time.Minute
	staleCleanupCacheMax = 2000
)

// staleCleanupCache tracks recently cleaned file paths to avoid repeated deletes.
// Key: "wsID:filePath", Value: time.Time of last cleanup.
var (
	staleCleanupCache      sync.Map
	staleCleanupCacheCount atomic.Int64
)

// CleanupStaleFiles removes vectors for deleted files from ALL language collections
// of a workspace. It runs asynchronously and deduplicates to avoid hammering Qdrant.
func (e *Engine) CleanupStaleFiles(wsID string, staleFiles []string) {
	if len(staleFiles) == 0 {
		return
	}

	// Bound staleCleanupCache size so it cannot grow without bound in a long-running daemon.
	if staleCleanupCacheCount.Load() >= staleCleanupCacheMax {
		staleCleanupCache.Range(func(k, _ any) bool {
			staleCleanupCache.Delete(k)
			return true
		})
		staleCleanupCacheCount.Store(0)
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
		} else {
			staleCleanupCacheCount.Add(1)
		}
		staleCleanupCache.Store(cacheKey, now)
		toClean = append(toClean, f)
	}

	if len(toClean) == 0 {
		return
	}

	go func() {
		ctx, cancel := context.WithTimeout(e.bgContext(), 30*time.Second)
		defer cancel()

		langs := parser.SupportedLanguages()
		collectionExists := make(map[string]bool, len(langs))
		var totalDeleted int

		for _, filePath := range toClean {
			for _, lang := range langs {
				collection := CollectionNameFor(wsID, lang)

				// Check collection exists once per collection per CleanupStaleFiles pass
				exists, checked := collectionExists[collection]
				if !checked {
					var err error
					exists, err = e.search.CollectionExists(ctx, collection)
					if err != nil {
						exists = false
					}
					collectionExists[collection] = exists
				}
				if !exists {
					continue
				}

				if err := e.search.DeleteByFilter(ctx, collection, "file_path", filePath); err != nil {
					logger.Instance.Warn("[STALE] Failed to delete vectors for %s from %s: %v", filePath, collection, err)
					// Remove from cache so retry is possible next query
					if _, loaded := staleCleanupCache.LoadAndDelete(wsID + ":" + filePath); loaded {
						staleCleanupCacheCount.Add(-1)
					}
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

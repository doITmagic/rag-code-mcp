package indexer

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/doITmagic/rag-code-mcp/internal/healthcheck"
	"github.com/doITmagic/rag-code-mcp/internal/logger"
	"github.com/doITmagic/rag-code-mcp/pkg/llm"
	"github.com/doITmagic/rag-code-mcp/pkg/parser"
	"github.com/doITmagic/rag-code-mcp/pkg/storage"
)


const (
	deleteCollectionTimeout = 10 * time.Second
	deleteCollectionMaxWait = 500 * time.Millisecond
)

// Options configures the indexer.
type Options struct {
	Language        string
	WorkspaceName   string // basename of workspace root, used for logging
	ExcludePatterns []string
	Recreate        bool
	Progress        func(doneFiles, totalFiles int)
}

// Service is the main indexing engine that handles file walking, change detection, and vector storage.
type Service struct {
	embedder     llm.Provider
	store        storage.VectorStore
	lastActivity atomic.Int64 // Tracks Unix elapsed time of the last successful chunk embedding
}

// NewService creates a new indexer service.
func NewService(embedder llm.Provider, store storage.VectorStore) *Service {
	s := &Service{
		embedder: embedder,
		store:    store,
	}
	s.lastActivity.Store(time.Now().Unix())
	return s
}

// IndexWorkspace performs a full or incremental index of a workspace.
func (s *Service) IndexWorkspace(ctx context.Context, root string, collection string, opts Options) error {
	statePath := filepath.Join(root, ".ragcode", "state.json")
	state, err := LoadState(statePath)
	if err != nil {
		logger.Instance.Warn("Failed to load index state for %s: %v", root, err)
		state = NewState()
	}

	if opts.Recreate {
		logger.Instance.Info("Recreate flag set, ignoring existing state for %s", root)
		state = NewState()
	}

	// 1. Scan for changes
	var changedFiles []string
	err = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			name := d.Name()
			// Basic exclusion
			if strings.HasPrefix(name, ".") || name == "vendor" || name == "node_modules" {
				return filepath.SkipDir
			}
			// User exclusion
			for _, p := range opts.ExcludePatterns {
				if name == p {
					return filepath.SkipDir
				}
			}
			return nil
		}

		info, err := d.Info()
		if err != nil {
			return nil
		}

		if state.IsChanged(path, info) {
			changedFiles = append(changedFiles, path)
		}
		return nil
	})

	if err != nil {
		return fmt.Errorf("failed to scan workspace: %w", err)
	}

	// 2. Filter files by language and supported parser
	var filteredFiles []string
	for _, p := range changedFiles {
		a := parser.GetByFile(p)
		if a == nil {
			continue // Unrecognized file type
		}
		if opts.Language != "" && a.Name() != opts.Language {
			continue // Does not match requested language layer
		}
		filteredFiles = append(filteredFiles, p)
	}
	changedFiles = filteredFiles

	if len(changedFiles) == 0 {
		logger.Instance.Debug("No changes detected in %s (Language: %s)", root, opts.Language)
		return nil
	}

	// 3. Ensure collection exists ONLY if we have files to index
	if opts.Recreate {
		logger.Instance.Info("Dropping collection %s for recreation", collection)
		if err := s.deleteCollectionForRecreate(ctx, collection); err != nil {
			logger.Instance.Error("Failed to drop collection %s: %v", collection, err)
			return err
		}
	}

	exists, err := s.store.CollectionExists(ctx, collection)
	if err != nil {
		logger.Instance.Error("Failed to check collection status for %s: %v", collection, err)
		return fmt.Errorf("failed to check collection status: %w", err)
	}
	if !exists {
		logger.Instance.Info("Creating collection %s", collection)
		// We'll use a dummy embedding to get the dimension
		dummy, err := s.embedder.Embed(ctx, "test")
		if err != nil {
			logger.Instance.Error("Failed to get embedding dimensions for %s: %v", collection, err)
			return fmt.Errorf("failed to get embedding dimensions: %w", err)
		}
		if err := s.store.CreateCollection(ctx, collection, len(dummy)); err != nil {
			logger.Instance.Error("Failed to create collection %s: %v", collection, err)
			return fmt.Errorf("failed to create collection: %w", err)
		}
	}

	wsName := opts.WorkspaceName
	if wsName == "" {
		wsName = filepath.Base(root)
	}

	totalFiles := len(changedFiles)
	logger.Instance.Info("[IDX] ws=%s lang=%s ▶ %d file(s) to index", wsName, opts.Language, totalFiles)

	// Ensure the embedding model is loaded in Ollama's memory before starting.
	// If another program evicted it, this will reload it (with up to 2min timeout).
	if ollamaProvider, ok := unwrapOllamaProvider(s.embedder); ok {
		if err := ollamaProvider.EnsureLoaded(ctx); err != nil {
			logger.Instance.Error("[IDX] ws=%s lang=%s ❌ embedding model not available: %v", wsName, opts.Language, err)
			return fmt.Errorf("embedding model not available: %w", err)
		}
	}

	// 4. File-level counters for watchdog (accessed from two goroutines via atomic).
	var doneFiles atomic.Int64

	// Dedicated periodic-save goroutine + stall watchdog: detects silent deadlocks.
	saveStop := make(chan struct{})
	go func() {
		saveTicker := time.NewTicker(10 * time.Second)
		stallTicker := time.NewTicker(60 * time.Second)
		defer saveTicker.Stop()
		defer stallTicker.Stop()
		stallCount := 0
		for {
			select {
			case <-saveTicker.C:
				if err := state.Save(statePath); err != nil {
					logger.Instance.Warn("[IDX] ws=%s lang=%s periodic state save failed: %v", wsName, opts.Language, err)
				}
			case <-stallTicker.C:
				current := doneFiles.Load()
				lastActivitySec := s.lastActivity.Load()
				elapsedSinceActivity := time.Now().Unix() - lastActivitySec

				if current < int64(totalFiles) && elapsedSinceActivity >= 60 {
					stallCount++
					logger.Instance.Warn("[IDX] ws=%s lang=%s ⚠️ STALL: no embed activity for %ds [%d/%d] (stall #%d)",
						wsName, opts.Language, elapsedSinceActivity, current, totalFiles, stallCount)
					if stallCount >= 2 {
						if err := healthcheck.PingOllama(""); err != nil {
							logger.Instance.Error("[IDX] ws=%s lang=%s ❌ Ollama unresponsive: %v — forcing restart", wsName, opts.Language, err)
						} else {
							logger.Instance.Error("[IDX] ws=%s lang=%s ❌ Ollama ping OK but embed goroutine STALLED — forcing restart", wsName, opts.Language)
						}
						attemptOllamaRestart()
					}
					if stallCount >= 3 {
						buf := make([]byte, 8192)
						n := runtime.Stack(buf, true)
						logger.Instance.Error("[IDX] ws=%s lang=%s goroutine dump:\n%s", wsName, opts.Language, string(buf[:n]))
					}
				} else {
					stallCount = 0
				}
			case <-saveStop:
				return
			}
		}
	}()

	// 5. Sequential file processing — no worker pool, no semaphore.
	// Embed is serial in Ollama anyway (numWorkers=1 in IndexItems), so parallelism
	// here only added complexity without meaningful throughput gain.
	var fileErrs []string
	for _, path := range changedFiles {
		n := int(doneFiles.Add(1))
		pct := n * 100 / totalFiles

		logger.Instance.Debug("[IDX] ws=%s lang=%s [%d/%d] %s (%d%%)",
			wsName, opts.Language, n, totalFiles, filepath.Base(path), pct)

		if opts.Progress != nil {
			opts.Progress(n, totalFiles)
		}

		symCount, indexErr := s.IndexFile(ctx, collection, path, state)
		if indexErr != nil {
			logger.Instance.Warn("[IDX] ws=%s lang=%s ⚠️ %s: %v", wsName, opts.Language, filepath.Base(path), indexErr)
			fileErrs = append(fileErrs, fmt.Sprintf("%s: %v", path, indexErr))
		} else {
			logger.Instance.Debug("[IDX] ws=%s lang=%s %s → %d symbol(s)", wsName, opts.Language, filepath.Base(path), symCount)
		}
	}

	close(saveStop)

	if len(fileErrs) > 0 {
		logger.Instance.Warn("[IDX] ws=%s lang=%s %d file(s) failed to index", wsName, opts.Language, len(fileErrs))
	}

	// 6. Save state
	if err := state.Save(statePath); err != nil {
		logger.Instance.Warn("[IDX] ws=%s lang=%s failed to save state: %v", wsName, opts.Language, err)
	}

	logger.Instance.Info("[IDX] ws=%s lang=%s ✅ DONE %d file(s)", wsName, opts.Language, totalFiles)
	return nil
}

// attemptOllamaRestart tries to restart Ollama when it becomes unresponsive.
// Attempts: 1) systemctl restart ollama, 2) fallback to `ollama serve` in background.
// After restart, waits up to 15s for Ollama to become responsive again.
func attemptOllamaRestart() {
	logger.Instance.Info("⚙️ Executing forced Ollama auto-recovery sequence...")

	// Strategy 1: systemctl (works if Ollama is managed as a systemd service)
	out, err := exec.Command("systemctl", "restart", "ollama").CombinedOutput()
	if err != nil {
		logger.Instance.Warn("⚠️ 'systemctl restart ollama' failed: %v. Output: %s", err, strings.TrimSpace(string(out)))

		// Strategy 2: kill any existing ollama processes and start fresh
		logger.Instance.Info("🔪 Forcefully killing any existing 'ollama serve' processes via pkill...")
		_ = exec.Command("pkill", "-9", "-f", "ollama serve").Run()
		time.Sleep(1 * time.Second)

		logger.Instance.Info("🚀 Starting standalone 'ollama serve' in background...")
		cmd := exec.Command("ollama", "serve")
		cmd.Stdout = nil
		cmd.Stderr = nil
		if err := cmd.Start(); err != nil {
			logger.Instance.Error("❌ Failed to start ollama serve: %v", err)
			return
		}
		logger.Instance.Info("✅ Started 'ollama serve' successfully (PID %d)", cmd.Process.Pid)
		// Don't wait — it runs as a daemon
		go func() { _ = cmd.Wait() }()
	} else {
		logger.Instance.Info("✅ 'systemctl restart ollama' executed successfully.")
	}

	// Wait for Ollama to come back up
	logger.Instance.Info("⏳ Waiting for Ollama HTTP heartbeat to return (max 15s)...")
	for i := 0; i < 5; i++ {
		time.Sleep(3 * time.Second)
		if err := healthcheck.PingOllama(""); err == nil {
			logger.Instance.Info("🎉 Ollama HTTP API is FULLY ONLINE after recovery! Resuming indexing.")
			return
		}
		logger.Instance.Warn("⏱️ Ollama not yet ready... (%d/5 attempts)", i+1)
	}
	logger.Instance.Error("💀 Ollama FATAL: Did not recover after restart limits.")
}

func (s *Service) deleteCollectionForRecreate(ctx context.Context, collection string) error {
	deadline := time.Now().Add(deleteCollectionTimeout)
	backoff := 50 * time.Millisecond

	for {
		if err := s.store.DeleteCollection(ctx, collection); err != nil {
			log.Printf("[DEBUG] DeleteCollection %s error: %v", collection, err)
		}

		exists, err := s.store.CollectionExists(ctx, collection)
		if err != nil {
			return fmt.Errorf("failed to check collection status after delete: %w", err)
		}
		if !exists {
			return nil
		}

		if time.Now().After(deadline) {
			return fmt.Errorf("failed to delete collection %s within %s", collection, deleteCollectionTimeout)
		}

		if backoff > deleteCollectionMaxWait {
			backoff = deleteCollectionMaxWait
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(backoff):
		}
		backoff *= 2
	}
}

func (s *Service) IndexFile(ctx context.Context, collection, path string, state *State) (int, error) {
	// We no longer apply a hard 2-minute timeout here.
	// Files with hundreds of symbols processed on a low-RAM or saturated parallel system
	// naturally take >2m. The individual embed requests have 30s timeouts which is sufficient protection.
	a := parser.GetByFile(path)
	if a == nil {
		logger.Instance.Debug("Skipping unsupported file type: %s", path)
		return 0, nil
	}

	res, err := a.Analyze(ctx, path)
	if err != nil {
		logger.Instance.Error("Analyze failed for %s: %v", path, err)
		return 0, fmt.Errorf("analyze failed: %w", err)
	}

	// Remove old points for this file if we are updating
	if err := s.store.DeleteByFilter(ctx, collection, "file_path", path); err != nil {
		logger.Instance.Warn("Failed to delete old points for %s: %v", path, err)
	}

	if len(res.Symbols) > 0 {
		// Index new symbols
		if err := s.IndexItems(ctx, collection, res.Symbols); err != nil {
			return 0, err
		}
	}

	// Update state
	if info, err := os.Stat(path); err == nil {
		state.UpdateFile(path, info)
	}

	return len(res.Symbols), nil
}

// circuitBreakerThreshold is the number of consecutive embed failures that
// triggers an Ollama health check and potential restart before continuing.
const circuitBreakerThreshold = 2

// unwrapOllamaProvider extracts the underlying *OllamaLLMProvider from the Provider
// chain (which may be wrapped in RetryableProvider).
func unwrapOllamaProvider(p llm.Provider) (*llm.OllamaLLMProvider, bool) {
	// Direct
	if op, ok := p.(*llm.OllamaLLMProvider); ok {
		return op, true
	}
	// Wrapped in RetryableProvider — try to extract via Unwrap.
	type unwrapper interface {
		Unwrap() llm.Provider
	}
	if uw, ok := p.(unwrapper); ok {
		if op, ok := uw.Unwrap().(*llm.OllamaLLMProvider); ok {
			return op, true
		}
	}
	return nil, false
}

// ensureOllamaAlive checks if Ollama is responsive and the embedding model is loaded.
// If not, it attempts a restart and model reload. Returns an error only if recovery fails.
// Respects the parent context — returns immediately if ctx is cancelled.
func (s *Service) ensureOllamaAlive(ctx context.Context) error {
	// Bail out early if context is already done (e.g. test timeout)
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}

	if err := healthcheck.PingOllama(""); err != nil {
		logger.Instance.Warn("🔌 Circuit breaker: Ollama is unresponsive, attempting restart...")
		attemptOllamaRestart()

		// Wait for recovery with short polling — exit early on context cancellation
		for i := 0; i < 10; i++ {
			select {
			case <-ctx.Done():
				return fmt.Errorf("context cancelled during Ollama recovery: %w", ctx.Err())
			case <-time.After(3 * time.Second):
			}
			if err := healthcheck.PingOllama(""); err == nil {
				logger.Instance.Info("✅ Circuit breaker: Ollama recovered after restart")
				break
			}
			logger.Instance.Warn("Circuit breaker: waiting for Ollama... (%d/10)", i+1)
			if i == 9 {
				return fmt.Errorf("Ollama did not recover after restart (waited 30s)")
			}
		}
	}

	// Ollama is alive — now ensure the embedding model is loaded in memory
	if ollamaProvider, ok := unwrapOllamaProvider(s.embedder); ok {
		loadCtx, cancel := context.WithTimeout(ctx, 2*time.Minute)
		defer cancel()
		if err := ollamaProvider.EnsureLoaded(loadCtx); err != nil {
			return fmt.Errorf("embedding model failed to reload: %w", err)
		}
	}

	return nil
}

// IndexItems handles the standard embedding and storage logic for a list of symbols using a worker pool.
// Includes a circuit breaker: after circuitBreakerThreshold consecutive embed failures,
// pauses to check/restart Ollama before continuing — avoids wasting retries against a dead service.
func (s *Service) IndexItems(ctx context.Context, collection string, symbols []parser.Symbol) error {
	if len(symbols) == 0 {
		return nil
	}

	// Ollama processes embeds serially — multiple workers only queue up requests
	// and increase latency for concurrent search queries. Keep 1 worker.
	numWorkers := 1

	type result struct {
		point storage.Point
		err   error
	}

	jobs := make(chan parser.Symbol, len(symbols))
	results := make(chan result, len(symbols))

	var wg sync.WaitGroup
	for w := 0; w < numWorkers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			consecutiveFailures := 0

			for sym := range jobs {
				// Circuit breaker: if we've had consecutive failures, check Ollama before retrying
				if consecutiveFailures >= circuitBreakerThreshold {
					// Check context first — if already cancelled, abort immediately
					select {
					case <-ctx.Done():
						results <- result{err: ctx.Err()}
						for range jobs {
						}
						return
					default:
					}
					logger.Instance.Warn("🔌 Circuit breaker tripped after %d consecutive embed failures — checking Ollama health", consecutiveFailures)
					if err := s.ensureOllamaAlive(ctx); err != nil {
						logger.Instance.Error("🔴 Circuit breaker: Ollama unrecoverable: %v — aborting remaining embeds", err)
						results <- result{err: fmt.Errorf("ollama unrecoverable after %d consecutive failures: %w", consecutiveFailures, err)}
						// Drain remaining jobs so we don't deadlock
						for range jobs {
						}
						return
					}
					// Ollama is back — reset counter, add a small cooldown
					consecutiveFailures = 0
					time.Sleep(2 * time.Second)
				}

				// Embed text construction
				embedText := fmt.Sprintf("%s\n%s\n%s\n%s", sym.Package, sym.Name, sym.Signature, sym.Content)
				if sym.Docstring != "" {
					embedText = sym.Docstring + "\n" + embedText
				}

				embedCtx, embedCancel := context.WithTimeout(ctx, 30*time.Second)
				vector64, err := s.embedder.Embed(embedCtx, embedText)
				embedCancel()

				if err != nil {
					consecutiveFailures++
					logger.Instance.Warn("Embed failed for %s (consecutive failures: %d): %v", sym.Name, consecutiveFailures, err)
					results <- result{err: fmt.Errorf("failed to embed %s: %w", sym.Name, err)}
					continue
				}

				// Success — reset circuit breaker counter and update activity watchdog
				consecutiveFailures = 0
				s.lastActivity.Store(time.Now().Unix())

				// Throttle: small pause between embeds to avoid overwhelming Ollama.
				// 150ms adds ~15s per 100 symbols — negligible vs total indexing time,
				// but prevents Ollama from freezing under sustained concurrent load.
				time.Sleep(10 * time.Millisecond)

				vector := make([]float32, len(vector64))
				for i, v := range vector64 {
					vector[i] = float32(v)
				}

				idKey := fmt.Sprintf("%s:%s:%d:%d", sym.FilePath, sym.Name, sym.StartLine, sym.EndLine)
				id := fmt.Sprintf("%x", sha256.Sum256([]byte(idKey)))[:32]

				payload := s.symbolToMap(sym)
				payload["text"] = embedText

				results <- result{
					point: storage.Point{
						ID:      id,
						Vector:  vector,
						Payload: payload,
					},
				}
			}
		}()
	}

	for _, sym := range symbols {
		jobs <- sym
	}
	close(jobs)

	wg.Wait()
	close(results)

	var allPoints []storage.Point
	for res := range results {
		if res.err != nil {
			return res.err
		}
		allPoints = append(allPoints, res.point)
	}

	batchSize := 50
	for i := 0; i < len(allPoints); i += batchSize {
		end := i + batchSize
		if end > len(allPoints) {
			end = len(allPoints)
		}

		upsertCtx, upsertCancel := context.WithTimeout(ctx, 30*time.Second)
		_, err := s.store.Upsert(upsertCtx, collection, allPoints[i:end])
		upsertCancel()

		if err != nil {
			return fmt.Errorf("upsert failed: %w", err)
		}
	}

	logger.Instance.Debug("Indexed %d symbols into %s", len(allPoints), collection)
	return nil
}

func (s *Service) symbolToMap(sym parser.Symbol) map[string]interface{} {
	data, _ := json.Marshal(sym)
	var res map[string]interface{}
	if err := json.Unmarshal(data, &res); err != nil {
		// Fallback for simple conversion if unmarshal fails (should not happen with Marshal output)
		return map[string]interface{}{
			"name":      sym.Name,
			"type":      sym.Type,
			"file_path": sym.FilePath,
		}
	}
	return res
}

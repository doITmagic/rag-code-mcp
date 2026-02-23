package indexer

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/doITmagic/rag-code-mcp/pkg/llm"
	"github.com/doITmagic/rag-code-mcp/pkg/parser"
	"github.com/doITmagic/rag-code-mcp/pkg/storage"
)

// Options configures the indexer.
type Options struct {
	Language        string
	ExcludePatterns []string
	Recreate        bool
}

// Service is the main indexing engine that handles file walking, change detection, and vector storage.
type Service struct {
	embedder llm.Provider
	store    storage.VectorStore
}

// NewService creates a new indexer service.
func NewService(embedder llm.Provider, store storage.VectorStore) *Service {
	return &Service{
		embedder: embedder,
		store:    store,
	}
}

// IndexWorkspace performs a full or incremental index of a workspace.
func (s *Service) IndexWorkspace(ctx context.Context, root string, collection string, opts Options) error {
	statePath := filepath.Join(root, ".ragcode", "state.json")
	state, err := LoadState(statePath)
	if err != nil {
		log.Printf("[WARN] Failed to load index state for %s: %v", root, err)
		state = NewState()
	}

	if opts.Recreate {
		log.Printf("[INFO] Recreate flag set, ignoring existing state for %s", root)
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

	// 2. Filter by language if specified
	if opts.Language != "" {
		var filtered []string
		for _, p := range changedFiles {
			if a := parser.GetByFile(p); a != nil && a.Name() == opts.Language {
				filtered = append(filtered, p)
			}
		}
		changedFiles = filtered
	}

	if len(changedFiles) == 0 {
		log.Printf("[INFO] No changes detected in %s (Language: %s)", root, opts.Language)
		return nil
	}

	// 3. Ensure collection exists ONLY if we have files to index
	if opts.Recreate {
		log.Printf("[INFO] Dropping collection %s for recreation", collection)
		if err := s.store.DeleteCollection(ctx, collection); err != nil {
			// ignore error if not exists or other issues (CreateCollection will fail later if needed)
			log.Printf("[DEBUG] DeleteCollection %s error: %v", collection, err)
		}
	}

	exists, err := s.store.CollectionExists(ctx, collection)
	if err != nil {
		return fmt.Errorf("failed to check collection status: %w", err)
	}
	if !exists {
		log.Printf("[INFO] Creating collection %s", collection)
		// We'll use a dummy embedding to get the dimension
		dummy, err := s.embedder.Embed(ctx, "test")
		if err != nil {
			return fmt.Errorf("failed to get embedding dimensions: %w", err)
		}
		if err := s.store.CreateCollection(ctx, collection, len(dummy)); err != nil {
			return fmt.Errorf("failed to create collection: %w", err)
		}
	}

	totalFiles := len(changedFiles)
	log.Printf("[INFO] Indexing %d changed files in %s (Language: %s)", totalFiles, root, opts.Language)

	// 4. Process changed files in parallel (file-level worker pool).
	// State.UpdateFile is mutex-protected; errors are collected safely.
	numFileWorkers := runtime.NumCPU() / 2
	if numFileWorkers < 2 {
		numFileWorkers = 2
	}
	if numFileWorkers > 8 {
		numFileWorkers = 8
	}

	filePaths := make(chan string, totalFiles)
	for _, p := range changedFiles {
		filePaths <- p
	}
	close(filePaths)

	var (
		fileWg    sync.WaitGroup
		errMu     sync.Mutex
		fileErrs  []string
		doneFiles atomic.Int64
	)

	for i := 0; i < numFileWorkers; i++ {
		fileWg.Add(1)
		go func() {
			defer fileWg.Done()
			for path := range filePaths {
				n := int(doneFiles.Add(1))
				remaining := totalFiles - n
				pct := remaining * 100 / totalFiles
				fmt.Fprintf(os.Stderr, "\r[INDEX] %s: %d%% (%d/%d files left)   ", opts.Language, pct, remaining, totalFiles)
				if err := s.IndexFile(ctx, collection, path, state); err != nil {
					log.Printf("[ERROR] Failed to index %s: %v", path, err)
					errMu.Lock()
					fileErrs = append(fileErrs, fmt.Sprintf("%s: %v", path, err))
					errMu.Unlock()
				}
			}
		}()
	}

	fileWg.Wait()
	fmt.Fprintf(os.Stderr, "\r[INDEX] %s: done (%d files indexed)            \n", opts.Language, totalFiles)

	if len(fileErrs) > 0 {
		log.Printf("[WARN] %d file(s) failed to index in %s", len(fileErrs), root)
	}

	// 5. Save state
	if err := state.Save(statePath); err != nil {
		log.Printf("[WARN] Failed to save index state for %s: %v", root, err)
	}

	return nil
}

func (s *Service) IndexFile(ctx context.Context, collection, path string, state *State) error {
	a := parser.GetByFile(path)
	if a == nil {
		return nil // Unsupported file type
	}

	// Double check language if we have a state
	// (Note: we could pass language here too if we want strict enforcement)

	res, err := a.Analyze(ctx, path)
	if err != nil {
		return fmt.Errorf("analyze failed: %w", err)
	}

	// Remove old points for this file if we are updating
	if err := s.store.DeleteByFilter(ctx, collection, "file_path", path); err != nil {
		log.Printf("[WARN] Failed to delete old points for %s: %v", path, err)
	}

	if len(res.Symbols) == 0 {
		return nil
	}

	// Index new symbols
	if err := s.IndexItems(ctx, collection, res.Symbols); err != nil {
		return err
	}

	// Update state
	if info, err := os.Stat(path); err == nil {
		state.UpdateFile(path, info)
	}

	return nil
}

// IndexItems handles the standard embedding and storage logic for a list of symbols using a worker pool.
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
			for sym := range jobs {
				// Embed text construction
				embedText := fmt.Sprintf("%s\n%s\n%s\n%s", sym.Package, sym.Name, sym.Signature, sym.Content)
				if sym.Docstring != "" {
					embedText = sym.Docstring + "\n" + embedText
				}

				vector64, err := s.embedder.Embed(ctx, embedText)
				if err != nil {
					results <- result{err: fmt.Errorf("failed to embed %s: %w", sym.Name, err)}
					continue
				}

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
		if _, err := s.store.Upsert(ctx, collection, allPoints[i:end]); err != nil {
			return fmt.Errorf("upsert failed: %w", err)
		}
	}

	log.Printf("[INFO] Indexed %d symbols into %s", len(allPoints), collection)
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

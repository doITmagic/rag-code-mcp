package workspace

import (
	"bufio"
	"context"
	"fmt"
	"hash/fnv"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/doITmagic/rag-code-mcp/internal/config"
	"github.com/doITmagic/rag-code-mcp/internal/llm"
	"github.com/doITmagic/rag-code-mcp/internal/memory"
	"github.com/doITmagic/rag-code-mcp/internal/ragcode"
	"github.com/doITmagic/rag-code-mcp/internal/storage"
)

// Manager manages workspace detection, collection management, and indexing
type Manager struct {
	detector *Detector
	cache    *Cache
	qdrant   *storage.QdrantClient
	llm      llm.Provider
	config   *config.Config

	// Indexing state
	indexingMu sync.RWMutex
	indexing   map[string]bool // workspace ID -> is indexing

	// Memory cache
	memoryMu sync.RWMutex
	memories map[string]memory.LongTermMemory // collection name -> memory

	// Workspace scan fingerprints to detect file changes per language
	scanMu           sync.RWMutex
	scanFingerprints map[string]string
}

type workspaceScan struct {
	LanguageDirs map[string][]string
	DocFiles     []string
	TotalFiles   int
	GeneratedAt  time.Time
}

var defaultSkipDirs = map[string]struct{}{
	".git":         {},
	".idea":        {},
	".vscode":      {},
	"node_modules": {},
	"vendor":       {},
	"dist":         {},
	"build":        {},
	"storage":      {},
	"public":       {},
}

func addDirForLanguage(scan *workspaceScan, cache map[string]map[string]struct{}, language, dir string) {
	if dir == "" {
		return
	}
	lang := strings.ToLower(language)
	if _, ok := cache[lang]; !ok {
		cache[lang] = make(map[string]struct{})
	}
	if _, exists := cache[lang][dir]; exists {
		return
	}
	cache[lang][dir] = struct{}{}
	if scan.LanguageDirs == nil {
		scan.LanguageDirs = make(map[string][]string)
	}
	scan.LanguageDirs[lang] = append(scan.LanguageDirs[lang], dir)
}

func (m *Manager) scanWorkspace(info *Info) (*workspaceScan, error) {
	scan := &workspaceScan{
		LanguageDirs: make(map[string][]string),
		DocFiles:     make([]string, 0),
		GeneratedAt:  time.Now(),
	}
	dirCache := make(map[string]map[string]struct{})
	err := filepath.WalkDir(info.Root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if path == info.Root {
				return nil
			}
			if _, skip := defaultSkipDirs[d.Name()]; skip {
				return filepath.SkipDir
			}
			return nil
		}

		scan.TotalFiles++
		ext := strings.ToLower(filepath.Ext(path))
		switch ext {
		case ".go":
			addDirForLanguage(scan, dirCache, "go", filepath.Dir(path))
		case ".php":
			addDirForLanguage(scan, dirCache, "php", filepath.Dir(path))
		case ".html", ".htm":
			addDirForLanguage(scan, dirCache, "html", filepath.Dir(path))
		case ".md":
			scan.DocFiles = append(scan.DocFiles, path)
		default:
			// ignored
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return scan, nil
}

func (s *workspaceScan) fingerprint(language string) string {
	h := fnv.New64a()
	lang := strings.ToLower(language)
	fmt.Fprintf(h, "%s|%d|%d", lang, s.TotalFiles, len(s.DocFiles))
	dirs := append([]string(nil), s.LanguageDirs[lang]...)
	sort.Strings(dirs)
	for _, dir := range dirs {
		h.Write([]byte(dir))
		h.Write([]byte("|"))
	}
	docs := append([]string(nil), s.DocFiles...)
	sort.Strings(docs)
	for _, doc := range docs {
		h.Write([]byte(doc))
		h.Write([]byte("|"))
	}
	return fmt.Sprintf("%x", h.Sum64())
}

func (m *Manager) fingerprintKey(info *Info, language string) string {
	return info.ID + "-" + strings.ToLower(language)
}

func (m *Manager) recordFingerprint(info *Info, language string, scan *workspaceScan) {
	if scan == nil {
		return
	}
	fp := scan.fingerprint(language)
	key := m.fingerprintKey(info, language)
	m.scanMu.Lock()
	if m.scanFingerprints == nil {
		m.scanFingerprints = make(map[string]string)
	}
	m.scanFingerprints[key] = fp
	m.scanMu.Unlock()
}

// NeedsReindex rescans the workspace and determines if tracked files changed for the language.
// Returns true when changes are detected or no previous fingerprint exists.
func (m *Manager) NeedsReindex(info *Info, language string) (bool, error) {
	scan, err := m.scanWorkspace(info)
	if err != nil {
		return false, err
	}
	fp := scan.fingerprint(language)
	key := m.fingerprintKey(info, language)
	m.scanMu.RLock()
	prev := m.scanFingerprints[key]
	m.scanMu.RUnlock()
	if prev == "" {
		return true, nil
	}
	return prev != fp, nil
}

// NewManager creates a new workspace manager
func NewManager(qdrant *storage.QdrantClient, llm llm.Provider, cfg *config.Config) *Manager {
	// Create detector with config or defaults
	var detector *Detector
	if cfg != nil && cfg.Workspace.Enabled {
		detector = NewDetectorWithConfig(
			cfg.Workspace.DetectionMarkers,
			cfg.Workspace.ExcludePatterns,
		)
	} else {
		detector = NewDetector()
	}

	return &Manager{
		detector: detector,
		cache:    NewCache(5 * time.Minute),
		qdrant:   qdrant,
		llm:      llm,
		config:   cfg,
		indexing: make(map[string]bool),
		memories: make(map[string]memory.LongTermMemory),
	}
}

// DetectWorkspace detects workspace from tool parameters
func (m *Manager) DetectWorkspace(params map[string]interface{}) (*Info, error) {
	// Try to extract file path for cache key
	var cacheKey string
	for _, param := range []string{"file_path", "filePath", "path", "file"} {
		if value, ok := params[param]; ok {
			if path, ok := value.(string); ok && path != "" {
				cacheKey = path
				break
			}
		}
	}

	// Check cache if we have a key
	if cacheKey != "" {
		if cached := m.cache.Get(cacheKey); cached != nil {
			return cached, nil
		}
	}

	// Detect workspace
	info, err := m.detector.DetectFromParams(params)
	if err != nil {
		return nil, err
	}

	// Set collection prefix from config
	if m.config != nil && m.config.Workspace.CollectionPrefix != "" {
		info.CollectionPrefix = m.config.Workspace.CollectionPrefix
	}

	// Cache result
	if cacheKey != "" {
		m.cache.Set(cacheKey, info)
	}

	return info, nil
}

// GetMemoryForWorkspace returns a memory instance for the workspace
// Creates collection and triggers indexing if needed
// Deprecated: Use GetMemoryForWorkspaceLanguage for multi-language support
func (m *Manager) GetMemoryForWorkspace(ctx context.Context, info *Info) (memory.LongTermMemory, error) {
	// For backward compatibility, use first detected language or fallback to ProjectType
	language := info.ProjectType
	if len(info.Languages) > 0 {
		language = info.Languages[0]
	}

	return m.GetMemoryForWorkspaceLanguage(ctx, info, language)
}

// GetMemoryForWorkspaceLanguage returns a memory instance for a specific language in the workspace
// Creates collection and triggers indexing if needed
func (m *Manager) GetMemoryForWorkspaceLanguage(ctx context.Context, info *Info, language string) (memory.LongTermMemory, error) {
	// Validate workspace root - reject suspicious directories
	homeDir, _ := os.UserHomeDir()
	if info.Root == "/" || info.Root == homeDir || strings.HasPrefix(info.Root, "/tmp") {
		return nil, fmt.Errorf(
			"invalid workspace root '%s'. "+
				"Please provide a file path inside a valid project directory with workspace markers "+
				"(e.g., .git, go.mod, composer.json, package.json)",
			info.Root,
		)
	}

	collectionName := info.CollectionNameForLanguage(language)

	// Check memory cache
	m.memoryMu.RLock()
	if mem, ok := m.memories[collectionName]; ok {
		m.memoryMu.RUnlock()
		return mem, nil
	}
	m.memoryMu.RUnlock()

	// Create collection-specific client FIRST (before checking existence)
	collectionConfig := storage.QdrantConfig{
		URL:        m.config.Storage.VectorDB.URL,
		APIKey:     m.config.Storage.VectorDB.APIKey,
		Collection: collectionName,
	}

	collectionClient, err := storage.NewQdrantClient(collectionConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to create collection client: %w", err)
	}

	// Check if collection exists in Qdrant using collection-specific client
	exists, err := collectionClient.CollectionExists(ctx, collectionName)
	if err != nil {
		collectionClient.Close()
		return nil, fmt.Errorf("failed to check collection: %w", err)
	}

	if !exists {
		// Collection doesn't exist - create it
		log.Printf("📦 Workspace '%s' language '%s' not indexed yet, creating collection...", info.Root, language)
		log.Printf("   Workspace ID: %s", info.ID)
		log.Printf("   Collection name: %s", collectionName)
		log.Printf("   Project type: %s", info.ProjectType)
		log.Printf("   Detected markers: %v", info.Markers)

		// Check workspace limit
		if m.config != nil && m.config.Workspace.MaxWorkspaces > 0 {
			m.memoryMu.RLock()
			currentCount := len(m.memories)
			m.memoryMu.RUnlock()

			if currentCount >= m.config.Workspace.MaxWorkspaces {
				collectionClient.Close()
				return nil, fmt.Errorf("workspace limit reached (%d/%d). Increase max_workspaces in config or clean up old workspaces",
					currentCount, m.config.Workspace.MaxWorkspaces)
			}
		}

		// Get embedding dimension from LLM
		testEmbed, err := m.llm.Embed(ctx, "test")
		if err != nil {
			collectionClient.Close()
			return nil, fmt.Errorf("failed to get embedding dimension: %w", err)
		}
		vectorDim := len(testEmbed)

		// Create collection using collection-specific client
		if err := collectionClient.CreateCollection(ctx, collectionName, vectorDim); err != nil {
			collectionClient.Close()
			return nil, fmt.Errorf("failed to create collection: %w", err)
		}

		log.Printf("✓ Created collection '%s' (dimension: %d)", collectionName, vectorDim)

		// Trigger background indexing only if auto_index is enabled
		if m.config != nil && m.config.Workspace.AutoIndex {
			// Pass a long-lived context for background indexing
			indexCtx := context.Background()
			go m.indexWorkspaceLanguage(indexCtx, info, language, collectionName)
		} else {
			log.Printf("⏸️  Auto-indexing disabled for workspace '%s' language '%s'. Run manual indexing.", info.Root, language)
		}
	}

	// Create memory instance with collection-specific client
	mem := storage.NewQdrantLongTermMemory(collectionClient)

	m.memoryMu.Lock()
	m.memories[collectionName] = mem
	m.memoryMu.Unlock()

	return mem, nil
}

// GetMemoriesForAllLanguages returns memory instances for all detected languages in the workspace
// Creates collections and triggers indexing if needed
func (m *Manager) GetMemoriesForAllLanguages(ctx context.Context, info *Info) (map[string]memory.LongTermMemory, error) {
	if len(info.Languages) == 0 {
		// No languages detected, use ProjectType as fallback
		language := info.ProjectType
		if language == "" || language == "unknown" {
			return nil, fmt.Errorf("no languages detected in workspace: %s", info.Root)
		}

		mem, err := m.GetMemoryForWorkspaceLanguage(ctx, info, language)
		if err != nil {
			return nil, err
		}
		return map[string]memory.LongTermMemory{language: mem}, nil
	}

	memories := make(map[string]memory.LongTermMemory)
	for _, language := range info.Languages {
		mem, err := m.GetMemoryForWorkspaceLanguage(ctx, info, language)
		if err != nil {
			log.Printf("⚠️  Failed to get memory for language '%s': %v", language, err)
			continue
		}
		memories[language] = mem
	}

	if len(memories) == 0 {
		return nil, fmt.Errorf("failed to create any memory instances for workspace: %s", info.Root)
	}

	return memories, nil
}

// indexWorkspaceLanguage indexes a specific language in a workspace in the background
func (m *Manager) indexWorkspaceLanguage(ctx context.Context, info *Info, language string, collectionName string) {
	// Check if already indexing
	indexKey := info.ID + "-" + language
	m.indexingMu.Lock()
	if m.indexing[indexKey] {
		m.indexingMu.Unlock()
		log.Printf("⚠️  Workspace '%s' language '%s' is already being indexed", info.Root, language)
		return
	}
	m.indexing[indexKey] = true
	m.indexingMu.Unlock()

	// Ensure we clear indexing flag when done
	defer func() {
		m.indexingMu.Lock()
		delete(m.indexing, indexKey)
		m.indexingMu.Unlock()
	}()

	log.Printf("🚀 Starting background indexing for workspace: %s", info.Root)
	log.Printf("   Collection: %s", collectionName)
	log.Printf("   Language: %s", language)
	log.Printf("   Project type: %s", info.ProjectType)

	// Create collection-specific memory
	collectionConfig := storage.QdrantConfig{
		URL:        m.config.Storage.VectorDB.URL,
		APIKey:     m.config.Storage.VectorDB.APIKey,
		Collection: collectionName,
	}

	collectionClient, err := storage.NewQdrantClient(collectionConfig)
	if err != nil {
		log.Printf("❌ Failed to create collection client: %v", err)
		return
	}

	ltm := storage.NewQdrantLongTermMemory(collectionClient)

	// Select analyzer based on language (not ProjectType)
	analyzerManager := ragcode.NewAnalyzerManager()
	analyzer := analyzerManager.CodeAnalyzerForProjectType(language)
	if analyzer == nil {
		log.Printf("⚠️ No code analyzer available for language '%s', skipping indexing", language)
		return
	}

	// Scan workspace once to determine relevant paths per language
	scan, err := m.scanWorkspace(info)
	if err != nil {
		log.Printf("❌ Failed to scan workspace '%s': %v", info.Root, err)
		return
	}

	languageDirs := scan.LanguageDirs[strings.ToLower(language)]
	if len(languageDirs) == 0 {
		log.Printf("⚠️ No %s source files detected in workspace '%s'", language, info.Root)
		return
	}

	// Create indexer
	indexer := ragcode.NewIndexer(analyzer, m.llm, ltm)

	// Index the language-specific directories
	startTime := time.Now()
	numChunks, err := indexer.IndexPaths(ctx, languageDirs, collectionName)
	duration := time.Since(startTime)

	if err != nil {
		log.Printf("❌ Indexing failed for workspace '%s' language '%s': %v", info.Root, language, err)
		log.Printf("   You can manually index with: ./bin/index-all -paths %s", info.Root)
		return
	}

	log.Printf("✅ Workspace language indexed successfully in %v", duration)
	log.Printf("   Workspace: %s", info.Root)
	log.Printf("   Language: %s", language)
	log.Printf("   Collection: %s", collectionName)
	log.Printf("   Code chunks indexed: %d", numChunks)

	// Index markdown documentation files automatically using scan result
	numDocs := m.indexMarkdownFiles(ctx, scan.DocFiles, collectionName, ltm)
	if numDocs > 0 {
		log.Printf("   Docs chunks indexed: %d", numDocs)
	}

	m.recordFingerprint(info, language, scan)
}

// indexMarkdownFiles indexes provided markdown files (already discovered during scan)
func (m *Manager) indexMarkdownFiles(ctx context.Context, markdownFiles []string, collectionName string, ltm memory.LongTermMemory) int {
	if len(markdownFiles) == 0 {
		return 0
	}

	log.Printf("📚 Found %d markdown file(s), indexing documentation...", len(markdownFiles))

	totalChunks := 0
	for _, path := range markdownFiles {
		chunks, err := m.indexMarkdownFile(ctx, path, collectionName, ltm)
		if err != nil {
			log.Printf("⚠️  Failed to index markdown file %s: %v", path, err)
			continue
		}
		totalChunks += chunks
	}

	return totalChunks
}

// indexMarkdownFile chunks and indexes a single markdown file
func (m *Manager) indexMarkdownFile(ctx context.Context, path string, collectionName string, ltm memory.LongTermMemory) (int, error) {
	f, err := os.Open(path)
	if err != nil {
		return 0, fmt.Errorf("open %s: %w", path, err)
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	var (
		chunks   []string
		current  strings.Builder
		maxChars = 1000
	)

	flushChunk := func() {
		text := strings.TrimSpace(current.String())
		if text != "" {
			chunks = append(chunks, text)
		}
		current.Reset()
	}

	for scanner.Scan() {
		line := scanner.Text()
		if strings.TrimSpace(line) == "" && current.Len() > 0 {
			flushChunk()
			continue
		}

		if current.Len()+len(line)+1 > maxChars {
			flushChunk()
		}
		if current.Len() > 0 {
			current.WriteString("\n")
		}
		current.WriteString(line)
	}
	if err := scanner.Err(); err != nil {
		return 0, fmt.Errorf("scan %s: %w", path, err)
	}
	flushChunk()

	// Index each chunk
	for i, text := range chunks {
		emb, err := m.llm.Embed(ctx, text)
		if err != nil {
			return i, fmt.Errorf("embed failed for %s chunk %d: %w", path, i, err)
		}

		h := fnv.New64a()
		h.Write([]byte(fmt.Sprintf("%s#%d", path, i)))
		id := fmt.Sprintf("%d", h.Sum64())

		doc := memory.Document{
			ID:        id,
			Content:   text,
			Embedding: emb,
			Metadata: map[string]interface{}{
				"file":       path,
				"chunk_id":   i,
				"source":     collectionName,
				"chunk_type": "markdown",
			},
		}

		if err := ltm.Store(ctx, doc); err != nil {
			return i, fmt.Errorf("store failed for %s: %w", id, err)
		}
	}

	return len(chunks), nil
}

// IsIndexing checks if a workspace is currently being indexed
func (m *Manager) IsIndexing(workspaceID string) bool {
	m.indexingMu.RLock()
	defer m.indexingMu.RUnlock()
	return m.indexing[workspaceID]
}

// StartIndexing explicitly starts background indexing for a workspace language
// This is used by the index_workspace tool to manually trigger indexing
func (m *Manager) StartIndexing(ctx context.Context, info *Info, language string) error {
	collectionName := info.CollectionNameForLanguage(language)
	indexKey := info.ID + "-" + language

	// Check if already indexing
	m.indexingMu.RLock()
	if m.indexing[indexKey] {
		m.indexingMu.RUnlock()
		return fmt.Errorf("indexing already in progress for workspace %s language %s", info.Root, language)
	}
	m.indexingMu.RUnlock()

	// Start background indexing
	indexCtx := context.Background()
	go m.indexWorkspaceLanguage(indexCtx, info, language, collectionName)

	return nil
}

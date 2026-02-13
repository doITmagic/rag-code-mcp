package workspace

import (
	"bufio"
	"context"
	"fmt"
	"hash/fnv"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/doITmagic/rag-code-mcp/internal/memory"
	"github.com/doITmagic/rag-code-mcp/internal/ragcode"
	"github.com/doITmagic/rag-code-mcp/internal/storage"
)

// IndexLanguage indexes a specific language in a workspace
// It runs synchronously. Use StartIndexing for background execution.
func (m *Manager) IndexLanguage(ctx context.Context, info *Info, language string, collectionName string, force bool) error {
	// Guard against concurrent indexing for same workspace/language
	indexKey := info.ID + "-" + language
	m.indexingMu.Lock()
	if m.indexing[indexKey] {
		m.indexingMu.Unlock()
		return fmt.Errorf("already indexing workspace '%s' language '%s'", info.Root, language)
	}
	m.indexing[indexKey] = true
	m.indexingMu.Unlock()

	defer func() {
		m.indexingMu.Lock()
		delete(m.indexing, indexKey)
		m.indexingMu.Unlock()
	}()

	log.Printf("🚀 Starting indexing for workspace: %s", info.Root)
	log.Printf("   Collection: %s", collectionName)
	log.Printf("   Language: %s", language)
	log.Printf("   Project type: %s", info.ProjectType)

	// Check if we need to migrate due to dimension mismatch
	collectionName, _, needsMigration, err := m.CheckAndPrepareMigration(ctx, info, language)
	if err != nil {
		return fmt.Errorf("failed to check collection migration: %w", err)
	}

	// Create collection-specific memory
	collectionConfig := storage.QdrantConfig{
		URL:        m.config.Storage.VectorDB.URL,
		APIKey:     m.config.Storage.VectorDB.APIKey,
		Collection: collectionName,
	}

	collectionClient, err := storage.NewQdrantClient(collectionConfig)
	if err != nil {
		return fmt.Errorf("failed to create collection client: %w", err)
	}
	defer collectionClient.Close()

	ltm := storage.NewQdrantLongTermMemory(collectionClient)

	// Select analyzer based on language (not ProjectType)
	analyzerManager := ragcode.NewAnalyzerManager()
	analyzer := analyzerManager.CodeAnalyzerForProjectType(language)
	if analyzer == nil {
		return fmt.Errorf("no code analyzer available for language '%s'", language)
	}

	// Scan workspace once to determine relevant paths per language
	scan, err := m.scanWorkspace(info)
	if err != nil {
		return fmt.Errorf("failed to scan workspace '%s': %w", info.Root, err)
	}

	languageDirs := scan.LanguageDirs[strings.ToLower(language)]
	if len(languageDirs) == 0 {
		return fmt.Errorf("no %s source files detected in workspace '%s'", language, info.Root)
	}

	// Load previous state
	stateFile := filepath.Join(info.Root, ".ragcode", "state.json")
	state, err := LoadState(stateFile)
	if err != nil {
		log.Printf("⚠️  Failed to load workspace state: %v", err)
		state = NewWorkspaceState()
	}

	// Double check if collection is actually empty in Qdrant. If it is, we MUST re-index regardless of state.
	// This handles cases where the collection was deleted but state.json remains.
	pointsCount, err := m.qdrant.GetCollectionPointCount(ctx, collectionName)
	if err == nil && pointsCount == 0 && !needsMigration {
		log.Printf("📭 Collection '%s' is empty, forcing full re-index (ignoring state.json)", collectionName)
		state = NewWorkspaceState()
	}

	// If migration is needed OR force is true, we MUST perform a full re-index regardless of state
	if needsMigration || force {
		log.Printf("🧹 Force indexing: re-indexing all files in collection '%s'", collectionName)
		state = NewWorkspaceState() // Start with fresh state
	}

	// Identify changes
	var filesToIndex []string
	var filesToDelete []string

	currentFiles := scan.LanguageFiles[strings.ToLower(language)]

	// Add markdown files to the list of files to check if this is the primary language
	// or if we handle them separately. For simplicity, let's handle docs as part of the language index
	// but with distinct metadata.
	// Actually, indexMarkdownFiles handles them separately in collection.
	// Let's integrate them into the state tracking.
	currentDocs := scan.DocFiles

	// Check for added or modified files (Code)
	for _, path := range currentFiles {
		info, err := os.Stat(path)
		if err != nil {
			continue
		}

		fileState, exists := state.GetFileState(path)
		if !exists || info.ModTime().After(fileState.ModTime) || info.Size() != fileState.Size {
			filesToIndex = append(filesToIndex, path)
			if exists {
				filesToDelete = append(filesToDelete, path)
			}
		}

		// Update state
		state.UpdateFile(path, info)
	}

	// Check for added or modified files (Docs)
	var docsToIndex []string
	var docsToDelete []string

	for _, path := range currentDocs {
		info, err := os.Stat(path)
		if err != nil {
			continue
		}

		fileState, exists := state.GetFileState(path)
		if !exists || info.ModTime().After(fileState.ModTime) || info.Size() != fileState.Size {
			docsToIndex = append(docsToIndex, path)
			if exists {
				docsToDelete = append(docsToDelete, path)
			}
		}

		// Update state
		state.UpdateFile(path, info)
	}

	// Check for deleted files (both code and docs)
	// We scan the state and check if files still exist in current scan
	// But scan only has current files.
	// Better: iterate state.Files and check if they exist on disk.
	state.mu.RLock()
	for path := range state.Files {
		if _, err := os.Stat(path); os.IsNotExist(err) {
			// It's deleted. Determine if it was code or doc based on extension
			ext := strings.ToLower(filepath.Ext(path))
			if ext == ".md" {
				docsToDelete = append(docsToDelete, path)
			} else {
				filesToDelete = append(filesToDelete, path)
			}
		}
	}
	state.mu.RUnlock()

	// Process deletions (Code)
	if len(filesToDelete) > 0 {
		log.Printf("🗑️  Deleting %d modified/deleted code files from index...", len(filesToDelete))
		for _, path := range filesToDelete {
			if err := ltm.DeleteByMetadata(ctx, "file", path); err != nil {
				log.Printf("⚠️  Failed to delete chunks for %s: %v", path, err)
			}
			state.RemoveFile(path)
		}
	}

	// Process deletions (Docs)
	if len(docsToDelete) > 0 {
		log.Printf("🗑️  Deleting %d modified/deleted doc files from index...", len(docsToDelete))
		for _, path := range docsToDelete {
			if err := ltm.DeleteByMetadata(ctx, "file", path); err != nil {
				log.Printf("⚠️  Failed to delete chunks for %s: %v", path, err)
			}
			state.RemoveFile(path)
		}
	}

	// Process indexing (Code)
	if len(filesToIndex) > 0 {
		log.Printf("📝 Indexing %d new/modified code files...", len(filesToIndex))

		indexer := ragcode.NewIndexer(analyzer, m.llm, ltm)

		startTime := time.Now()
		numChunks, err := indexer.IndexPaths(ctx, filesToIndex, collectionName)
		duration := time.Since(startTime)

		if err != nil {
			return fmt.Errorf("indexing failed: %w", err)
		}
		log.Printf("✅ Indexed %d chunks in %v", numChunks, duration)
	} else {
		log.Printf("✨ No code changes detected for language '%s'", language)
	}

	// Process indexing (Docs)
	if len(docsToIndex) > 0 {
		log.Printf("📚 Indexing %d new/modified doc files...", len(docsToIndex))
		// We use indexMarkdownFiles but only for the changed list
		numDocs := m.indexMarkdownFiles(ctx, docsToIndex, collectionName, ltm)
		if numDocs > 0 {
			log.Printf("   Docs chunks indexed: %d", numDocs)
		}
	} else {
		if len(currentDocs) > 0 {
			log.Printf("✨ No documentation changes detected")
		}
	}

	// Save state
	if err := state.Save(stateFile); err != nil {
		log.Printf("⚠️  Failed to save workspace state: %v", err)
	}

	m.recordFingerprint(info, language, scan)
	return nil
}

// checkAndReindexIfNeeded checks if any files have changed and triggers incremental re-indexing if needed
// This is called automatically when a tool accesses an existing workspace collection
func (m *Manager) checkAndReindexIfNeeded(ctx context.Context, info *Info, language string, collectionName string) {
	// 1. Check if we need to migrate/re-index due to dimension mismatch or empty collection
	_, _, needsMigration, err := m.CheckAndPrepareMigration(ctx, info, language)
	if err == nil && needsMigration {
		log.Printf("ℹ️ Migration or re-index needed for '%s', triggering IndexLanguage", collectionName)
		if err := m.IndexLanguage(ctx, info, language, collectionName, false); err != nil {
			log.Printf("⚠️  Migration/Re-index failed: %v", err)
		}
		return
	}

	// 2. Load workspace state
	stateFile := filepath.Join(info.Root, ".ragcode", "state.json")
	state, err := LoadState(stateFile)
	if err != nil {
		// If state doesn't exist, we can't check for changes
		// This is normal for first-time indexing
		return
	}

	// Quick scan to check if any files have changed
	scan, err := m.scanWorkspace(info)
	if err != nil {
		log.Printf("⚠️  Auto-reindex check failed for workspace '%s': %v", info.Root, err)
		return
	}

	currentFiles := scan.LanguageFiles[strings.ToLower(language)]
	if len(currentFiles) == 0 {
		return
	}

	// Check if any files have been modified, added, or deleted
	hasChanges := false

	// Check for modifications or additions
	for _, path := range currentFiles {
		fileInfo, err := os.Stat(path)
		if err != nil {
			continue
		}

		fileState, exists := state.GetFileState(path)
		if !exists || fileInfo.ModTime().After(fileState.ModTime) || fileInfo.Size() != fileState.Size {
			hasChanges = true
			break
		}
	}

	// Check for deletions (files in state but not in current scan)
	if !hasChanges {
		currentFileMap := make(map[string]bool)
		for _, p := range currentFiles {
			currentFileMap[p] = true
		}

		state.mu.RLock()
		for path := range state.Files {
			if _, err := os.Stat(path); os.IsNotExist(err) {
				hasChanges = true
				break
			}
		}
		state.mu.RUnlock()
	}

	// If changes detected, trigger incremental re-indexing
	if hasChanges {
		log.Printf("🔄 Auto-detected file changes in workspace '%s' (language: %s), triggering incremental re-indexing...", info.Root, language)
		if err := m.IndexLanguage(ctx, info, language, collectionName, false); err != nil {
			log.Printf("⚠️  Auto-reindex failed: %v", err)
		}
	}
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
		chunks          []string
		current         strings.Builder
		maxChars        = 2000 // Increased for better context
		emptyLineCount  = 0
		lastLineHeading = false
	)

	flushChunk := func() {
		text := strings.TrimSpace(current.String())
		if text != "" {
			chunks = append(chunks, text)
		}
		current.Reset()
		emptyLineCount = 0
		lastLineHeading = false
	}

	isHeading := func(line string) bool {
		trimmed := strings.TrimSpace(line)
		if !strings.HasPrefix(trimmed, "#") {
			return false
		}
		// Count leading hashes
		i := 0
		for i < len(trimmed) && trimmed[i] == '#' {
			i++
		}
		// Valid markdown heading: 1-6 hashes followed by a space or end of string
		return i >= 1 && i <= 6 && (i == len(trimmed) || trimmed[i] == ' ')
	}

	for scanner.Scan() {
		line := scanner.Text()
		trimmedLine := strings.TrimSpace(line)

		// Empty line handling
		if trimmedLine == "" {
			emptyLineCount++
			// Only flush if we have multiple empty lines AND we're not after a heading
			if emptyLineCount >= 2 && !lastLineHeading && current.Len() > 0 {
				flushChunk()
				continue
			}
			// Keep single empty line for formatting
			if current.Len() > 0 {
				current.WriteString("\n")
			}
			continue
		}

		// Reset empty line counter on content
		emptyLineCount = 0

		// New section: flush on heading unless it's the first content
		if isHeading(line) && current.Len() > 500 { // Keep headings together if chunk is small for better context
			flushChunk()
		}

		// Size check
		if current.Len()+len(line)+1 > maxChars {
			flushChunk()
		}

		if current.Len() > 0 {
			current.WriteString("\n")
		}
		current.WriteString(line)
		lastLineHeading = isHeading(line)
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
func (m *Manager) StartIndexing(ctx context.Context, info *Info, language string, force bool) error {
	collectionName := info.CollectionNameForLanguage(language)

	// Check if already indexing BEFORE starting goroutine for immediate feedback
	indexKey := info.ID + "-" + language
	m.indexingMu.RLock()
	if m.indexing[indexKey] {
		m.indexingMu.RUnlock()
		return fmt.Errorf("workspace '%s' language '%s' is already being indexed", info.Root, language)
	}
	m.indexingMu.RUnlock()

	// Start background indexing
	go func() {
		// IndexLanguage now handles its own concurrency guarding and lock management
		if err := m.IndexLanguage(context.Background(), info, language, collectionName, force); err != nil {
			log.Printf("❌ Background indexing failed: %v", err)
		}
	}()

	return nil
}

// EnsureWorkspaceIndexed triggers indexing for all detected languages in the workspace
func (m *Manager) EnsureWorkspaceIndexed(ctx context.Context, rootPath string) error {
	info, err := m.detector.DetectFromPath(rootPath)
	if err != nil {
		return err
	}
	// ID is generated by detector
	if m.config != nil && m.config.Workspace.CollectionPrefix != "" {
		info.CollectionPrefix = m.config.Workspace.CollectionPrefix
	}

	var errs []string

	// Check which languages have analyzers available
	analyzerManager := ragcode.NewAnalyzerManager()

	// Helper to check if we have an analyzer for a language
	hasAnalyzer := func(lang string) bool {
		return analyzerManager.CodeAnalyzerForProjectType(lang) != nil
	}

	// Helper to index language
	indexLang := func(lang string) {
		if !hasAnalyzer(lang) {
			log.Printf("⚠️  Skipping language '%s' - no analyzer available", lang)
			return
		}
		colName := info.CollectionNameForLanguage(lang)
		if err := m.IndexLanguage(ctx, info, lang, colName, false); err != nil {
			errs = append(errs, fmt.Sprintf("%s: %v", lang, err))
		}
	}

	if len(info.Languages) == 0 {
		lang := info.ProjectType
		if lang != "" && lang != "unknown" {
			indexLang(lang)
		}
	} else {
		for _, lang := range info.Languages {
			indexLang(lang)
		}
	}

	if len(errs) > 0 {
		return fmt.Errorf("indexing errors: %s", strings.Join(errs, "; "))
	}
	return nil
}

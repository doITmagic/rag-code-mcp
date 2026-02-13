package workspace

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
	"github.com/doITmagic/rag-code-mcp/internal/llm"
	"github.com/doITmagic/rag-code-mcp/internal/memory"
	"github.com/doITmagic/rag-code-mcp/internal/storage"
)

// Manager manages workspace detection, collection management, and indexing
type Manager struct {
	detector *Detector
	cache    *Cache
	registry *Registry
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

	// File watchers
	watchersMu sync.Mutex
	watchers   map[string]*FileWatcher

	// Mutexes per collection for migration/init operations to prevent race conditions
	collLocksMu sync.Mutex
	collLocks   map[string]*sync.Mutex
}

// NewManager creates a new workspace manager
func NewManager(qdrant *storage.QdrantClient, llm llm.Provider, cfg *config.Config) *Manager {
	// Create detector with config or defaults
	var detector *Detector
	if cfg != nil && cfg.Workspace.Enabled {
		detector = NewDetectorWithConfig(
			cfg.Workspace.DetectionMarkers,
			cfg.Workspace.ExcludePatterns,
			cfg.Workspace.AllowedWorkspacePaths,
			cfg.Workspace.DisableUpwardSearch,
		)
	} else {
		detector = NewDetector()
	}

	log.Printf("🔧 Workspace Manager initialized (logging verified)")

	return &Manager{
		detector:         detector,
		cache:            NewCache(5 * time.Minute),
		registry:         NewRegistry(""),
		qdrant:           qdrant,
		llm:              llm,
		config:           cfg,
		indexing:         make(map[string]bool),
		memories:         make(map[string]memory.LongTermMemory),
		scanFingerprints: make(map[string]string),
		watchers:         make(map[string]*FileWatcher),
		collLocks:        make(map[string]*sync.Mutex),
	}
}

// GetRegistry returns the workspace registry.
func (m *Manager) GetRegistry() *Registry {
	return m.registry
}

// ResolveWorkspace resolves a workspace from the registry.
// If "workspace" param is given, looks it up by name.
// If exactly one workspace is registered, returns it automatically.
// If multiple are registered and none specified, returns a formatted list as error.
// If none are registered, returns an error telling the AI to index first.
func (m *Manager) ResolveWorkspace(params map[string]interface{}) (*Info, error) {
	// Check if AI specified a workspace name
	if wsName, ok := params["workspace"].(string); ok && wsName != "" {
		entry := m.registry.FindByName(wsName)
		if entry == nil {
			return nil, fmt.Errorf("workspace %q not found in registry.\n\n%s", wsName, m.registry.FormatList())
		}
		return m.registryEntryToInfo(entry), nil
	}

	// Auto-resolve: single workspace → use it
	if single := m.registry.Single(); single != nil {
		return m.registryEntryToInfo(single), nil
	}

	// No workspaces or multiple without selection
	n := m.registry.Len()
	if n == 0 {
		return nil, fmt.Errorf("no workspaces indexed.\n\nPlease call 'index_workspace' first with:\n  {\"file_path\": \"/path/to/your/project\"}")
	}

	return nil, fmt.Errorf("%s", m.registry.FormatList())
}

// registryEntryToInfo converts a RegistryEntry to an Info struct.
func (m *Manager) registryEntryToInfo(entry *RegistryEntry) *Info {
	info := &Info{
		Root:       entry.Root,
		ID:         entry.ID,
		Languages:  entry.Languages,
		DetectedAt: entry.IndexedAt,
	}
	if m.config != nil && m.config.Workspace.CollectionPrefix != "" {
		info.CollectionPrefix = m.config.Workspace.CollectionPrefix
	}
	return info
}

// getCollectionMutex returns a mutex for a specific collection name, creating it if needed
func (m *Manager) getCollectionMutex(name string) *sync.Mutex {
	m.collLocksMu.Lock()
	defer m.collLocksMu.Unlock()

	if m.collLocks == nil {
		m.collLocks = make(map[string]*sync.Mutex)
	}

	if lock, ok := m.collLocks[name]; ok {
		return lock
	}

	lock := &sync.Mutex{}
	m.collLocks[name] = lock
	return lock
}

// DetectWorkspace detects workspace from tool parameters.
// Used exclusively by index_workspace to find the real project root via walk-up.
//
// Resolution order (3 strategies):
//  1. If params contain a path (workspace_root/file_path/filePath/path) → DetectFromPath (walk-up)
//  2. If no path provided → fallback to registry (1 ws → auto, multiple → list)
//  3. If registry is empty → clear error asking AI to provide the path once
func (m *Manager) DetectWorkspace(params map[string]interface{}) (*Info, error) {
	// Strategy 1: Extract path from params: workspace_root > file_path > filePath > path
	var rawPath string
	for _, key := range []string{"workspace_root", "file_path", "filePath", "path"} {
		if v, ok := params[key].(string); ok && v != "" {
			rawPath = v
			break
		}
	}

	// If we have a path, detect via walk-up (original behavior)
	if rawPath != "" {
		// Expand tilde
		if strings.HasPrefix(rawPath, "~/") {
			if home, err := os.UserHomeDir(); err == nil {
				rawPath = filepath.Join(home, rawPath[2:])
			}
		}

		absPath, err := filepath.Abs(rawPath)
		if err != nil {
			return nil, fmt.Errorf("invalid path: %w", err)
		}

		info, err := m.detector.DetectFromPath(absPath)
		if err != nil {
			return nil, err
		}

		if m.config != nil && m.config.Workspace.CollectionPrefix != "" {
			info.CollectionPrefix = m.config.Workspace.CollectionPrefix
		}

		m.cache.Set(absPath, info)
		return info, nil
	}

	// Strategy 2: No path provided — fallback to registry
	if single := m.registry.Single(); single != nil {
		log.Printf("[workspace] No path provided, using single registered workspace: %s", single.Root)
		return m.registryEntryToInfo(single), nil
	}

	n := m.registry.Len()
	if n > 1 {
		return nil, fmt.Errorf("no file path provided and multiple workspaces registered.\n\n%s\n\nPlease re-call index_workspace with {\"workspace\": \"<name>\"} to select one.", m.registry.FormatList())
	}

	// Strategy 3: Registry is empty — ask AI to provide path once
	return nil, fmt.Errorf("no workspaces registered yet.\n\n" +
		"Please call index_workspace with {\"file_path\": \"/path/to/your/project\"} or {\"workspace_root\": \"/path/to/your/project\"}.\n" +
		"This only needs to be done once — the workspace will be remembered permanently.")
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
	homeDir, err := os.UserHomeDir()
	if err != nil {
		log.Printf("workspace: could not determine user home directory: %v", err)
	}
	if info.Root == "/" || info.Root == homeDir || info.Root == "/tmp" {
		return nil, fmt.Errorf(
			"invalid workspace root '%s'. "+
				"Please provide a file path inside a valid project directory with workspace markers "+
				"(e.g., .git, go.mod, composer.json, package.json)",
			info.Root,
		)
	}

	// Ensure filesystem watcher is running so future changes trigger reindex automatically
	m.StartWatcher(info.Root)

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
			go func() {
				if err := m.IndexLanguage(indexCtx, info, language, collectionName, false); err != nil {
					log.Printf("❌ Background indexing failed: %v", err)
				}
			}()
		} else {
			log.Printf("⏸️  Auto-indexing disabled for workspace '%s' language '%s'. Run manual indexing.", info.Root, language)
		}
	} else {
		// Collection exists - check if files have changed and trigger incremental re-indexing
		if m.config != nil && m.config.Workspace.AutoIndex {
			go m.checkAndReindexIfNeeded(context.Background(), info, language, collectionName)
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

// StartWatcher starts the file watcher for a workspace if not already running
func (m *Manager) StartWatcher(root string) {
	// Validate root directory before starting watcher to prevent broad filesystem access
	if isInvalidRoot(root) {
		log.Printf("[ERROR] Cannot start watcher on invalid root directory: %s", root)
		return
	}

	m.watchersMu.Lock()
	defer m.watchersMu.Unlock()

	if _, exists := m.watchers[root]; exists {
		return
	}

	watcher, err := NewFileWatcher(root, m)
	if err != nil {
		log.Printf("⚠️ Failed to create file watcher for %s: %v", root, err)
		return
	}

	m.watchers[root] = watcher
	watcher.Start()
}

package workspace

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"
)

// RegistryEntry represents a known workspace in the global registry
type RegistryEntry struct {
	ID        string    `json:"id"`
	Root      string    `json:"root"`
	LastUsed  time.Time `json:"last_used"`
	Languages []string  `json:"languages,omitempty"`
}

// Registry manages the persistence of known workspaces across sessions
type Registry struct {
	Entries map[string]*RegistryEntry `json:"entries"`
	path    string
	mu      sync.RWMutex

	// Throttling
	saveTimer *time.Timer
}

// NewRegistry creates a new registry loaded from the given path.
// If path is empty, it defaults to ~/.config/ragcode/workspaces.json
func NewRegistry(path string) (*Registry, error) {
	if path == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return nil, fmt.Errorf("failed to get home dir: %w", err)
		}
		path = filepath.Join(home, ".config", "ragcode", "workspaces.json")
	}

	r := &Registry{
		Entries: make(map[string]*RegistryEntry),
		path:    path,
	}

	if err := r.Load(); err != nil {
		// If the file exists but we can't load it (e.g. corruption), log and start fresh
		if !os.IsNotExist(err) {
			log.Printf("⚠️  Workspace registry at %s is corrupted or unreadable: %v. Starting with a fresh one.", path, err)
			// Reset entries just in case
			r.Entries = make(map[string]*RegistryEntry)
		}
	}

	return r, nil
}

// Load loads the registry from disk
func (r *Registry) Load() error {
	r.mu.Lock()
	defer r.mu.Unlock()

	f, err := os.Open(r.path)
	if err != nil {
		return err
	}
	defer f.Close()

	if err := json.NewDecoder(f).Decode(&r.Entries); err != nil {
		// If JSON is corrupted, log error and return it so NewRegistry can handle it
		return fmt.Errorf("failed to decode registry JSON: %w", err)
	}
	return nil
}

// Save saves the registry to disk. It assumes the caller holds the lock.
func (r *Registry) saveLocked() error {
	dir := filepath.Dir(r.path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}

	f, err := os.Create(r.path)
	if err != nil {
		return err
	}
	defer f.Close()

	encoder := json.NewEncoder(f)
	encoder.SetIndent("", "  ")
	return encoder.Encode(r.Entries)
}

// Save saves the registry to disk
func (r *Registry) Save() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.saveLocked()
}

// RegisterOrUpdate adds or updates a workspace in the registry.
// It uses a debounce mechanism to avoid excessive disk writes in high-frequency scenarios.
func (r *Registry) RegisterOrUpdate(info *Info) error {
	if info == nil {
		return nil
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	entry, exists := r.Entries[info.ID]
	if !exists {
		entry = &RegistryEntry{
			ID:   info.ID,
			Root: info.Root,
		}
		r.Entries[info.ID] = entry
	}

	// Update root if it changed (e.g. symlink resolution or moved directory)
	entry.Root = info.Root
	// Always update timestamp and languages
	entry.LastUsed = time.Now()
	entry.Languages = info.Languages

	// Throttled save: wait for 500ms of inactivity before writing to disk
	if r.saveTimer != nil {
		r.saveTimer.Stop()
	}

	r.saveTimer = time.AfterFunc(500*time.Millisecond, func() {
		r.mu.Lock()
		defer r.mu.Unlock()
		if err := r.saveLocked(); err != nil {
			log.Printf("⚠️  Failed to save workspace registry: %v", err)
		}
	})

	return nil
}

// GetLastUsed returns the most recently used workspace
func (r *Registry) GetLastUsed() *RegistryEntry {
	r.mu.RLock()
	defer r.mu.RUnlock()

	var entries []*RegistryEntry
	for _, e := range r.Entries {
		entries = append(entries, e)
	}

	if len(entries) == 0 {
		return nil
	}

	// Sort by LastUsed descending
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].LastUsed.After(entries[j].LastUsed)
	})

	return entries[0]
}

package workspace

import (
	"encoding/json"
	"fmt"
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
		// Log error but continue with empty registry
		// Only return error if it's not a "file not found" error
		if !os.IsNotExist(err) {
			return nil, err
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

	return json.NewDecoder(f).Decode(&r.Entries)
}

// Save saves the registry to disk
func (r *Registry) Save() error {
	r.mu.RLock()
	defer r.mu.RUnlock()

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

// RegisterOrUpdate adds or updates a workspace in the registry
func (r *Registry) RegisterOrUpdate(info *Info) error {
	if info == nil {
		return nil
	}

	r.mu.Lock()
	entry, exists := r.Entries[info.ID]
	if !exists {
		entry = &RegistryEntry{
			ID:   info.ID,
			Root: info.Root,
		}
		r.Entries[info.ID] = entry
	}

	// Always update timestamp and languages
	entry.LastUsed = time.Now()
	entry.Languages = info.Languages
	r.mu.Unlock()

	return r.Save()
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

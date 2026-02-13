package workspace

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// RegistryEntry represents a single indexed workspace in the registry.
type RegistryEntry struct {
	Name       string   `json:"name"`
	Root       string   `json:"root"`
	ID         string   `json:"id"`
	Languages  []string `json:"languages,omitempty"`
	IndexedAt  time.Time `json:"indexed_at"`
}

// Registry is a persistent store of all indexed workspaces.
// Stored as JSON at ~/.local/share/ragcode/workspaces.json.
type Registry struct {
	mu         sync.RWMutex
	Workspaces []RegistryEntry `json:"workspaces"`
	filePath   string
}

// DefaultRegistryPath returns the default path for the registry file.
func DefaultRegistryPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return "workspaces.json"
	}
	return filepath.Join(home, ".local", "share", "ragcode", "workspaces.json")
}

// NewRegistry creates a new registry and loads existing data from disk.
func NewRegistry(path string) *Registry {
	if path == "" {
		path = DefaultRegistryPath()
	}
	r := &Registry{filePath: path}
	if err := r.load(); err != nil {
		log.Printf("[registry] no existing registry at %s: %v", path, err)
	}
	return r
}

// load reads the registry from disk.
func (r *Registry) load() error {
	data, err := os.ReadFile(r.filePath)
	if err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return json.Unmarshal(data, &r.Workspaces)
}

// save writes the registry to disk.
func (r *Registry) save() error {
	if err := os.MkdirAll(filepath.Dir(r.filePath), 0755); err != nil {
		return fmt.Errorf("create registry dir: %w", err)
	}
	data, err := json.MarshalIndent(r.Workspaces, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal registry: %w", err)
	}
	return os.WriteFile(r.filePath, data, 0644)
}

// Register adds or updates a workspace in the registry and persists to disk.
func (r *Registry) Register(info *Info) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	name := filepath.Base(info.Root)

	// Update existing entry if same root
	for i, entry := range r.Workspaces {
		if entry.Root == info.Root {
			r.Workspaces[i].Languages = info.Languages
			r.Workspaces[i].IndexedAt = time.Now()
			r.Workspaces[i].ID = info.ID
			r.Workspaces[i].Name = name
			return r.save()
		}
	}

	// Add new entry
	r.Workspaces = append(r.Workspaces, RegistryEntry{
		Name:      name,
		Root:      info.Root,
		ID:        info.ID,
		Languages: info.Languages,
		IndexedAt: time.Now(),
	})
	return r.save()
}

// Remove deletes a workspace from the registry by name or root.
func (r *Registry) Remove(nameOrRoot string) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	for i, entry := range r.Workspaces {
		if entry.Name == nameOrRoot || entry.Root == nameOrRoot {
			r.Workspaces = append(r.Workspaces[:i], r.Workspaces[i+1:]...)
			return r.save()
		}
	}
	return fmt.Errorf("workspace %q not found in registry", nameOrRoot)
}

// FindByName returns the entry matching the given name (case-insensitive).
func (r *Registry) FindByName(name string) *RegistryEntry {
	r.mu.RLock()
	defer r.mu.RUnlock()

	lower := strings.ToLower(name)
	for i, entry := range r.Workspaces {
		if strings.ToLower(entry.Name) == lower {
			return &r.Workspaces[i]
		}
	}
	return nil
}

// FindByRoot returns the entry matching the given root path.
func (r *Registry) FindByRoot(root string) *RegistryEntry {
	r.mu.RLock()
	defer r.mu.RUnlock()

	for i, entry := range r.Workspaces {
		if entry.Root == root {
			return &r.Workspaces[i]
		}
	}
	return nil
}

// List returns all registered workspaces sorted by name.
func (r *Registry) List() []RegistryEntry {
	r.mu.RLock()
	defer r.mu.RUnlock()

	out := make([]RegistryEntry, len(r.Workspaces))
	copy(out, r.Workspaces)
	sort.Slice(out, func(i, j int) bool {
		return out[i].Name < out[j].Name
	})
	return out
}

// Len returns the number of registered workspaces.
func (r *Registry) Len() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.Workspaces)
}

// Single returns the only workspace if exactly one is registered, nil otherwise.
func (r *Registry) Single() *RegistryEntry {
	r.mu.RLock()
	defer r.mu.RUnlock()

	if len(r.Workspaces) == 1 {
		return &r.Workspaces[0]
	}
	return nil
}

// FormatList returns a human-readable list of workspaces for the AI to choose from.
func (r *Registry) FormatList() string {
	entries := r.List()
	if len(entries) == 0 {
		return "No workspaces indexed. Please call 'index_workspace' first."
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Multiple workspaces indexed (%d). Please specify 'workspace' parameter with one of:\n\n", len(entries)))
	for i, e := range entries {
		sb.WriteString(fmt.Sprintf("  %d. **%s** → %s (indexed %s)\n",
			i+1, e.Name, e.Root, e.IndexedAt.Format("2006-01-02 15:04")))
	}
	return sb.String()
}

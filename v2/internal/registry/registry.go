package registry

import (
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const registrySchemaVersion = "v1"

// Entry represents a persisted workspace selection.
type Entry struct {
	SchemaVersion string    `json:"schema_version"`
	ID            string    `json:"id"`
	Root          string    `json:"root"`
	Name          string    `json:"name,omitempty"`
	Client        string    `json:"client,omitempty"`
	ConfirmedAt   time.Time `json:"confirmed_at"`
	LastUsedAt    time.Time `json:"last_used_at"`
}

// Registry persists confirmed workspaces for deterministic reuse.
type Registry struct {
	path      string
	entries   map[string]*Entry
	indexRoot map[string]string
	indexName map[string][]string
	clock     func() time.Time
	mu        sync.Mutex
}

// New creates a registry backed by the given file path.
func New(path string) (*Registry, error) {
	r := &Registry{
		path:      path,
		entries:   make(map[string]*Entry),
		indexRoot: make(map[string]string),
		indexName: make(map[string][]string),
		clock:     time.Now,
	}
	if err := r.load(); err != nil {
		return nil, err
	}
	return r, nil
}

// Upsert confirms a workspace selection and updates timestamps.
func (r *Registry) Upsert(root, name, client string) (*Entry, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	root = filepath.Clean(root)
	id := hashRoot(root)

	now := r.clock()
	if entry, ok := r.entries[id]; ok {
		entry.LastUsedAt = now
		if name != "" {
			entry.Name = name
		}
		if client != "" {
			entry.Client = client
		}
		return entry, r.save()
	}

	entry := &Entry{
		SchemaVersion: registrySchemaVersion,
		ID:            id,
		Root:          root,
		Name:          name,
		Client:        client,
		ConfirmedAt:   now,
		LastUsedAt:    now,
	}
	r.entries[id] = entry
	r.indexRoot[strings.ToLower(root)] = id
	if name != "" {
		lower := strings.ToLower(name)
		r.indexName[lower] = append(r.indexName[lower], id)
	}
	return entry, r.save()
}

// LookupByID returns an entry by ID.
func (r *Registry) LookupByID(id string) (*Entry, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	entry, ok := r.entries[id]
	return entry, ok
}

// LookupByRoot returns an entry by normalized root path.
func (r *Registry) LookupByRoot(root string) (*Entry, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	id, ok := r.indexRoot[strings.ToLower(filepath.Clean(root))]
	if !ok {
		return nil, false
	}
	return r.entries[id], true
}

// LookupByName returns entries matching the provided name.
func (r *Registry) LookupByName(name string) []*Entry {
	r.mu.Lock()
	defer r.mu.Unlock()
	ids := r.indexName[strings.ToLower(name)]
	results := make([]*Entry, 0, len(ids))
	for _, id := range ids {
		if entry, ok := r.entries[id]; ok {
			results = append(results, entry)
		}
	}
	return results
}

// List returns all registry entries.
func (r *Registry) List() []*Entry {
	r.mu.Lock()
	defer r.mu.Unlock()
	results := make([]*Entry, 0, len(r.entries))
	for _, entry := range r.entries {
		results = append(results, entry)
	}
	return results
}

// Cleanup removes entries that have not been used since the cutoff time.
func (r *Registry) Cleanup(cutoff time.Time) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	for id, entry := range r.entries {
		if entry.LastUsedAt.Before(cutoff) {
			delete(r.entries, id)
			delete(r.indexRoot, strings.ToLower(entry.Root))
			if entry.Name != "" {
				lower := strings.ToLower(entry.Name)
				ids := r.indexName[lower]
				filtered := make([]string, 0, len(ids))
				for _, existing := range ids {
					if existing != id {
						filtered = append(filtered, existing)
					}
				}
				if len(filtered) == 0 {
					delete(r.indexName, lower)
				} else {
					r.indexName[lower] = filtered
				}
			}
		}
	}
	return r.save()
}

// load loads existing registry entries from disk.
func (r *Registry) load() error {
	data, err := os.ReadFile(r.path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	var entries []*Entry
	if err := json.Unmarshal(data, &entries); err != nil {
		return err
	}
	for _, entry := range entries {
		if entry.ID == "" {
			entry.ID = hashRoot(entry.Root)
		}
		r.entries[entry.ID] = entry
		r.indexRoot[strings.ToLower(entry.Root)] = entry.ID
		if entry.Name != "" {
			lower := strings.ToLower(entry.Name)
			r.indexName[lower] = append(r.indexName[lower], entry.ID)
		}
	}
	return nil
}

func (r *Registry) save() error {
	entries := make([]*Entry, 0, len(r.entries))
	for _, entry := range r.entries {
		entries = append(entries, entry)
	}
	if err := os.MkdirAll(filepath.Dir(r.path), 0o755); err != nil {
		return err
	}
	payload, err := json.MarshalIndent(entries, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(r.path, payload, 0o644)
}

func hashRoot(root string) string {
	sum := sha1.Sum([]byte(strings.ToLower(filepath.Clean(root))))
	return hex.EncodeToString(sum[:])
}

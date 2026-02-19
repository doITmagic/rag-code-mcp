package registry

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/doITmagic/rag-code-mcp/pkg/workspace/contract"
)

const (
	registrySchemaVersion = "v1"
	registryStoreVersion  = "v2"
)

// AuditSink receives auditable registry lifecycle events.
type AuditSink interface {
	Record(ctx context.Context, event string, fields map[string]any)
}

type noopAuditSink struct{}

func (noopAuditSink) Record(ctx context.Context, event string, fields map[string]any) {}

// Metrics captures registry counters relevant to feedback/promotion workflow.
type Metrics struct {
	FeedbackIngested   int `json:"feedback_ingested"`
	CandidatesPromoted int `json:"candidates_promoted"`
}

type registryStore struct {
	Version    string            `json:"version"`
	Entries    []*Entry          `json:"entries"`
	Candidates []*CandidateEntry `json:"candidates,omitempty"`
}

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

// CandidateEntry tracks un-trusted path suggestions.
type CandidateEntry struct {
	Root       string    `json:"root"`
	Count      int       `json:"count"`
	LastSeenAt time.Time `json:"last_seen_at"`
	Reason     string    `json:"reason,omitempty"`
}

// Registry persists confirmed workspaces and tracks feedback candidates.
type Registry struct {
	path       string
	entries    map[string]*Entry
	candidates map[string]*CandidateEntry
	indexRoot  map[string]string
	indexName  map[string][]string
	clock      func() time.Time
	audit      AuditSink
	metrics    Metrics
	mu         sync.Mutex
}

// New creates a registry backed by the given file path.
func New(path string) (*Registry, error) {
	r := &Registry{
		path:       path,
		entries:    make(map[string]*Entry),
		candidates: make(map[string]*CandidateEntry),
		indexRoot:  make(map[string]string),
		indexName:  make(map[string][]string),
		clock:      time.Now,
		audit:      noopAuditSink{},
	}
	if err := r.load(); err != nil {
		return nil, err
	}
	return r, nil
}

// SetAuditSink configures an optional sink for auditable lifecycle events.
func (r *Registry) SetAuditSink(sink AuditSink) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if sink == nil {
		r.audit = noopAuditSink{}
		return
	}
	r.audit = sink
}

// MetricsSnapshot returns a copy of registry counters.
func (r *Registry) MetricsSnapshot() Metrics {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.metrics
}

// ResolveAlias implements resolver.Registry.
func (r *Registry) ResolveAlias(ctx context.Context, alias string) (*contract.WorkspaceCandidate, *contract.ResolveWorkspaceError) {
	entries := r.LookupByName(alias)
	if len(entries) == 0 {
		return nil, nil
	}
	entry := entries[0]
	return &contract.WorkspaceCandidate{
		Root:   entry.Root,
		Name:   entry.Name,
		Reason: contract.ReasonWorkspaceAlias,
	}, nil
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

// PromoteCandidate promotes a suggested path candidate to a confirmed entry only when execution succeeded.
func (r *Registry) PromoteCandidate(ctx context.Context, root, client string, executionSucceeded bool) error {
	if !executionSucceeded || strings.TrimSpace(root) == "" {
		return nil
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	cleanRoot := filepath.Clean(root)
	normalized := strings.ToLower(cleanRoot)
	candidate, ok := r.candidates[normalized]
	if !ok {
		return nil
	}

	id := hashRoot(cleanRoot)
	now := r.clock()
	name := filepath.Base(cleanRoot)
	if existing, exists := r.entries[id]; exists {
		existing.LastUsedAt = now
		if client != "" {
			existing.Client = client
		}
		if existing.Name == "" {
			existing.Name = name
		}
	} else {
		entry := &Entry{
			SchemaVersion: registrySchemaVersion,
			ID:            id,
			Root:          cleanRoot,
			Name:          name,
			Client:        client,
			ConfirmedAt:   now,
			LastUsedAt:    now,
		}
		r.entries[id] = entry
		r.indexRoot[normalized] = id
		if entry.Name != "" {
			lower := strings.ToLower(entry.Name)
			r.indexName[lower] = append(r.indexName[lower], id)
		}
	}

	delete(r.candidates, normalized)
	r.metrics.CandidatesPromoted++
	r.audit.Record(ctx, "registry.candidate_promoted", map[string]any{
		"root":                cleanRoot,
		"client":              client,
		"execution_succeeded": executionSucceeded,
		"candidate_count":     candidate.Count,
	})

	return r.save()
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

// load loads existing registry entries and candidates from disk.
func (r *Registry) load() error {
	data, err := os.ReadFile(r.path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}

	// Try loading V2 store first
	var store registryStore
	if err := json.Unmarshal(data, &store); err == nil && store.Version != "" {
		for _, entry := range store.Entries {
			r.addEntry(entry)
		}
		for _, cand := range store.Candidates {
			r.candidates[strings.ToLower(cand.Root)] = cand
		}
		return nil
	}

	// Fallback to V1 (array of entries)
	var entries []*Entry
	if err := json.Unmarshal(data, &entries); err != nil {
		return err
	}
	for _, entry := range entries {
		r.addEntry(entry)
	}
	return nil
}

func (r *Registry) addEntry(entry *Entry) {
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

func (r *Registry) save() error {
	store := registryStore{
		Version: registryStoreVersion,
		Entries: make([]*Entry, 0, len(r.entries)),
	}
	for _, entry := range r.entries {
		store.Entries = append(store.Entries, entry)
	}
	for _, cand := range r.candidates {
		store.Candidates = append(store.Candidates, cand)
	}

	if err := os.MkdirAll(filepath.Dir(r.path), 0o755); err != nil {
		return err
	}
	payload, err := json.MarshalIndent(store, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(r.path, payload, 0o644)
}

// RecordFeedback captures IDE suggestions for future promotion.
func (r *Registry) RecordFeedback(ctx context.Context, feedback *contract.PathFeedback) error {
	if feedback == nil || feedback.SuggestedPath == "" {
		return nil
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	root := strings.ToLower(filepath.Clean(feedback.SuggestedPath))
	cand, ok := r.candidates[root]
	if !ok {
		cand = &CandidateEntry{
			Root: feedback.SuggestedPath,
		}
		r.candidates[root] = cand
	}

	cand.Count++
	cand.LastSeenAt = r.clock()
	if feedback.Reason != "" {
		cand.Reason = feedback.Reason
	}
	r.metrics.FeedbackIngested++
	r.audit.Record(ctx, "registry.feedback_ingested", map[string]any{
		"suggested_path": feedback.SuggestedPath,
		"reason":         feedback.Reason,
		"count":          cand.Count,
	})

	return r.save()
}

func hashRoot(root string) string {
	sum := sha1.Sum([]byte(strings.ToLower(filepath.Clean(root))))
	return hex.EncodeToString(sum[:])
}

// GetActiveWorkspace returns the root path of the most recently used workspace.
func (r *Registry) GetActiveWorkspace() (string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	var latest *Entry
	for _, entry := range r.entries {
		if latest == nil || entry.LastUsedAt.After(latest.LastUsedAt) {
			latest = entry
		}
	}

	if latest == nil {
		return "", nil
	}
	return latest.Root, nil
}

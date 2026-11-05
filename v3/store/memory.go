package store

import (
	"context"
	"fmt"
	"math"
	"path/filepath"
	"slices"
	"sort"
	"sync"

	"github.com/doITmagic/rag-code-mcp/v3/parser"
)

// Memory is an in-memory Store for tests and engine unit tests. It is safe
// for concurrent use. Search is a brute-force cosine scan.
type Memory struct {
	mu   sync.RWMutex
	cols map[string]*memCol
}

type memCol struct {
	dim    int
	points map[string]Item // by Symbol.ID
}

// NewMemory returns an empty in-memory store.
func NewMemory() *Memory {
	return &Memory{cols: map[string]*memCol{}}
}

func (m *Memory) EnsureCollection(ctx context.Context, name string, dim int) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	c, ok := m.cols[name]
	if ok && c.dim == dim {
		return false, nil
	}
	m.cols[name] = &memCol{dim: dim, points: map[string]Item{}}
	return ok, nil
}

func (m *Memory) CollectionExists(ctx context.Context, name string) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	_, ok := m.cols[name]
	return ok, nil
}

func (m *Memory) VectorSize(ctx context.Context, name string) (int, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	c, ok := m.cols[name]
	if !ok {
		return 0, ErrNotFound
	}
	return c.dim, nil
}

func (m *Memory) DeleteCollection(ctx context.Context, name string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.cols[name]; !ok {
		return ErrNotFound
	}
	delete(m.cols, name)
	return nil
}

func (m *Memory) Upsert(ctx context.Context, collection string, items []Item) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	c, ok := m.cols[collection]
	if !ok {
		return ErrNotFound
	}
	for _, it := range items {
		if len(it.Vector) != c.dim {
			return fmt.Errorf("store: vector size %d, collection %q has %d", len(it.Vector), collection, c.dim)
		}
		c.points[it.Symbol.ID] = it
	}
	return nil
}

func (m *Memory) DeleteByFile(ctx context.Context, collection, filePath string) error {
	return m.deleteWhere(ctx, collection, filePath, func(s *parser.Symbol) bool { return s.FilePath == filePath })
}

// deleteWhere refuses an empty path: it is never a wildcard (Qdrant parity).
func (m *Memory) deleteWhere(ctx context.Context, collection, path string, pred func(*parser.Symbol) bool) error {
	if path == "" {
		return errEmptyPath
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	c, ok := m.cols[collection]
	if !ok {
		return ErrNotFound
	}
	for id, it := range c.points {
		if pred(&it.Symbol) {
			delete(c.points, id)
		}
	}
	return nil
}

func (m *Memory) Search(ctx context.Context, collection string, vector []float32, f Filter, limit int) ([]Hit, error) {
	if limit <= 0 {
		return nil, nil
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	c, ok := m.cols[collection]
	if !ok {
		return nil, ErrNotFound
	}
	if len(vector) != c.dim {
		return nil, fmt.Errorf("store: query vector size %d, collection %q has %d", len(vector), collection, c.dim)
	}
	var hits []Hit
	for _, it := range c.points {
		if f.matches(&it.Symbol) {
			hits = append(hits, Hit{Score: cosine(vector, it.Vector), Symbol: it.Symbol})
		}
	}
	sort.Slice(hits, func(i, j int) bool {
		if hits[i].Score != hits[j].Score {
			return hits[i].Score > hits[j].Score
		}
		return hits[i].Symbol.ID < hits[j].Symbol.ID
	})
	if len(hits) > limit {
		hits = hits[:limit]
	}
	return hits, nil
}

func (m *Memory) Scroll(ctx context.Context, collection string, f Filter, limit int) ([]Hit, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	c, ok := m.cols[collection]
	if !ok {
		return nil, ErrNotFound
	}
	var hits []Hit
	for _, it := range c.points {
		if f.matches(&it.Symbol) {
			hits = append(hits, Hit{Symbol: it.Symbol})
		}
	}
	sort.Slice(hits, func(i, j int) bool { return hits[i].Symbol.ID < hits[j].Symbol.ID })
	if limit < 0 || limit > MaxScroll {
		limit = MaxScroll
	}
	if len(hits) > limit {
		hits = hits[:limit]
	}
	return hits, nil
}

func (m *Memory) Close() error { return nil }

// matches reports whether s satisfies every set field of f.
func (f Filter) matches(s *parser.Symbol) bool {
	switch {
	case f.ID != "" && s.ID != f.ID,
		f.Language != "" && s.Language != f.Language,
		f.FilePath != "" && s.FilePath != f.FilePath,
		f.PathPrefix != "" && !underDir(s.FilePath, f.PathPrefix),
		f.Name != "" && s.Name != f.Name,
		f.QualifiedName != "" && s.QualifiedName != f.QualifiedName,
		f.QualifiedSuffix != "" && !parser.MatchQualified(s.QualifiedName, f.QualifiedSuffix),
		f.Module != "" && s.Module != f.Module,
		len(f.Kinds) > 0 && !slices.Contains(f.Kinds, s.Kind),
		f.TargetID != "" && !slices.ContainsFunc(s.Relations, func(r parser.Relation) bool { return r.TargetID == f.TargetID }),
		f.TargetQualifiedName != "" && !slices.ContainsFunc(s.Relations, func(r parser.Relation) bool { return r.TargetQualifiedName == f.TargetQualifiedName }),
		f.TargetName != "" && !slices.ContainsFunc(s.Relations, func(r parser.Relation) bool { return r.TargetName == f.TargetName }):
		return false
	}
	return true
}

// underDir reports whether prefix is the directory of path or one of its
// ancestors: the dir_parts keyword match Qdrant does, never a raw string
// prefix ("/w/mod" does not cover "/w/models/x.go").
func underDir(path, prefix string) bool {
	return slices.Contains(dirParts(path), filepath.Clean(prefix))
}

// cosine returns the cosine similarity of two equal-length vectors, 0 when
// either is the zero vector.
func cosine(a, b []float32) float32 {
	var dot, na, nb float64
	for i := range a {
		dot += float64(a[i]) * float64(b[i])
		na += float64(a[i]) * float64(a[i])
		nb += float64(b[i]) * float64(b[i])
	}
	if na == 0 || nb == 0 {
		return 0
	}
	return float32(dot / (math.Sqrt(na) * math.Sqrt(nb)))
}

package store

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"

	"github.com/doITmagic/rag-code-mcp/v3/parser"
	"github.com/doITmagic/rag-code-mcp/v3/vecdb"
)

// SQLite is the Store kept on disk by vecdb: one SQLite file per collection
// under dir (~/.ragcode/index/<collection>/index.db).
type SQLite struct {
	db *vecdb.DB
}

// NewSQLite returns the store rooted at dir. Nothing touches the disk until
// a collection is created.
func NewSQLite(dir string) *SQLite { return &SQLite{db: vecdb.New(dir)} }

// CollectionName is the collection of a resolved root:
// "ragcode-" + 16 hex characters of sha1(root).
func CollectionName(root string) string {
	sum := sha1.Sum([]byte(filepath.Clean(root)))
	return "ragcode-" + hex.EncodeToString(sum[:])[:16]
}

func mapErr(err error) error {
	if errors.Is(err, vecdb.ErrNotFound) {
		return ErrNotFound
	}
	return err
}

func (s *SQLite) EnsureCollection(ctx context.Context, name string, dim int) (bool, error) {
	rec, err := s.db.Ensure(ctx, name, dim)
	return rec, mapErr(err)
}

func (s *SQLite) CollectionExists(ctx context.Context, name string) (bool, error) {
	ok, err := s.db.Exists(ctx, name)
	return ok, mapErr(err)
}

func (s *SQLite) VectorSize(ctx context.Context, name string) (int, error) {
	n, err := s.db.Dim(ctx, name)
	return n, mapErr(err)
}

func (s *SQLite) DeleteCollection(ctx context.Context, name string) error {
	return mapErr(s.db.Drop(ctx, name))
}

func (s *SQLite) Upsert(ctx context.Context, collection string, items []Item) error {
	pts := make([]vecdb.Point, 0, len(items))
	for _, it := range items {
		payload, err := json.Marshal(it.Symbol)
		if err != nil {
			return fmt.Errorf("store: symbol %s: %w", it.Symbol.ID, err)
		}
		pts = append(pts, vecdb.Point{ID: it.Symbol.ID, Vector: it.Vector, Payload: payload, Fields: fields(&it.Symbol)})
	}
	return mapErr(s.db.Upsert(ctx, collection, pts))
}

// DeleteByFile refuses an empty path: it is never a wildcard.
func (s *SQLite) DeleteByFile(ctx context.Context, collection, filePath string) error {
	if filePath == "" {
		return errEmptyPath
	}
	return mapErr(s.db.Delete(ctx, collection, vecdb.Cond{{Field: "file_path", Values: []string{filePath}}}))
}

func (s *SQLite) Search(ctx context.Context, collection string, vector []float32, f Filter, limit int) ([]Hit, error) {
	hs, err := s.db.Search(ctx, collection, vector, cond(f), limit)
	if err != nil {
		return nil, mapErr(err)
	}
	return toHits(hs)
}

func (s *SQLite) Scroll(ctx context.Context, collection string, f Filter, limit int) ([]Hit, error) {
	if limit < 0 || limit > MaxScroll {
		limit = MaxScroll
	}
	if limit == 0 {
		// Still report a missing collection, as every other read does.
		if _, err := s.db.Dim(ctx, collection); err != nil {
			return nil, mapErr(err)
		}
		return nil, nil
	}
	hs, err := s.db.Scan(ctx, collection, cond(f), limit)
	if err != nil {
		return nil, mapErr(err)
	}
	return toHits(hs)
}

func (s *SQLite) Close() error { return s.db.Close() }

// Reopens reports how often the collection's file was found deleted or
// replaced on disk and reopened (vecdb.DB.Reopens).
func (s *SQLite) Reopens(collection string) uint64 { return s.db.Reopens(collection) }

func toHits(hs []vecdb.Hit) ([]Hit, error) {
	out := make([]Hit, len(hs))
	for i, h := range hs {
		out[i].Score = h.Score
		if err := json.Unmarshal(h.Payload, &out[i].Symbol); err != nil {
			return nil, fmt.Errorf("store: point %s: %w", h.ID, err)
		}
	}
	return out, nil
}

// fields are the indexed keyword values of a symbol, one key per Filter field.
func fields(s *parser.Symbol) map[string][]string {
	m := map[string][]string{
		"id":             {s.ID},
		"language":       {s.Language},
		"file_path":      {s.FilePath},
		"dir_parts":      dirParts(s.FilePath),
		"name":           {s.Name},
		"qualified_name": {s.QualifiedName},
		"qn_suffixes":    qnSuffixes(parser.Normalize(s.QualifiedName)),
		"module":         {s.Module},
		"kind":           {string(s.Kind)},
	}
	for _, r := range s.Relations {
		m["rel_target_id"] = append(m["rel_target_id"], r.TargetID)
		m["rel_target_qualified_name"] = append(m["rel_target_qualified_name"], r.TargetQualifiedName)
		m["rel_target_name"] = append(m["rel_target_name"], r.TargetName)
	}
	return m
}

// cond translates f into vecdb matches; zero fields are left out.
func cond(f Filter) vecdb.Cond {
	var c vecdb.Cond
	add := func(field, val string) {
		if val != "" {
			c = append(c, vecdb.Match{Field: field, Values: []string{val}})
		}
	}
	addAll := func(field string, vals []string) {
		if len(vals) > 0 {
			c = append(c, vecdb.Match{Field: field, Values: vals})
		}
	}
	addAll("id", f.IDs)
	add("language", f.Language)
	add("file_path", f.FilePath)
	if f.PathPrefix != "" {
		add("dir_parts", filepath.Clean(f.PathPrefix))
	}
	add("name", f.Name)
	add("qualified_name", f.QualifiedName)
	if f.QualifiedSuffix != "" {
		// A suffix that normalizes to "" matches nothing (parser.MatchQualified):
		// qn_suffixes never holds "".
		c = append(c, vecdb.Match{Field: "qn_suffixes", Values: []string{parser.Normalize(f.QualifiedSuffix)}})
	}
	add("module", f.Module)
	if len(f.Kinds) > 0 {
		kinds := make([]string, len(f.Kinds))
		for i, k := range f.Kinds {
			kinds[i] = string(k)
		}
		c = append(c, vecdb.Match{Field: "kind", Values: kinds})
	}
	addAll("rel_target_id", f.TargetIDs)
	addAll("rel_target_qualified_name", f.TargetQualifiedNames)
	addAll("rel_target_name", f.TargetNames)
	return c
}

// qnSuffixes lists every suffix of the normalized qualified name on "."
// boundaries, longest first: "a.b.c" -> a.b.c, b.c, c.
func qnSuffixes(norm string) []string {
	var out []string
	for i := 0; i < len(norm); i++ {
		if i == 0 || norm[i-1] == '.' {
			out = append(out, norm[i:])
		}
	}
	return out
}

// dirParts lists every ancestor directory of path, nearest first.
func dirParts(path string) []string {
	var out []string
	for d := filepath.Dir(path); ; d = filepath.Dir(d) {
		out = append(out, d)
		if d == filepath.Dir(d) {
			return out
		}
	}
}

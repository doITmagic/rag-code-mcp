// Package store is the vector store behind one collection per workspace root.
// Every point is one parser.Symbol plus its embedding; the symbol's fields are
// the payload, so exact lookups are payload filters and nothing is re-derived
// at query time.
package store

import (
	"context"
	"errors"

	"github.com/doITmagic/rag-code-mcp/v2/parser"
)

// ErrNotFound is returned when a collection does not exist.
var ErrNotFound = errors.New("store: collection not found")

// errEmptyPath is returned by DeleteByFile for an empty argument, which is
// never a wildcard.
var errEmptyPath = errors.New("store: empty path")

// Item is one point to upsert.
type Item struct {
	Vector []float32
	Symbol parser.Symbol
}

// Hit is one search or scroll result.
type Hit struct {
	Score  float32 // cosine similarity for Search; 0 for Scroll
	Symbol parser.Symbol
}

// Filter narrows Search and Scroll. Zero fields are ignored. Every set field
// must match (AND). QualifiedName and Name are exact matches; use
// QualifiedSuffix for the "ends with .X" lookup parser.MatchQualified does.
type Filter struct {
	IDs                  []string // the symbol id is one of these
	Language             string
	FilePath             string
	PathPrefix           string // file_path is inside this directory (whole path segments, not a raw string prefix)
	Name                 string
	QualifiedName        string
	QualifiedSuffix      string // qualified_name == x or ends with "."+x (normalized form)
	Module               string
	Kinds                []parser.Kind
	TargetIDs            []string // any relation with one of these target_ids
	TargetQualifiedNames []string // any relation with one of these target_qualified_names
	TargetNames          []string // any relation with one of these target_names
}

// Store is implemented by SQLite (on disk) and by the in-memory store used
// in tests. Collection names are opaque; the engine derives them from the root.
type Store interface {
	// EnsureCollection creates the collection when missing. When it exists
	// with another vector size it is dropped and recreated (embedding model
	// changed) and recreated is true.
	EnsureCollection(ctx context.Context, name string, dim int) (recreated bool, err error)
	CollectionExists(ctx context.Context, name string) (bool, error)
	// VectorSize returns the vector size of the collection, ErrNotFound when
	// it is missing.
	VectorSize(ctx context.Context, name string) (int, error)
	DeleteCollection(ctx context.Context, name string) error

	Upsert(ctx context.Context, collection string, items []Item) error
	DeleteByFile(ctx context.Context, collection, filePath string) error

	// Search returns the limit nearest symbols by cosine similarity.
	Search(ctx context.Context, collection string, vector []float32, f Filter, limit int) ([]Hit, error)
	// Scroll returns up to limit symbols matching f, without a vector. limit
	// is capped by the implementation (MaxScroll); callers must not depend on
	// seeing every point.
	Scroll(ctx context.Context, collection string, f Filter, limit int) ([]Hit, error)

	Close() error
}

// MaxScroll is the hard cap on one Scroll call.
const MaxScroll = 1000

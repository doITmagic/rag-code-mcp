package storage

import (
	"context"

	"github.com/qdrant/go-client/qdrant"
)

type UpdateResult = qdrant.UpdateResult

// Point represents a single vector entry in the database.
type Point struct {
	ID      string                 `json:"id"`
	Vector  []float32              `json:"vector"`
	Payload map[string]interface{} `json:"payload"`
}

// SearchQuery defines parameters for a vector search.
type SearchQuery struct {
	Vector   []float32
	Limit    int
	Filter   map[string]interface{}
	MinScore float32
}

// SearchResult is a single match from a search.
type SearchResult struct {
	Point Point
	Score float32
}

// CollectionInfo captures statistics about a collection.
type CollectionInfo struct {
	PointsCount uint64
	VectorSize  uint64
}

// VectorStore is the generic interface for any vector database.
type VectorStore interface {
	// Upsert adds or updates points in a collection.
	Upsert(ctx context.Context, collection string, points []Point) (*UpdateResult, error)

	// Search finds the nearest neighbors.
	Search(ctx context.Context, collection string, query SearchQuery) ([]SearchResult, error)

	// SearchDocsOnly limits the search to documentation/text chunks.
	SearchDocsOnly(ctx context.Context, collection string, query SearchQuery) ([]SearchResult, error)

	// SearchCodeOnly excludes documentation/text chunks.
	SearchCodeOnly(ctx context.Context, collection string, query SearchQuery) ([]SearchResult, error)

	// SearchByChunkType searches using a specific chunk_type filter.
	SearchByChunkType(ctx context.Context, collection string, query SearchQuery, chunkType string) ([]SearchResult, error)

	// CollectionExists checks if a collection and its schema are ready.
	CollectionExists(ctx context.Context, name string) (bool, error)

	// CreateCollection initializes a new collection with the given dimensions.
	CreateCollection(ctx context.Context, name string, dimension int) error

	// GetCollectionInfo returns collection stats (vector size, points count).
	GetCollectionInfo(ctx context.Context, name string) (*CollectionInfo, error)

	// GetCollectionPointCount returns total points within a collection.
	GetCollectionPointCount(ctx context.Context, name string) (uint64, error)

	// ExactSearch performs a metadata-only filter scan without vector comparison.
	// Uses Qdrant Scroll internally — no embedding, no HNSW traversal.
	ExactSearch(ctx context.Context, collection string, filters map[string]interface{}, limit int) ([]SearchResult, error)

	// DeleteByFilter removes points matching a specific metadata filter.
	DeleteByFilter(ctx context.Context, collection string, key string, value interface{}) error

	// DeleteCollection drops an entire collection and all its data.
	DeleteCollection(ctx context.Context, name string) error
}

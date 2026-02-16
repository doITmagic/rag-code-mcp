package storage

import (
	"context"
)

// Point represents a single vector entry in the database.
type Point struct {
	ID       string                 `json:"id"`
	Vector   []float32              `json:"vector"`
	Payload  map[string]interface{} `json:"payload"`
}

// SearchQuery defines parameters for a vector search.
type SearchQuery struct {
	Vector      []float32
	Limit       int
	Filter      map[string]interface{}
	MinScore    float32
}

// SearchResult is a single match from a search.
type SearchResult struct {
	Point Point
	Score float32
}

// VectorStore is the generic interface for any vector database.
type VectorStore interface {
	// Upsert adds or updates points in a collection.
	Upsert(ctx context.Context, collection string, points []Point) error
	
	// Search finds the nearest neighbors.
	Search(ctx context.Context, collection string, query SearchQuery) ([]SearchResult, error)
	
	// CollectionExists checks if a collection and its schema are ready.
	CollectionExists(ctx context.Context, name string) (bool, error)
	
	// CreateCollection initializes a new collection with the given dimensions.
	CreateCollection(ctx context.Context, name string, dimension int) error
}

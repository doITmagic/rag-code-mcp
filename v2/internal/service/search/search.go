package search

import (
	"context"
	"fmt"

	"github.com/doITmagic/rag-code-mcp/v2/pkg/embedding"
	"github.com/doITmagic/rag-code-mcp/v2/pkg/storage"
)

type Service struct {
	embedder embedding.Client
	store    storage.VectorStore
}

// NewService creates a new search service.
func NewService(embedder embedding.Client, store storage.VectorStore) *Service {
	return &Service{
		embedder: embedder,
		store:    store,
	}
}

// Search performs a semantic search in the given collection.
func (s *Service) Search(ctx context.Context, collection string, queryText string, limit int) ([]storage.SearchResult, error) {
	vector, err := s.embedder.Embed(ctx, queryText)
	if err != nil {
		return nil, fmt.Errorf("search embedding failed: %w", err)
	}

	res, err := s.store.Search(ctx, collection, storage.SearchQuery{
		Vector: vector,
		Limit:  limit,
	})
	if err != nil {
		return nil, fmt.Errorf("storage search failed: %w", err)
	}

	return res, nil
}

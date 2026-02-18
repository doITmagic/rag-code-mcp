package search

import (
	"context"
	"fmt"

	"github.com/doITmagic/rag-code-mcp/internal/service/internalutil"
	"github.com/doITmagic/rag-code-mcp/pkg/llm"
	"github.com/doITmagic/rag-code-mcp/pkg/storage"
)

type Service struct {
	embedder llm.Provider
	store    storage.VectorStore
}

// NewService creates a new search service.
func NewService(embedder llm.Provider, store storage.VectorStore) *Service {
	return &Service{
		embedder: embedder,
		store:    store,
	}
}

// Search performs a semantic search in the given collection.
func (s *Service) Search(ctx context.Context, collection string, queryText string, limit int) ([]storage.SearchResult, error) {
	vector64, err := s.embedder.Embed(ctx, queryText)
	if err != nil {
		return nil, fmt.Errorf("search embedding failed: %w", err)
	}
	vector := internalutil.Float64To32(vector64)

	res, err := s.store.Search(ctx, collection, storage.SearchQuery{
		Vector: vector,
		Limit:  limit,
	})
	if err != nil {
		return nil, fmt.Errorf("storage search failed: %w", err)
	}

	return res, nil
}

// SearchCodeOnly performs a semantic search restricted to code chunks only.
func (s *Service) SearchCodeOnly(ctx context.Context, collection string, queryText string, limit int) ([]storage.SearchResult, error) {
	vector64, err := s.embedder.Embed(ctx, queryText)
	if err != nil {
		return nil, fmt.Errorf("search embedding failed: %w", err)
	}
	vector := internalutil.Float64To32(vector64)

	res, err := s.store.SearchCodeOnly(ctx, collection, storage.SearchQuery{
		Vector: vector,
		Limit:  limit,
	})
	if err != nil {
		return nil, fmt.Errorf("storage search failed: %w", err)
	}

	return res, nil
}

// CollectionExists checks whether a collection exists in the vector store.
func (s *Service) CollectionExists(ctx context.Context, collection string) (bool, error) {
	return s.store.CollectionExists(ctx, collection)
}

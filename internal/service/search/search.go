package search

import (
	"context"
	"fmt"

	"math"
	"sort"
	"strings"

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
	vector, err := s.EmbedQuery(ctx, queryText)
	if err != nil {
		return nil, err
	}
	return s.SearchCodeWithVector(ctx, collection, vector, limit)
}

// EmbedQuery converts a text query into a float32 vector using the configured embedder.
// Use this to embed once and reuse the vector across multiple searches.
func (s *Service) EmbedQuery(ctx context.Context, queryText string) ([]float32, error) {
	vector64, err := s.embedder.Embed(ctx, queryText)
	if err != nil {
		return nil, fmt.Errorf("query embedding failed: %w", err)
	}
	return internalutil.Float64To32(vector64), nil
}

// SearchCodeWithVector performs a vector search using a pre-computed embedding.
// Useful when you want to embed once and fan-out to multiple collections.
func (s *Service) SearchCodeWithVector(ctx context.Context, collection string, vector []float32, limit int) ([]storage.SearchResult, error) {
	res, err := s.store.SearchCodeOnly(ctx, collection, storage.SearchQuery{
		Vector: vector,
		Limit:  limit,
	})
	if err != nil {
		return nil, fmt.Errorf("storage search failed: %w", err)
	}
	return res, nil
}

// SearchWithVector performs a full vector search (including docs) using a pre-computed embedding.
// Mirrors Search() but avoids a second embedding call when vector is already available.
func (s *Service) SearchWithVector(ctx context.Context, collection string, vector []float32, limit int) ([]storage.SearchResult, error) {
	res, err := s.store.Search(ctx, collection, storage.SearchQuery{
		Vector: vector,
		Limit:  limit,
	})
	if err != nil {
		return nil, fmt.Errorf("storage search failed: %w", err)
	}
	return res, nil
}

// ExactSearch performs a metadata-only filter scan without generating a vector embedding.
// Delegates directly to the store's ExactSearch (Qdrant Scroll), bypassing HNSW entirely.
func (s *Service) ExactSearch(ctx context.Context, collection string, filter map[string]interface{}, limit int) ([]storage.SearchResult, error) {
	res, err := s.store.ExactSearch(ctx, collection, filter, limit)
	if err != nil {
		return nil, fmt.Errorf("exact search failed: %w", err)
	}
	return res, nil
}

// CollectionExists checks whether a collection exists in the vector store.
func (s *Service) CollectionExists(ctx context.Context, collection string) (bool, error) {
	return s.store.CollectionExists(ctx, collection)
}

// HybridSearch combines semantic search with basic lexical re-ranking.
func (s *Service) HybridSearch(ctx context.Context, collection string, queryText string, limit int) ([]storage.SearchResult, error) {
	// 1. Get semantic candidates
	fetchLimit := int(math.Max(float64(limit*5), 10))
	candidates, err := s.SearchCodeOnly(ctx, collection, queryText, fetchLimit)
	if err != nil {
		return nil, err
	}

	if len(candidates) == 0 {
		return nil, nil
	}

	// 2. Lexical scoring
	lowerQuery := strings.ToLower(queryText)
	tokens := filterTokens(strings.Fields(lowerQuery))

	type scoredResult struct {
		res     storage.SearchResult
		lexical float64
	}

	scored := make([]scoredResult, len(candidates))
	maxLexical := 0.0

	for i, cand := range candidates {
		content := ""
		if txt, ok := cand.Point.Payload["text"].(string); ok {
			content = strings.ToLower(txt)
		} else if txt, ok := cand.Point.Payload["content"].(string); ok {
			content = strings.ToLower(txt)
		}

		lScore := lexicalMatchScore(content, tokens)
		if lScore > maxLexical {
			maxLexical = lScore
		}
		scored[i] = scoredResult{res: cand, lexical: lScore}
	}

	// 3. Combine scores (60% semantic + 40% lexical)
	for i := range scored {
		lexicalNorm := 0.0
		if maxLexical > 0 {
			lexicalNorm = scored[i].lexical / maxLexical
		}
		// Qdrant scores are usually cosine similarity [0, 1]
		scored[i].res.Score = float32(0.6*float64(scored[i].res.Score) + 0.4*lexicalNorm)
	}

	// 4. Sort and limit
	sort.Slice(scored, func(i, j int) bool {
		return scored[i].res.Score > scored[j].res.Score
	})

	finalSize := limit
	if len(scored) < finalSize {
		finalSize = len(scored)
	}

	results := make([]storage.SearchResult, finalSize)
	for i := 0; i < finalSize; i++ {
		results[i] = scored[i].res
	}

	return results, nil
}

func filterTokens(tokens []string) []string {
	filtered := make([]string, 0, len(tokens))
	for _, tok := range tokens {
		tok = strings.TrimSpace(tok)
		if len(tok) > 2 { // Skip very short tokens
			filtered = append(filtered, tok)
		}
	}
	return filtered
}

func lexicalMatchScore(content string, tokens []string) float64 {
	if len(tokens) == 0 {
		return 0
	}
	score := 0.0
	for _, token := range tokens {
		score += float64(strings.Count(content, token))
	}
	return score
}

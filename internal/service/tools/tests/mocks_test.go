package tests

import (
	"context"

	"github.com/doITmagic/rag-code-mcp/internal/config"
	"github.com/doITmagic/rag-code-mcp/internal/service/engine"
	"github.com/doITmagic/rag-code-mcp/internal/service/search"
	"github.com/doITmagic/rag-code-mcp/pkg/indexer"
	"github.com/doITmagic/rag-code-mcp/pkg/llm"
	"github.com/doITmagic/rag-code-mcp/pkg/storage"
	"github.com/doITmagic/rag-code-mcp/pkg/workspace/contract"
	"github.com/doITmagic/rag-code-mcp/pkg/workspace/resolver"
)

type mockVectorStore struct {
	storage.VectorStore
	SearchFunc           func(ctx context.Context, collection string, query storage.SearchQuery) ([]storage.SearchResult, error)
	SearchCodeOnlyFunc   func(ctx context.Context, collection string, query storage.SearchQuery) ([]storage.SearchResult, error)
	ExactSearchFunc      func(ctx context.Context, collection string, filters map[string]interface{}, limit int) ([]storage.SearchResult, error)
	CollectionExistsFunc func(ctx context.Context, name string) (bool, error)
	UpsertFunc           func(ctx context.Context, collection string, points []storage.Point) (*storage.UpdateResult, error)
}

func (m *mockVectorStore) Search(ctx context.Context, collection string, query storage.SearchQuery) ([]storage.SearchResult, error) {
	if m.SearchFunc != nil {
		return m.SearchFunc(ctx, collection, query)
	}
	return nil, nil
}

func (m *mockVectorStore) SearchCodeOnly(ctx context.Context, collection string, query storage.SearchQuery) ([]storage.SearchResult, error) {
	if m.SearchCodeOnlyFunc != nil {
		return m.SearchCodeOnlyFunc(ctx, collection, query)
	}
	return nil, nil
}

func (m *mockVectorStore) ExactSearch(ctx context.Context, collection string, filters map[string]interface{}, limit int) ([]storage.SearchResult, error) {
	if m.ExactSearchFunc != nil {
		return m.ExactSearchFunc(ctx, collection, filters, limit)
	}
	return nil, nil
}

func (m *mockVectorStore) CollectionExists(ctx context.Context, name string) (bool, error) {
	if m.CollectionExistsFunc != nil {
		return m.CollectionExistsFunc(ctx, name)
	}
	return true, nil
}

func (m *mockVectorStore) Upsert(ctx context.Context, collection string, points []storage.Point) (*storage.UpdateResult, error) {
	if m.UpsertFunc != nil {
		return m.UpsertFunc(ctx, collection, points)
	}
	return nil, nil
}

func (m *mockVectorStore) SearchByChunkType(ctx context.Context, collection string, query storage.SearchQuery, chunkType string) ([]storage.SearchResult, error) {
	return nil, nil
}

func (m *mockVectorStore) CreateCollection(ctx context.Context, name string, dimension int) error {
	return nil
}

func (m *mockVectorStore) GetCollectionInfo(ctx context.Context, name string) (*storage.CollectionInfo, error) {
	return &storage.CollectionInfo{PointsCount: 0, VectorSize: 1024}, nil
}

func (m *mockVectorStore) GetCollectionPointCount(ctx context.Context, name string) (uint64, error) {
	return 0, nil
}

func (m *mockVectorStore) DeleteByFilter(ctx context.Context, collection string, key string, value interface{}) error {
	return nil
}

func (m *mockVectorStore) DeleteCollection(ctx context.Context, name string) error {
	return nil
}

type mockLLMProvider struct {
	llm.Provider
	EmbedFunc func(ctx context.Context, text string) ([]float64, error)
}

func (m *mockLLMProvider) Embed(ctx context.Context, text string) ([]float64, error) {
	if m.EmbedFunc != nil {
		return m.EmbedFunc(ctx, text)
	}
	return make([]float64, 1024), nil
}

func (m *mockLLMProvider) Name() string                  { return "mock" }
func (m *mockLLMProvider) GetEmbeddingDimension() uint64 { return 1024 }

type mockDetector struct {
	Root string
}

func (m *mockDetector) DetectFromFilePath(ctx context.Context, filePath string) (*contract.WorkspaceCandidate, *contract.ResolveWorkspaceError) {
	return &contract.WorkspaceCandidate{
		Root:       m.Root,
		Confidence: 1.0,
		Reason:     contract.ReasonFilePath,
		Source:     "file_path",
	}, nil
}

func setupTestEngine(mockStore storage.VectorStore) *engine.Engine {
	mockLLM := &mockLLMProvider{}
	idxSvc := indexer.NewService(mockLLM, mockStore)
	searchSvc := search.NewService(mockLLM, mockStore)
	cfg := &config.Config{}

	eng := engine.NewEngine(idxSvc, searchSvc, "", cfg)

	det := &mockDetector{Root: "/mock/workspace"}
	res := resolver.New(resolver.Dependencies{
		Detector: det,
	})

	eng.SetResolver(res)
	return eng
}

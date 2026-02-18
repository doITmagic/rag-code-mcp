package workspace

import (
	"context"
	"fmt"
	"sync"
	"testing"

	"github.com/doITmagic/rag-code-mcp/internal/config"
	"github.com/doITmagic/rag-code-mcp/internal/llm"
	"github.com/doITmagic/rag-code-mcp/internal/storage"
)

// mockQdrant implements QdrantInterface for testing
type mockQdrant struct {
	exists          bool
	collectionInfo  *storage.CollectionInfo
	pointCount      uint64
	deleteCalled    bool
	createCalled    bool
	createDimension int
}

func (m *mockQdrant) CollectionExists(ctx context.Context, name string) (bool, error) {
	return m.exists, nil
}

func (m *mockQdrant) GetCollectionInfo(ctx context.Context, name string) (*storage.CollectionInfo, error) {
	if m.collectionInfo == nil {
		return nil, fmt.Errorf("collection not found")
	}
	return m.collectionInfo, nil
}

func (m *mockQdrant) GetCollectionPointCount(ctx context.Context, name string) (uint64, error) {
	return m.pointCount, nil
}

func (m *mockQdrant) CreateCollection(ctx context.Context, name string, dimension int) error {
	m.createCalled = true
	m.createDimension = dimension
	m.exists = true
	m.collectionInfo = &storage.CollectionInfo{VectorSize: uint64(dimension)}
	return nil
}

func (m *mockQdrant) DeleteCollection(ctx context.Context, name string) error {
	m.deleteCalled = true
	m.exists = false
	m.collectionInfo = nil
	return nil
}

// fakeLLM implements llm.Provider for testing
type fakeLLM struct {
	llm.Provider
	dimension uint64
}

func (f *fakeLLM) GetEmbeddingDimension() uint64 {
	return f.dimension
}

func (f *fakeLLM) Embed(ctx context.Context, text string) ([]float64, error) {
	return make([]float64, f.dimension), nil
}

func TestDimensionMismatchMigration(t *testing.T) {
	ctx := context.Background()
	
	// Initial state: Collection exists with 768 dimensions
	mockQ := &mockQdrant{
		exists: true,
		collectionInfo: &storage.CollectionInfo{
			VectorSize: 768,
			PointsCount: 100,
		},
	}
	
	// New model has 1024 dimensions
	fakeL := &fakeLLM{dimension: 1024}
	
	cfg := &config.Config{
		Workspace: config.WorkspaceConfig{
			Enabled:   true,
			AutoIndex: true,
		},
	}
	
	mgr := &Manager{
		qdrant:    mockQ,
		llm:       fakeL,
		config:    cfg,
		collLocks: make(map[string]*sync.Mutex),
	}
	
	info := &Info{
		ID:    "test-ws",
		Root:  "/tmp/test-ws",
	}
	
	// Test CheckAndPrepareMigration
	newCol, oldCol, needsMigration, err := mgr.CheckAndPrepareMigration(ctx, info, "go")
	
	if err != nil {
		t.Fatalf("CheckAndPrepareMigration failed: %v", err)
	}
	
	if !needsMigration {
		t.Error("Expected needsMigration to be true")
	}
	
	if !mockQ.deleteCalled {
		t.Error("Expected DeleteCollection to be called")
	}
	
	if !mockQ.createCalled {
		t.Error("Expected CreateCollection to be called")
	}
	
	if mockQ.createDimension != 1024 {
		t.Errorf("Expected created dimension 1024, got %d", mockQ.createDimension)
	}
	
	t.Logf("Migration verified: %s -> %s (recreated with %d)", oldCol, newCol, mockQ.createDimension)
}

func TestAutoMigrationOnMemoryAccess(t *testing.T) {
	// This test verifies that calling GetMemoryForWorkspaceLanguage 
	// triggers migration if dimensions mismatch.
	
	ctx := context.Background()
	
	// Collection exists with 768 dimensions
	mockQ := &mockQdrant{
		exists: true,
		collectionInfo: &storage.CollectionInfo{
			VectorSize: 768,
			PointsCount: 100,
		},
	}
	
	// New model has 1024 dimensions
	fakeL := &fakeLLM{dimension: 1024}
	
	cfg := &config.Config{
		Workspace: config.WorkspaceConfig{
			Enabled:   true,
			AutoIndex: true,
		},
		Storage: config.StorageConfig{
			VectorDB: config.VectorDBConfig{
				URL: "http://localhost:6333",
			},
		},
	}
	
	mgr := NewManager(nil, fakeL, cfg) // NewManager will create detector etc.
	mgr.qdrant = mockQ // Inject our mock
	
	info := &Info{
		ID:    "test-ws-2",
		Root:  "/home/razvan/my-project", // Must not be /tmp or / for detector validation
	}
	
	// We need to avoid real filesystem access in StartWatcher if possible, 
	// or mock it. For now let's hope it doesn't crash on non-existent dir.
	
	_, err := mgr.GetMemoryForWorkspaceLanguage(ctx, info, "go")
	if err != nil {
		t.Fatalf("GetMemoryForWorkspaceLanguage failed: %v", err)
	}
	
	if !mockQ.deleteCalled {
		t.Error("Expected DeleteCollection to be called during GetMemoryForWorkspaceLanguage due to mismatch")
	}
	
	if mockQ.createDimension != 1024 {
		t.Errorf("Expected recreated dimension 1024, got %d", mockQ.createDimension)
	}
}

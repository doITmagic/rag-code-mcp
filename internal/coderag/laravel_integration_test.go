package coderag

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/doITmagic/coderag-mcp/internal/coderag/analyzers/php/laravel"
	"github.com/doITmagic/coderag-mcp/internal/llm"
	"github.com/doITmagic/coderag-mcp/internal/memory"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestIndexer_LaravelProject tests full indexing pipeline with Laravel project
func TestIndexer_LaravelProject(t *testing.T) {
	// Create a mini Laravel project
	tmpDir := t.TempDir()
	appDir := filepath.Join(tmpDir, "app")
	modelsDir := filepath.Join(appDir, "Models")
	require.NoError(t, os.MkdirAll(modelsDir, 0755))

	// Create a simple Laravel model
	userModel := `<?php
namespace App\Models;

use Illuminate\Database\Eloquent\Model;

class User extends Model
{
    protected $fillable = ['name', 'email'];
    
    public function posts()
    {
        return $this->hasMany(Post::class);
    }
}
`
	require.NoError(t, os.WriteFile(filepath.Join(modelsDir, "User.php"), []byte(userModel), 0644))

	// Setup indexer with Laravel adapter
	adapter := laravel.NewAdapter()
	mockEmbedder := &mockProvider{}
	mockLTM := &mockMemoryStore{docs: make(map[string]memory.Document)}

	indexer := NewIndexer(adapter, mockEmbedder, mockLTM)

	// Index the project
	ctx := context.Background()
	count, err := indexer.IndexPaths(ctx, []string{tmpDir}, "test-laravel")
	require.NoError(t, err)

	t.Logf("Indexed %d chunks from Laravel project", count)
	assert.Greater(t, count, 0, "Should index at least one chunk")

	// Verify chunks were stored
	docs := mockLTM.GetAll()
	assert.Len(t, docs, count)

	// Verify Laravel metadata is present
	foundLaravelModel := false
	for _, doc := range docs {
		if doc.Metadata["type"] == "class" && doc.Metadata["name"] == "User" {
			foundLaravelModel = true
			t.Logf("Found User model chunk with metadata: %+v", doc.Metadata)

			// The chunk should have Laravel-specific metadata from the adapter
			assert.Equal(t, "App\\Models", doc.Metadata["package"])
			break
		}
	}

	assert.True(t, foundLaravelModel, "Should find User model chunk")
}

// mockMemoryStore implements memory.LongTermMemory for testing
type mockMemoryStore struct {
	docs map[string]memory.Document
}

func (m *mockMemoryStore) Store(ctx context.Context, doc memory.Document) error {
	m.docs[doc.ID] = doc
	return nil
}

func (m *mockMemoryStore) Search(ctx context.Context, query []float64, limit int) ([]memory.Document, error) {
	return nil, nil
}

func (m *mockMemoryStore) GetAll() []memory.Document {
	docs := make([]memory.Document, 0, len(m.docs))
	for _, doc := range m.docs {
		docs = append(docs, doc)
	}
	return docs
}

func (m *mockMemoryStore) Clear(ctx context.Context) error {
	m.docs = make(map[string]memory.Document)
	return nil
}

func (m *mockMemoryStore) Delete(ctx context.Context, id string) error {
	delete(m.docs, id)
	return nil
}

// TestAnalyzerManager_WithBarouProject tests analyzer selection with real Barou project
func TestAnalyzerManager_WithBarouProject(t *testing.T) {
	barouPath := "/home/razvan/go/src/github.com/doITmagic/coderag-mcp/barou"

	if _, err := os.Stat(barouPath); os.IsNotExist(err) {
		t.Skip("Barou project not found")
		return
	}

	mgr := NewAnalyzerManager()

	// Test that Laravel project type returns Laravel adapter
	analyzer := mgr.CodeAnalyzerForProjectType("laravel")
	require.NotNil(t, analyzer)

	// Verify it's the Laravel adapter by checking type
	_, ok := analyzer.(*laravel.Adapter)
	assert.True(t, ok, "Should return Laravel adapter for 'laravel' project type")

	// Test with php-laravel
	analyzer = mgr.CodeAnalyzerForProjectType("php-laravel")
	require.NotNil(t, analyzer)
	_, ok = analyzer.(*laravel.Adapter)
	assert.True(t, ok, "Should return Laravel adapter for 'php-laravel' project type")

	// Test with php
	analyzer = mgr.CodeAnalyzerForProjectType("php")
	require.NotNil(t, analyzer)
	_, ok = analyzer.(*laravel.Adapter)
	assert.True(t, ok, "Should return Laravel adapter for 'php' project type")
}

// mockProvider implements llm.Provider for testing
type mockProvider struct{}

func (m *mockProvider) Embed(ctx context.Context, text string) ([]float64, error) {
	// Return a dummy embedding vector
	return make([]float64, 384), nil
}

func (m *mockProvider) Generate(ctx context.Context, prompt string, opts ...llm.GenerateOption) (string, error) {
	return "mock response", nil
}

func (m *mockProvider) GenerateStream(ctx context.Context, prompt string, opts ...llm.GenerateOption) (<-chan string, <-chan error) {
	ch := make(chan string, 1)
	errCh := make(chan error, 1)
	ch <- "mock response"
	close(ch)
	close(errCh)
	return ch, errCh
}

func (m *mockProvider) Name() string {
	return "mock"
}

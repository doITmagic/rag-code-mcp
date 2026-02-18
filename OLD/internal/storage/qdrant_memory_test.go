package storage

import (
	"context"
	"testing"

	"github.com/doITmagic/rag-code-mcp/internal/memory"
)

func TestQdrantLongTermMemoryStoreValidation(t *testing.T) {
	m := &QdrantLongTermMemory{}
	ctx := context.Background()

	// Missing ID
	if err := m.Store(ctx, memory.Document{ID: "", Embedding: []float64{0.1}}); err == nil {
		t.Fatalf("Store with empty ID = nil error, want non-nil")
	}

	// Missing embedding
	if err := m.Store(ctx, memory.Document{ID: "doc-1", Embedding: nil}); err == nil {
		t.Fatalf("Store with empty embedding = nil error, want non-nil")
	}
}

func TestQdrantLongTermMemorySearchValidation(t *testing.T) {
	m := &QdrantLongTermMemory{}
	ctx := context.Background()

	if _, err := m.Search(ctx, nil, 10); err == nil {
		t.Fatalf("Search with empty query = nil error, want non-nil")
	}
}

func TestConvertSearchResultsToDocuments(t *testing.T) {
	results := []SearchResult{
		{
			ID:    "123",
			Score: 0.9,
			Payload: map[string]interface{}{
				"content": "hello world",
				"lang":    "go",
			},
		},
	}

	docs := convertSearchResultsToDocuments(results)
	if len(docs) != 1 {
		t.Fatalf("len(docs) = %d, want 1", len(docs))
	}

	d := docs[0]
	if d.ID != "123" {
		t.Errorf("doc.ID = %q, want %q", d.ID, "123")
	}
	if d.Content != "hello world" {
		t.Errorf("doc.Content = %q, want %q", d.Content, "hello world")
	}
	if v, ok := d.Metadata["lang"]; !ok || v != "go" {
		t.Errorf("doc.Metadata[lang] = %#v, want %q", v, "go")
	}
	if v, ok := d.Metadata["content"]; ok {
		t.Errorf("expected content to be removed from Metadata, found %#v", v)
	}
	if v, ok := d.Metadata["score"]; !ok || v != 0.9 {
		t.Errorf("doc.Metadata[score] = %#v, want %v", v, 0.9)
	}
}

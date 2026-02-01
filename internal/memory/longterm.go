package memory

import (
	"context"
	"fmt"
)

// Document represents a document stored in long-term memory
type Document struct {
	ID        string
	Content   string
	Embedding []float64
	Metadata  map[string]interface{}
}

// LongTermMemory manages persistent vector storage
type LongTermMemory interface {
	// Store stores a document with its embedding
	Store(ctx context.Context, doc Document) error

	// Search searches for similar documents
	Search(ctx context.Context, query []float64, limit int) ([]Document, error)

	// Delete deletes a document by ID
	Delete(ctx context.Context, id string) error

	// DeleteByMetadata deletes documents matching a metadata key-value pair
	DeleteByMetadata(ctx context.Context, key, value string) error

	// Clear clears all documents
	Clear(ctx context.Context) error
}

// InMemoryLongTermMemory is a simple in-memory implementation for testing
type InMemoryLongTermMemory struct {
	documents map[string]Document
}

// NewInMemoryLongTermMemory creates a new in-memory long-term memory
func NewInMemoryLongTermMemory() *InMemoryLongTermMemory {
	return &InMemoryLongTermMemory{
		documents: make(map[string]Document),
	}
}

// Store stores a document
func (m *InMemoryLongTermMemory) Store(ctx context.Context, doc Document) error {
	if doc.ID == "" {
		return fmt.Errorf("document ID is required")
	}
	m.documents[doc.ID] = doc
	return nil
}

// Search searches for similar documents (simplified cosine similarity)
func (m *InMemoryLongTermMemory) Search(ctx context.Context, query []float64, limit int) ([]Document, error) {
	// TODO: Implement proper similarity search
	// This is a placeholder that returns all documents
	results := make([]Document, 0, len(m.documents))
	for _, doc := range m.documents {
		results = append(results, doc)
		if len(results) >= limit {
			break
		}
	}
	return results, nil
}

// Delete deletes a document
func (m *InMemoryLongTermMemory) Delete(ctx context.Context, id string) error {
	delete(m.documents, id)
	return nil
}

// DeleteByMetadata deletes documents matching a metadata key-value pair
func (m *InMemoryLongTermMemory) DeleteByMetadata(ctx context.Context, key, value string) error {
	for id, doc := range m.documents {
		if val, ok := doc.Metadata[key]; ok {
			if strVal, ok := val.(string); ok && strVal == value {
				delete(m.documents, id)
			}
		}
	}
	return nil
}

// Clear clears all documents
func (m *InMemoryLongTermMemory) Clear(ctx context.Context) error {
	m.documents = make(map[string]Document)
	return nil
}

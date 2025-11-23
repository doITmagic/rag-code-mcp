package storage

import (
	"context"
	"fmt"

	"github.com/doITmagic/rag-code-mcp/internal/memory"
)

// QdrantLongTermMemory implements memory.LongTermMemory using Qdrant
type QdrantLongTermMemory struct {
	client *QdrantClient
}

// NewQdrantLongTermMemory creates a new Qdrant-backed long-term memory
func NewQdrantLongTermMemory(client *QdrantClient) *QdrantLongTermMemory {
	return &QdrantLongTermMemory{
		client: client,
	}
}

// Store stores a document with its embedding
func (m *QdrantLongTermMemory) Store(ctx context.Context, doc memory.Document) error {
	if doc.ID == "" {
		return fmt.Errorf("document ID is required")
	}

	if len(doc.Embedding) == 0 {
		return fmt.Errorf("document embedding is required")
	}

	// Prepare payload
	payload := make(map[string]interface{})
	payload["content"] = doc.Content

	// Add metadata to payload
	for key, val := range doc.Metadata {
		payload[key] = val
	}

	// Store in Qdrant
	if err := m.client.Upsert(ctx, doc.ID, doc.Embedding, payload); err != nil {
		return fmt.Errorf("failed to store document in qdrant: %w", err)
	}

	return nil
}

// Search searches for similar documents
func (m *QdrantLongTermMemory) Search(ctx context.Context, query []float64, limit int) ([]memory.Document, error) {
	if len(query) == 0 {
		return nil, fmt.Errorf("query embedding is required")
	}

	// Search in Qdrant
	results, err := m.client.Search(ctx, query, limit)
	if err != nil {
		return nil, fmt.Errorf("failed to search in qdrant: %w", err)
	}

	return convertSearchResultsToDocuments(results), nil
}

func convertSearchResultsToDocuments(results []SearchResult) []memory.Document {
	documents := make([]memory.Document, 0, len(results))
	for _, result := range results {
		doc := memory.Document{
			ID:       result.ID,
			Content:  fmt.Sprintf("%v", result.Payload["content"]),
			Metadata: make(map[string]interface{}),
		}

		// Extract metadata (skip 'content' field)
		for key, val := range result.Payload {
			if key != "content" {
				doc.Metadata[key] = val
			}
		}

		// Add score to metadata
		doc.Metadata["score"] = result.Score

		documents = append(documents, doc)
	}

	return documents
}

// Delete deletes a document by ID
func (m *QdrantLongTermMemory) Delete(ctx context.Context, id string) error {
	if err := m.client.Delete(ctx, id); err != nil {
		return fmt.Errorf("failed to delete document from qdrant: %w", err)
	}
	return nil
}

// DeleteByMetadata deletes documents matching a metadata key-value pair
func (m *QdrantLongTermMemory) DeleteByMetadata(ctx context.Context, key, value string) error {
	if err := m.client.DeleteByFilter(ctx, key, value); err != nil {
		return fmt.Errorf("failed to delete documents by metadata from qdrant: %w", err)
	}
	return nil
}

// Clear clears all documents (not implemented for safety - use with caution)
func (m *QdrantLongTermMemory) Clear(ctx context.Context) error {
	// For safety, we don't implement collection deletion
	// You would need to delete the collection and recreate it
	return fmt.Errorf("clear operation not implemented for safety - please delete and recreate collection manually")
}

// CollectionExists checks if the collection exists
func (m *QdrantLongTermMemory) CollectionExists(ctx context.Context, collectionName string) (bool, error) {
	return m.client.CollectionExists(ctx, collectionName)
}

// GetCollectionPointCount returns the number of points (documents) in a collection
func (m *QdrantLongTermMemory) GetCollectionPointCount(ctx context.Context, collectionName string) (uint64, error) {
	return m.client.GetCollectionPointCount(ctx, collectionName)
}

// Ensure QdrantLongTermMemory implements memory.LongTermMemory
var _ memory.LongTermMemory = (*QdrantLongTermMemory)(nil)

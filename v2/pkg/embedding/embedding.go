package embedding

import "context"

// Client is the interface for generating embeddings.
type Client interface {
	// Embed generates an embedding vector for the given text.
	Embed(ctx context.Context, text string) ([]float32, error)
	
	// EmbedBatch generates embeddings for multiple texts.
	EmbedBatch(ctx context.Context, texts []string) ([][]float32, error)
	
	// Dimension returns the size of the embedding vector.
	Dimension(ctx context.Context) (int, error)
}

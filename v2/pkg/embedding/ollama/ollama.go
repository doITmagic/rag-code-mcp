package ollama

import (
	"context"
	"fmt"
	"sync"

	"github.com/tmc/langchaingo/llms/ollama"
)

type Client struct {
	model     string
	client    *ollama.LLM
	dimension int
	dimOnce   sync.Once
}

// NewClient creates a new Ollama embedding client.
func NewClient(baseURL, model string) (*Client, error) {
	c, err := ollama.New(
		ollama.WithServerURL(baseURL),
		ollama.WithModel(model),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create ollama client: %w", err)
	}
	return &Client{
		model:  model,
		client: c,
	}, nil
}

func (c *Client) Embed(ctx context.Context, text string) ([]float32, error) {
	embeddings, err := c.client.CreateEmbedding(ctx, []string{text})
	if err != nil {
		return nil, err
	}
	if len(embeddings) == 0 {
		return nil, fmt.Errorf("no embeddings returned")
	}
	// Convert float64 to float32
	res := make([]float32, len(embeddings[0]))
	for i, v := range embeddings[0] {
		res[i] = float32(v)
	}
	return res, nil
}

func (c *Client) EmbedBatch(ctx context.Context, texts []string) ([][]float32, error) {
	if len(texts) == 0 {
		return nil, nil
	}
	embeddings, err := c.client.CreateEmbedding(ctx, texts)
	if err != nil {
		return nil, err
	}
	
	res := make([][]float32, len(embeddings))
	for i, emb := range embeddings {
		res[i] = make([]float32, len(emb))
		for j, v := range emb {
			res[i][j] = float32(v)
		}
	}
	return res, nil
}

func (c *Client) Dimension(ctx context.Context) (int, error) {
	var err error
	c.dimOnce.Do(func() {
		var emb []float32
		emb, err = c.Embed(ctx, "test")
		if err == nil {
			c.dimension = len(emb)
		}
	})
	return c.dimension, err
}

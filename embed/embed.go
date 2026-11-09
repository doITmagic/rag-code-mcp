// Package embed turns text into vectors. One implementation talks to Ollama's
// /api/embed over plain HTTP; a deterministic fake backs the tests.
package embed

import (
	"context"
	"errors"
)

// ErrUnavailable wraps every transport failure so callers can report
// "embedder down" without inspecting the cause.
var ErrUnavailable = errors.New("embed: embedder unavailable")

// Embedder embeds a batch of texts. Implementations must be safe for
// concurrent use and must honour ctx.
type Embedder interface {
	// Embed returns one vector per text, in order.
	Embed(ctx context.Context, texts []string) ([][]float32, error)
	// Dimension returns the vector size, probing the model once and caching.
	Dimension(ctx context.Context) (int, error)
	// Model names the embedding model, for status output.
	Model() string
}

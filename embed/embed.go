// Package embed turns text into vectors. One implementation runs llama.cpp's
// llama-server as a child process and talks to it over loopback HTTP; a
// deterministic fake backs the tests.
package embed

import (
	"context"
	"errors"
)

// ErrFetching is returned, wrapped in ErrUnavailable, to a call made with
// WithoutFetchWait while the embedding runtime or model is being downloaded.
var ErrFetching = errors.New("the embedding runtime or model is downloading; retry shortly")

// ErrModelChanged is returned, wrapped in ErrUnavailable, when the model file
// is not the one this process named the index after: the embedder refuses to
// start on it, since its vectors would be mixed into an index of another model.
var ErrModelChanged = errors.New("the embedding model file changed on disk since RagCode started")

type noFetchWait struct{}

// WithoutFetchWait marks a call that must not wait for a download. A search
// that needs only a query vector passes it: when the runtime or the model is
// being downloaded, which takes minutes, the call starts that download if none
// runs and returns at once with ErrFetching, instead of blocking. A model that
// is loading, which takes seconds, is still waited for. Calls without the mark,
// such as an index run, wait through the download.
func WithoutFetchWait(ctx context.Context) context.Context {
	return context.WithValue(ctx, noFetchWait{}, true)
}

func fetchWaitDisabled(ctx context.Context) bool { return ctx.Value(noFetchWait{}) != nil }

// ErrUnavailable wraps every start, transport and protocol failure so callers can report
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

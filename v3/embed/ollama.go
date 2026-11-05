package embed

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"time"
)

const (
	batchSize   = 16
	batchBytes  = 8 << 10 // CPU Ollama embeds inputs one by one: a big request only delays a queued query
	callTimeout = 60 * time.Second
	retryDelay  = time.Second
	probeText   = "dimension probe"
)

// Ollama embeds through Ollama's /api/embed endpoint.
type Ollama struct {
	base   string
	model  string
	client *http.Client
	dim    atomic.Int32 // 0 until the first successful probe
}

// NewOllama returns an embedder for model at baseURL. A nil client means http.DefaultClient.
func NewOllama(baseURL, model string, client *http.Client) *Ollama {
	if client == nil {
		client = http.DefaultClient
	}
	return &Ollama{base: strings.TrimRight(baseURL, "/"), model: model, client: client}
}

// Model returns the embedding model name.
func (o *Ollama) Model() string { return o.model }

// Embed embeds texts in batches of at most 16 texts and 8 KiB (a longer
// text goes alone), one request per batch.
func (o *Ollama) Embed(ctx context.Context, texts []string) ([][]float32, error) {
	out := make([][]float32, 0, len(texts))
	for start, end := 0, 0; start < len(texts); start = end {
		end = start + 1
		for size := len(texts[start]); end < len(texts) && end-start < batchSize && size+len(texts[end]) <= batchBytes; end++ {
			size += len(texts[end])
		}
		vecs, err := o.embedBatch(ctx, texts[start:end])
		if err != nil {
			return nil, err
		}
		out = append(out, vecs...)
	}
	return out, nil
}

// Dimension probes the model once and caches the vector size. A failed probe
// is retried on the next call.
func (o *Ollama) Dimension(ctx context.Context) (int, error) {
	if d := o.dim.Load(); d > 0 {
		return int(d), nil
	}
	vecs, err := o.embedBatch(ctx, []string{probeText})
	if err != nil {
		return 0, err
	}
	o.dim.Store(int32(len(vecs[0])))
	return len(vecs[0]), nil
}

// embedBatch sends one /api/embed request, retrying once on a transport error.
func (o *Ollama) embedBatch(ctx context.Context, texts []string) ([][]float32, error) {
	body, err := json.Marshal(map[string]any{"model": o.model, "input": texts})
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrUnavailable, err)
	}
	var resp struct {
		Embeddings [][]float32 `json:"embeddings"`
	}
	for attempt := 0; ; attempt++ {
		resp.Embeddings = nil
		err = o.post(ctx, body, &resp)
		if err == nil {
			break
		}
		var te *transportError
		if attempt > 0 || !errors.As(err, &te) || ctx.Err() != nil {
			return nil, err
		}
		select {
		case <-ctx.Done():
			return nil, err
		case <-time.After(retryDelay):
		}
	}
	if len(resp.Embeddings) != len(texts) {
		return nil, fmt.Errorf("%w: got %d embeddings for %d inputs", ErrUnavailable, len(resp.Embeddings), len(texts))
	}
	for i, v := range resp.Embeddings {
		if len(v) == 0 {
			return nil, fmt.Errorf("%w: empty embedding for input %d", ErrUnavailable, i)
		}
	}
	return resp.Embeddings, nil
}

func (o *Ollama) post(ctx context.Context, body []byte, out any) error {
	ctx, cancel := withTimeout(ctx)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, o.base+"/api/embed", bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("%w: %w", ErrUnavailable, err)
	}
	req.Header.Set("Content-Type", "application/json")
	return o.do(req, out)
}

// do runs req and decodes a 200 JSON body into out. Connection-level failures
// come back as *transportError so embedBatch can tell them from HTTP errors.
func (o *Ollama) do(req *http.Request, out any) error {
	res, err := o.client.Do(req)
	if err != nil {
		return &transportError{err}
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		msg, _ := io.ReadAll(io.LimitReader(res.Body, 512))
		hint := ""
		if res.StatusCode == http.StatusPreconditionFailed { // old Ollama refusing a newer model, with an empty message
			hint = " (this model needs a newer Ollama, 0.40 or later: upgrade Ollama)"
		}
		return fmt.Errorf("%w: ollama %s: %s%s", ErrUnavailable, res.Status, strings.TrimSpace(string(msg)), hint)
	}
	if err := json.NewDecoder(res.Body).Decode(out); err != nil {
		return fmt.Errorf("%w: bad response: %w", ErrUnavailable, err)
	}
	return nil
}

type transportError struct{ err error }

func (e *transportError) Error() string   { return "embed: embedder unavailable: " + e.err.Error() }
func (e *transportError) Unwrap() []error { return []error{ErrUnavailable, e.err} }

// withTimeout caps a call at callTimeout when ctx carries no deadline.
func withTimeout(ctx context.Context) (context.Context, context.CancelFunc) {
	if _, ok := ctx.Deadline(); ok {
		return context.WithCancel(ctx)
	}
	return context.WithTimeout(ctx, callTimeout)
}

package llm

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/doITmagic/rag-code-mcp/internal/config"
	"github.com/ollama/ollama/api"
)

// defaultKeepAlive is how long Ollama keeps the embedding model loaded in memory
// after the last request. 30 minutes prevents constant cold-starts when other
// programs (IDE, chat) load competing models into Ollama.
const defaultKeepAlive = 30 * time.Minute

// OllamaLLMProvider implements Provider interface for Ollama using the native client.
type OllamaLLMProvider struct {
	client    *api.Client
	embedName string
	cachedDim uint64
	dimOnce   sync.Once
	keepAlive api.Duration
}

// NewOllamaLLMProvider creates a new Ollama provider using the native ollama/api client.
func NewOllamaLLMProvider(cfg config.LLMConfig) (*OllamaLLMProvider, error) {
	baseURL := cfg.OllamaBaseURL
	if baseURL == "" {
		baseURL = cfg.BaseURL
	}
	if baseURL == "" {
		baseURL = "http://localhost:11434"
	}

	embedModelName := cfg.OllamaEmbed
	if embedModelName == "" {
		embedModelName = cfg.EmbedModel
	}
	// Accept OllamaModel / Model as fallback for backward-compat
	if embedModelName == "" {
		embedModelName = cfg.OllamaModel
	}
	if embedModelName == "" {
		embedModelName = cfg.Model
	}
	if embedModelName == "" {
		return nil, fmt.Errorf("ollama model is required (set ollama_embed in config)")
	}

	// Parse the base URL and create the native Ollama client
	parsedURL, err := url.Parse(baseURL)
	if err != nil {
		return nil, fmt.Errorf("invalid ollama base URL %q: %w", baseURL, err)
	}

	httpClient := &http.Client{
		Timeout: 120 * time.Second,
		Transport: &http.Transport{
			MaxIdleConns:        10,
			IdleConnTimeout:     90 * time.Second,
			DisableKeepAlives:   false,
			MaxIdleConnsPerHost: 5,
		},
	}

	client := api.NewClient(parsedURL, httpClient)

	log.Printf("🎯 Ollama (native client): embed=%s, url=%s, keep_alive=%v", embedModelName, baseURL, defaultKeepAlive)

	return &OllamaLLMProvider{
		client:    client,
		embedName: embedModelName,
		keepAlive: api.Duration{Duration: defaultKeepAlive},
	}, nil
}

// Heartbeat performs a native liveness check against Ollama.
// Returns nil if Ollama is responsive, error otherwise.
func (p *OllamaLLMProvider) Heartbeat(ctx context.Context) error {
	return p.client.Heartbeat(ctx)
}

// IsModelLoaded checks if the embedding model is currently loaded in Ollama's memory.
func (p *OllamaLLMProvider) IsModelLoaded(ctx context.Context) bool {
	checkCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	ps, err := p.client.ListRunning(checkCtx)
	if err != nil {
		return false
	}

	for _, m := range ps.Models {
		// Compare base model names (strip tags for flexible matching)
		if m.Name == p.embedName || strings.HasPrefix(m.Name, strings.Split(p.embedName, ":")[0]) {
			return true
		}
	}
	return false
}

// EnsureLoaded checks if the embedding model is in Ollama's memory.
// If not (e.g. another program loaded a different model), it triggers a warmup reload.
// This should be called before indexing batches or after circuit breaker recovery.
func (p *OllamaLLMProvider) EnsureLoaded(ctx context.Context) error {
	if p.IsModelLoaded(ctx) {
		return nil
	}

	log.Printf("[WARN] 🔄 Embedding model '%s' is NOT loaded in Ollama memory — reloading...", p.embedName)
	return p.Warmup(ctx)
}

// Generate is not supported; this provider is embedding-only.
func (p *OllamaLLMProvider) Generate(_ context.Context, _ string, _ ...GenerateOption) (string, error) {
	return "", fmt.Errorf("text generation not supported: provider is configured for embedding only")
}

// GenerateStream is not supported; this provider is embedding-only.
func (p *OllamaLLMProvider) GenerateStream(_ context.Context, _ string, _ ...GenerateOption) (<-chan string, <-chan error) {
	textChan := make(chan string)
	errChan := make(chan error, 1)
	go func() {
		defer close(textChan)
		defer close(errChan)
		errChan <- fmt.Errorf("text generation not supported: provider is configured for embedding only")
	}()
	return textChan, errChan
}

// Warmup pre-loads the embedding model into Ollama's memory.
// Call this at startup to avoid cold-start timeouts on the first embed request.
// Uses a generous 2-minute timeout since model loading can be slow.
func (p *OllamaLLMProvider) Warmup(ctx context.Context) error {
	log.Printf("🔥 Warming up Ollama model '%s' (pre-loading into memory)...", p.embedName)
	warmupCtx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()

	start := time.Now()
	resp, err := p.client.Embed(warmupCtx, &api.EmbedRequest{
		Model:     p.embedName,
		Input:     "warmup",
		KeepAlive: &p.keepAlive,
	})
	if err != nil {
		return fmt.Errorf("warmup embed failed for '%s': %w", p.embedName, err)
	}

	dim := 0
	if len(resp.Embeddings) > 0 {
		dim = len(resp.Embeddings[0])
		p.cachedDim = uint64(dim)
	}

	log.Printf("✅ Ollama model '%s' warmed up: dim=%d, load=%v, total=%v",
		p.embedName, dim, resp.LoadDuration, time.Since(start))
	return nil
}

// Embed generates embeddings using the native Ollama API client.
// Uses api.Client.Embed which supports batch input and returns [][]float32.
// Sets keep_alive to prevent Ollama from unloading the model between requests
// (critical when other programs compete for Ollama's model slots).
func (p *OllamaLLMProvider) Embed(ctx context.Context, text string) ([]float64, error) {
	resp, err := p.client.Embed(ctx, &api.EmbedRequest{
		Model:     p.embedName,
		Input:     text,
		KeepAlive: &p.keepAlive,
	})
	if err != nil {
		return nil, fmt.Errorf("ollama embed failed: %w", err)
	}

	if len(resp.Embeddings) == 0 || len(resp.Embeddings[0]) == 0 {
		return nil, fmt.Errorf("empty embedding returned for model %s", p.embedName)
	}

	// Convert float32 to float64
	raw := resp.Embeddings[0]
	result := make([]float64, len(raw))
	for i, v := range raw {
		result[i] = float64(v)
	}

	return result, nil
}

// GetEmbeddingDimension returns the dimension of the embedding model.
// Strategy:
//  1. Return cached dimension if already known
//  2. Query Ollama /api/show for model_info (native, no hardcoded table)
//  3. Fallback: probe with a dummy embedding
func (p *OllamaLLMProvider) GetEmbeddingDimension() uint64 {
	// 1. Return cached dimension if already known
	if p.cachedDim > 0 {
		return p.cachedDim
	}

	// 2+3. Try /api/show first, then probe — both via dimOnce
	p.dimOnce.Do(func() {
		// 2. Try querying the model info via native API
		dim := p.queryModelDimension()
		if dim > 0 {
			p.cachedDim = dim
			log.Printf("✅ Embedding dimension for '%s' from /api/show: %d", p.embedName, dim)
			return
		}

		// 3. Fallback: Probe with a dummy embedding
		log.Printf("🔍 Probing Ollama for embedding dimension of model '%s'...", p.embedName)
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()

		vec, err := p.Embed(ctx, "probe")
		if err == nil && len(vec) > 0 {
			p.cachedDim = uint64(len(vec))
			log.Printf("✅ Auto-detected embedding dimension for '%s': %d", p.embedName, p.cachedDim)
		} else {
			log.Printf("⚠️  WARNING: Failed to probe dimension for '%s': %v. Defaulting to 1024.", p.embedName, err)
			p.cachedDim = 1024 // Final fallback
		}
	})

	return p.cachedDim
}

// queryModelDimension queries Ollama /api/show to extract embedding_length from model_info.
// Searches for keys like "*.embedding_length" across different model architectures.
func (p *OllamaLLMProvider) queryModelDimension() uint64 {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	showResp, err := p.client.Show(ctx, &api.ShowRequest{
		Model: p.embedName,
	})
	if err != nil {
		log.Printf("⚠️  Could not query model info for '%s': %v", p.embedName, err)
		return 0
	}

	// model_info is a map[string]any — search for any key ending in "embedding_length"
	for key, val := range showResp.ModelInfo {
		if len(key) > 16 && key[len(key)-16:] == "embedding_length" {
			switch v := val.(type) {
			case float64:
				return uint64(v)
			case int:
				return uint64(v)
			case int64:
				return uint64(v)
			}
		}
	}

	return 0
}

// Name returns the provider name
func (p *OllamaLLMProvider) Name() string {
	return "ollama"
}

// Client returns the underlying native Ollama API client.
// This allows healthcheck and other packages to use Heartbeat(), List(), etc.
func (p *OllamaLLMProvider) Client() *api.Client {
	return p.client
}

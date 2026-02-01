package llm

import (
	"context"
	"fmt"
	"io"
	"time"

	"github.com/doITmagic/rag-code-mcp/internal/config"
	"github.com/doITmagic/rag-code-mcp/internal/utils"
)

// Provider represents an LLM provider interface
type Provider interface {
	// Generate generates text completion
	Generate(ctx context.Context, prompt string, opts ...GenerateOption) (string, error)

	// GenerateStream generates text completion with streaming
	GenerateStream(ctx context.Context, prompt string, opts ...GenerateOption) (<-chan string, <-chan error)

	// Embed generates embeddings for the given text
	Embed(ctx context.Context, text string) ([]float64, error)

	// Name returns the provider name
	Name() string
}

// GenerateOptions contains options for text generation
type GenerateOptions struct {
	Temperature   float64
	MaxTokens     int
	StopWords     []string
	StopSequences []string
	TopP          float64
	TopK          int
	Stream        bool
}

// GenerateOption is a function that modifies GenerateOptions
type GenerateOption func(*GenerateOptions)

// WithTemperature sets the temperature
func WithTemperature(temp float64) GenerateOption {
	return func(opts *GenerateOptions) {
		opts.Temperature = temp
	}
}

// WithMaxTokens sets the max tokens
func WithMaxTokens(tokens int) GenerateOption {
	return func(opts *GenerateOptions) {
		opts.MaxTokens = tokens
	}
}

// WithStopWords sets the stop words
func WithStopWords(words []string) GenerateOption {
	return func(opts *GenerateOptions) {
		opts.StopWords = words
	}
}

// NewProvider creates a new LLM provider based on configuration
func NewProvider(cfg *config.LLMConfig) (Provider, error) {
	// MCP RagCode currently uses only Ollama as LLM provider
	switch cfg.Provider {
	case "", "ollama":
		p, err := NewOllamaLLMProvider(*cfg)
		if err != nil {
			// Ensure we don't return a non-nil Provider when err != nil
			return nil, err
		}
		return p, nil
	default:
		return nil, fmt.Errorf("unknown provider: %s (supported: ollama)", cfg.Provider)
	}
}

// RetryableProvider wraps a provider with retry logic
type RetryableProvider struct {
	provider   Provider
	maxRetries int
	timeout    time.Duration
}

// NewRetryableProvider creates a new retryable provider
func NewRetryableProvider(provider Provider, maxRetries int, timeout time.Duration) *RetryableProvider {
	return &RetryableProvider{
		provider:   provider,
		maxRetries: maxRetries,
		timeout:    timeout,
	}
}

// Generate generates text with retry logic
func (r *RetryableProvider) Generate(ctx context.Context, prompt string, opts ...GenerateOption) (string, error) {
	var result string
	err := utils.Retry(r.maxRetries, time.Second, func() error {
		timeoutCtx, cancel := context.WithTimeout(ctx, r.timeout)
		defer cancel()

		var err error
		result, err = r.provider.Generate(timeoutCtx, prompt, opts...)
		return err
	})
	return result, err
}

// GenerateStream generates streaming text with retry logic
func (r *RetryableProvider) GenerateStream(ctx context.Context, prompt string, opts ...GenerateOption) (<-chan string, <-chan error) {
	return r.provider.GenerateStream(ctx, prompt, opts...)
}

// Embed generates embeddings with retry logic
func (r *RetryableProvider) Embed(ctx context.Context, text string) ([]float64, error) {
	var result []float64
	err := utils.Retry(r.maxRetries, time.Second, func() error {
		timeoutCtx, cancel := context.WithTimeout(ctx, r.timeout)
		defer cancel()

		var err error
		result, err = r.provider.Embed(timeoutCtx, text)
		return err
	})
	return result, err
}

// Name returns the provider name
func (r *RetryableProvider) Name() string {
	return r.provider.Name()
}

var _ Provider = (*RetryableProvider)(nil)
var _ io.Closer = (*RetryableProvider)(nil)

// Close implements io.Closer
func (r *RetryableProvider) Close() error {
	if closer, ok := r.provider.(io.Closer); ok {
		return closer.Close()
	}
	return nil
}

package llm

import (
	"context"
	"fmt"
	"log"

	"github.com/doITmagic/coderag-mcp/internal/config"
	"github.com/tmc/langchaingo/llms"
	"github.com/tmc/langchaingo/llms/ollama"
)

// OllamaLLMProvider implements Provider interface for Ollama
type OllamaLLMProvider struct {
	chatModel  llms.Model
	embedModel llms.Model
	chatName   string
	embedName  string
	config     config.LLMConfig
}

// NewOllamaLLMProvider creates a new Ollama provider with separate chat and embedding models
func NewOllamaLLMProvider(cfg config.LLMConfig) (*OllamaLLMProvider, error) {
	// Server URL
	baseURL := cfg.OllamaBaseURL
	if baseURL == "" {
		baseURL = cfg.BaseURL
	}
	if baseURL == "" {
		baseURL = "http://localhost:11434"
	}

	// Chat model
	chatModelName := cfg.OllamaModel
	if chatModelName == "" {
		chatModelName = cfg.Model
	}
	if chatModelName == "" {
		return nil, fmt.Errorf("ollama chat model is required (set ollama_model)")
	}

	// Embedding model
	embedModelName := cfg.OllamaEmbed
	if embedModelName == "" {
		embedModelName = cfg.EmbedModel
	}
	if embedModelName == "" {
		embedModelName = chatModelName // Use chat model if not specified
	}

	// Create chat client
	chatClient, err := ollama.New(
		ollama.WithServerURL(baseURL),
		ollama.WithModel(chatModelName),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create Ollama chat client: %w", err)
	}

	// Create embedding client (separate if different model)
	var embedClient llms.Model
	if embedModelName != chatModelName {
		embedClient, err = ollama.New(
			ollama.WithServerURL(baseURL),
			ollama.WithModel(embedModelName),
		)
		if err != nil {
			return nil, fmt.Errorf("failed to create Ollama embedding client: %w", err)
		}
		log.Printf("🎯 Ollama: chat=%s, embed=%s (dual-model)", chatModelName, embedModelName)
	} else {
		embedClient = chatClient
		log.Printf("🎯 Ollama: model=%s (single-model)", chatModelName)
	}

	return &OllamaLLMProvider{
		chatModel:  chatClient,
		embedModel: embedClient,
		chatName:   chatModelName,
		embedName:  embedModelName,
		config:     cfg,
	}, nil
}

// Generate generates text using Ollama chat model
func (p *OllamaLLMProvider) Generate(ctx context.Context, prompt string, opts ...GenerateOption) (string, error) {
	lcOpts := p.convertOptions(opts...)
	return llms.GenerateFromSinglePrompt(ctx, p.chatModel, prompt, lcOpts...)
}

// GenerateStream generates streaming text using Ollama chat model
func (p *OllamaLLMProvider) GenerateStream(ctx context.Context, prompt string, opts ...GenerateOption) (<-chan string, <-chan error) {
	textChan := make(chan string)
	errChan := make(chan error, 1)

	go func() {
		defer close(textChan)
		defer close(errChan)

		streamFunc := func(ctx context.Context, chunk []byte) error {
			select {
			case textChan <- string(chunk):
				return nil
			case <-ctx.Done():
				return ctx.Err()
			}
		}

		lcOpts := p.convertOptions(opts...)
		lcOpts = append(lcOpts, llms.WithStreamingFunc(streamFunc))

		_, err := llms.GenerateFromSinglePrompt(ctx, p.chatModel, prompt, lcOpts...)
		if err != nil {
			errChan <- err
		}
	}()

	return textChan, errChan
}

// Embed generates embeddings using Ollama embedding model
func (p *OllamaLLMProvider) Embed(ctx context.Context, text string) ([]float64, error) {
	embedder, ok := p.embedModel.(interface {
		CreateEmbedding(ctx context.Context, texts []string) ([][]float32, error)
	})
	if !ok {
		return nil, fmt.Errorf("Ollama model does not support embeddings")
	}

	embeddings, err := embedder.CreateEmbedding(ctx, []string{text})
	if err != nil {
		return nil, fmt.Errorf("failed to create embedding: %w", err)
	}

	if len(embeddings) == 0 || len(embeddings[0]) == 0 {
		return nil, fmt.Errorf("empty embedding returned")
	}

	log.Printf("📊 Ollama embedding: dimension=%d", len(embeddings[0]))

	// Convert float32 to float64
	result := make([]float64, len(embeddings[0]))
	for i, v := range embeddings[0] {
		result[i] = float64(v)
	}

	return result, nil
}

// Name returns the provider name
func (p *OllamaLLMProvider) Name() string {
	return "ollama"
}

// convertOptions converts GenerateOption to langchaingo CallOption
func (p *OllamaLLMProvider) convertOptions(opts ...GenerateOption) []llms.CallOption {
	genOpts := &GenerateOptions{}
	for _, opt := range opts {
		opt(genOpts)
	}

	var lcOpts []llms.CallOption

	if genOpts.Temperature != 0 {
		lcOpts = append(lcOpts, llms.WithTemperature(genOpts.Temperature))
	}
	if genOpts.MaxTokens != 0 {
		lcOpts = append(lcOpts, llms.WithMaxTokens(genOpts.MaxTokens))
	}
	if genOpts.TopP != 0 {
		lcOpts = append(lcOpts, llms.WithTopP(genOpts.TopP))
	}
	if genOpts.TopK != 0 {
		lcOpts = append(lcOpts, llms.WithTopK(genOpts.TopK))
	}
	if len(genOpts.StopSequences) > 0 {
		lcOpts = append(lcOpts, llms.WithStopWords(genOpts.StopSequences))
	}

	// Apply config defaults
	if genOpts.Temperature == 0 && p.config.Temperature != 0 {
		lcOpts = append(lcOpts, llms.WithTemperature(p.config.Temperature))
	}
	if genOpts.MaxTokens == 0 && p.config.MaxTokens != 0 {
		lcOpts = append(lcOpts, llms.WithMaxTokens(p.config.MaxTokens))
	}

	return lcOpts
}

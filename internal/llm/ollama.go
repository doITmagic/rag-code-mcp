package llm

import (
	"context"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/doITmagic/rag-code-mcp/internal/config"
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
	cachedDim  uint64
	dimOnce    sync.Once
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

// GetEmbeddingDimension returns the dimension of the embedding model
func (p *OllamaLLMProvider) GetEmbeddingDimension() uint64 {
	// 1. Return cached dimension if already known
	if p.cachedDim > 0 {
		return p.cachedDim
	}

	// 2. Try hardcoded lookup for known common models
	dim := p.lookupHardcodedDimension()
	if dim > 0 {
		p.cachedDim = dim
		return dim
	}

	// 3. Fallback: Probe Ollama API dynamically
	p.dimOnce.Do(func() {
		log.Printf("🔍 Probing Ollama API for embedding dimension of model '%s'...", p.embedName)
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		// Generate a dummy embedding to check its length
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

func (p *OllamaLLMProvider) lookupHardcodedDimension() uint64 {
	// Reference: https://ollama.com/library
	switch p.embedName {
	case "mxbai-embed-large":
		return 1024
	case "nomic-embed-text", "nomic-embed-text:latest", "nomic-embed-text-v1.5":
		return 768
	case "nomic-embed-text-v2-moe", "nomic-embed-text-v2-moe:latest":
		return 768
	case "all-minilm":
		return 384
	case "bge-m3":
		return 1024
	case "bge-small-en-v1.5":
		return 384
	case "phi3", "phi3:medium", "phi3:14b", "phi3:latest":
		return 3072
	case "phi3:mini", "phi3:4b":
		return 3072
	case "qwen2.5-coder", "qwen2.5-coder:latest", "qwen2.5-coder:7b", "qwen2.5-coder:14b", "qwen2.5-coder:32b":
		return 3584
	case "qwen2.5-coder:1.5b", "qwen2.5-coder:0.5b":
		return 1536
	case "qwen3-embedding", "qwen3-embedding:0.6b", "qwen3-embedding:latest":
		return 1024
	case "qwen3-embedding:4b":
		return 2560
	case "qwen3-embedding:8b":
		return 4096
	case "deepseek-coder", "deepseek-coder:6.7b", "deepseek-coder-v2":
		return 4096
	case "deepseek-coder:1.3b":
		return 2048
	case "codellama", "codellama:7b", "codellama:13b":
		return 4096
	case "starcoder2:3b":
		return 3072
	case "starcoder2:7b", "starcoder2:15b":
		return 6144
	case "llama3", "llama3:8b":
		return 4096
	case "mistral", "mistral:7b":
		return 4096
	case "granite3.1-dense:8b":
		return 4096
	case "snowflake-arctic-embed", "snowflake-arctic-embed:latest":
		return 1024
	case "snowflake-arctic-embed:m":
		return 768
	case "snowflake-arctic-embed:xs":
		return 384
	case "bge-large-en-v1.5":
		return 1024
	case "jina-embeddings-v2-base-en":
		return 768
	default:
		return 0 // Unknown
	}
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

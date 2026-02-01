package llm

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/doITmagic/rag-code-mcp/internal/config"
)

func TestGenerateOptionsHelpers(t *testing.T) {
	opts := &GenerateOptions{}

	WithTemperature(0.7)(opts)
	WithMaxTokens(128)(opts)
	WithStopWords([]string{"foo", "bar"})(opts)

	if opts.Temperature != 0.7 {
		t.Errorf("expected temperature 0.7, got %v", opts.Temperature)
	}
	if opts.MaxTokens != 128 {
		t.Errorf("expected max tokens 128, got %v", opts.MaxTokens)
	}
	if !reflect.DeepEqual(opts.StopWords, []string{"foo", "bar"}) {
		t.Errorf("unexpected stop words: %#v", opts.StopWords)
	}
}

func TestNewProvider_UnknownProvider(t *testing.T) {
	cfg := &config.LLMConfig{Provider: "unknown"}

	p, err := NewProvider(cfg)
	if err == nil {
		t.Fatalf("expected error for unknown provider, got nil")
	}
	if !strings.Contains(err.Error(), "unknown provider") {
		t.Errorf("unexpected error: %v", err)
	}
	if p != nil {
		t.Errorf("expected nil provider on error, got %#v", p)
	}
}

func TestNewProvider_OllamaMissingModel(t *testing.T) {
	cfg := &config.LLMConfig{Provider: "ollama"}

	p, err := NewProvider(cfg)
	if err == nil {
		t.Fatalf("expected error when ollama model is missing, got nil")
	}
	if !strings.Contains(err.Error(), "ollama chat model is required") {
		t.Errorf("unexpected error: %v", err)
	}
	if p != nil {
		t.Errorf("expected nil provider on error, got %#v", p)
	}
}

func TestNewProvider_DefaultOllama(t *testing.T) {
	cfg := &config.LLMConfig{
		Provider:      "", // implicit ollama
		OllamaModel:   "dummy-model",
		OllamaBaseURL: "http://localhost:11434",
	}

	p, err := NewProvider(cfg)
	if err != nil {
		t.Fatalf("expected provider, got error: %v", err)
	}
	if p == nil {
		t.Fatalf("expected non-nil provider")
	}
	if p.Name() != "ollama" {
		t.Errorf("expected provider name 'ollama', got %q", p.Name())
	}
}

type fakeProvider struct {
	generateResult string
	generateErr    error
	generateCalls  int

	embedResult []float64
	embedErr    error
	embedCalls  int

	name string
}

func (f *fakeProvider) Generate(ctx context.Context, prompt string, opts ...GenerateOption) (string, error) {
	f.generateCalls++
	return f.generateResult, f.generateErr
}

func (f *fakeProvider) GenerateStream(ctx context.Context, prompt string, opts ...GenerateOption) (<-chan string, <-chan error) {
	out := make(chan string)
	errCh := make(chan error, 1)

	go func() {
		defer close(out)
		defer close(errCh)

		if f.generateErr != nil {
			errCh <- f.generateErr
			return
		}
		if f.generateResult != "" {
			out <- f.generateResult
		}
	}()

	return out, errCh
}

func (f *fakeProvider) Embed(ctx context.Context, text string) ([]float64, error) {
	f.embedCalls++
	return f.embedResult, f.embedErr
}

func (f *fakeProvider) Name() string {
	if f.name != "" {
		return f.name
	}
	return "fake"
}

// Ensure fakeProvider implements Provider
var _ Provider = (*fakeProvider)(nil)

func TestRetryableProvider_Success(t *testing.T) {
	base := &fakeProvider{
		generateResult: "ok",
		embedResult:    []float64{1, 2, 3},
	}

	r := NewRetryableProvider(base, 3, 2*time.Second)
	ctx := context.Background()

	got, err := r.Generate(ctx, "prompt")
	if err != nil {
		t.Fatalf("Generate returned error: %v", err)
	}
	if got != "ok" {
		t.Errorf("expected 'ok', got %q", got)
	}
	if base.generateCalls != 1 {
		t.Errorf("expected 1 generate call, got %d", base.generateCalls)
	}

	emb, err := r.Embed(ctx, "text")
	if err != nil {
		t.Fatalf("Embed returned error: %v", err)
	}
	if !reflect.DeepEqual(emb, []float64{1, 2, 3}) {
		t.Errorf("unexpected embedding: %#v", emb)
	}
	if base.embedCalls != 1 {
		t.Errorf("expected 1 embed call, got %d", base.embedCalls)
	}

	if r.Name() != base.Name() {
		t.Errorf("expected Name to be forwarded, got %q", r.Name())
	}

	// Basic sanity check for streaming delegation
	stream, errs := r.GenerateStream(ctx, "prompt")
	select {
	case v, ok := <-stream:
		if ok && v != "ok" {
			t.Errorf("expected streamed 'ok', got %q", v)
		}
	case err := <-errs:
		if err != nil {
			t.Errorf("unexpected error from stream: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("timeout waiting for stream")
	}
}

func TestRetryableProvider_ErrorNoRetry(t *testing.T) {
	base := &fakeProvider{
		generateErr: errors.New("boom"),
	}

	// maxRetries = 1 -> no retry is performed, but the Retry utility is used
	r := NewRetryableProvider(base, 1, time.Second)
	ctx := context.Background()

	_, err := r.Generate(ctx, "prompt")
	if err == nil {
		t.Fatalf("expected error from Generate, got nil")
	}
	if !strings.Contains(err.Error(), "failed after 1 attempts") {
		t.Errorf("unexpected error: %v", err)
	}
	if base.generateCalls != 1 {
		t.Errorf("expected 1 generate call, got %d", base.generateCalls)
	}
}

type closableFakeProvider struct {
	fakeProvider
	closed bool
}

func (c *closableFakeProvider) Close() error {
	c.closed = true
	return nil
}

func TestRetryableProvider_CloseDelegates(t *testing.T) {
	base := &closableFakeProvider{}
	r := NewRetryableProvider(base, 1, time.Second)

	if err := r.Close(); err != nil {
		t.Fatalf("expected nil error from Close, got %v", err)
	}
	if !base.closed {
		t.Errorf("expected underlying provider Close to be called")
	}
}

func TestRetryableProvider_CloseNoCloser(t *testing.T) {
	base := &fakeProvider{}
	r := NewRetryableProvider(base, 1, time.Second)

	if err := r.Close(); err != nil {
		t.Fatalf("expected nil error from Close, got %v", err)
	}
}

package indexer

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/doITmagic/rag-code-mcp/internal/config"
	"github.com/doITmagic/rag-code-mcp/pkg/llm"
	"github.com/doITmagic/rag-code-mcp/pkg/parser"
)

// The circuit breaker must probe the Ollama set in llm.ollama_base_url. A
// probe of the default localhost:11434 aborted the remaining embeds while the
// configured Ollama was healthy again.
func TestIndexItems_CircuitBreakerPingsConfiguredOllama(t *testing.T) {
	var heartbeats atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodHead && r.URL.Path == "/":
			heartbeats.Add(1)
		case r.URL.Path == "/api/ps":
			_, _ = w.Write([]byte(`{"models":[{"name":"embed-model"}]}`))
		case r.URL.Path == "/api/embed":
			// Embeds fail until the breaker probes this server.
			if heartbeats.Load() == 0 {
				http.Error(w, `{"error":"transient"}`, http.StatusInternalServerError)
				return
			}
			var req struct {
				Input []string `json:"input"`
			}
			_ = json.NewDecoder(r.Body).Decode(&req)
			vectors := make([][]float32, len(req.Input))
			for i := range vectors {
				vectors[i] = []float32{1, 2, 3}
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"model": "embed-model", "embeddings": vectors})
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	provider, err := llm.NewOllamaLLMProvider(config.LLMConfig{OllamaBaseURL: srv.URL, OllamaEmbed: "embed-model"})
	if err != nil {
		t.Fatal(err)
	}
	store := &mockStore{}
	svc := NewService(provider, store)

	symbols := make([]parser.Symbol, 3*svc.embedBatchSize)
	for i := range symbols {
		symbols[i] = parser.Symbol{Name: fmt.Sprintf("Sym%d", i), Content: "content", FilePath: "a.go", StartLine: i}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	_ = svc.IndexItems(ctx, "breaker-url", symbols)

	if heartbeats.Load() == 0 {
		t.Fatalf("circuit breaker never probed the configured Ollama at %s", srv.URL)
	}
	if got := len(store.upsertPoints); got != svc.embedBatchSize {
		t.Fatalf("stored %d points after the configured Ollama recovered, want %d", got, svc.embedBatchSize)
	}
}

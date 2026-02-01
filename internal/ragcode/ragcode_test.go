package ragcode

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/doITmagic/rag-code-mcp/internal/config"
	"github.com/doITmagic/rag-code-mcp/internal/llm"
	"github.com/doITmagic/rag-code-mcp/internal/storage"
)

func TestRagCodeIndexAndSearch(t *testing.T) {
	t.Skip("Skipping integration test - requires Ollama and Qdrant")

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	// Load config
	cfg, err := config.Load("../../config.yaml")
	if err != nil {
		t.Fatalf("Failed to load config: %v", err)
	}

	// Create embedding provider
	provider, err := llm.NewProvider(&cfg.LLM)
	if err != nil {
		t.Fatalf("Failed to create provider: %v", err)
	}

	// Probe embedding dimension
	dimProbe, err := provider.Embed(ctx, "test")
	if err != nil {
		t.Fatalf("Failed to probe embedding: %v", err)
	}
	dim := len(dimProbe)
	t.Logf("✓ Embedding dimension: %d", dim)

	// Create Qdrant client
	collection := "ragcode-test-" + fmt.Sprintf("%d", time.Now().Unix())
	qdrantClient, err := storage.NewQdrantClient(storage.QdrantConfig{
		URL:        cfg.Storage.VectorDB.URL,
		APIKey:     cfg.Storage.VectorDB.APIKey,
		Collection: collection,
	})
	if err != nil {
		t.Fatalf("Failed to create qdrant client: %v", err)
	}
	defer qdrantClient.Close()

	// Create collection
	if err := qdrantClient.CreateCollection(ctx, collection, dim); err != nil {
		t.Fatalf("Failed to create collection: %v", err)
	}
	t.Logf("✓ Collection '%s' created", collection)

	// Create long-term memory
	ltm := storage.NewQdrantLongTermMemory(qdrantClient)

	// Create analyzer and indexer
	mgr := NewAnalyzerManager()
	analyzer := mgr.CodeAnalyzerForProjectType("go")
	if analyzer == nil {
		t.Fatal("Failed to create code analyzer for Go")
	}
	indexer := NewIndexer(analyzer, provider, ltm)

	// Index internal/workspace directory
	paths := []string{"../../internal/workspace"}
	t.Logf("Indexing paths: %v", paths)
	count, err := indexer.IndexPaths(ctx, paths, "test")
	if err != nil {
		t.Fatalf("Indexing failed: %v", err)
	}
	t.Logf("✓ Indexed %d code chunks", count)

	if count == 0 {
		t.Fatal("Expected to index at least some chunks")
	}

	// Wait a bit for Qdrant to index
	time.Sleep(2 * time.Second)

	// Search for functions/methods
	t.Log("\n=== Searching for 'workspace detection' ===")
	queryEmbed, err := provider.Embed(ctx, "workspace detection implementation")
	if err != nil {
		t.Fatalf("Failed to embed query: %v", err)
	}

	results, err := qdrantClient.Search(ctx, queryEmbed, 5)
	if err != nil {
		t.Fatalf("Search failed: %v", err)
	}

	t.Logf("Found %d results:", len(results))
	for i, res := range results {
		name := "unknown"
		if n, ok := res.Payload["name"].(string); ok {
			name = n
		}
		typ := "unknown"
		if tp, ok := res.Payload["type"].(string); ok {
			typ = tp
		}
		sig := ""
		if s, ok := res.Payload["signature"].(string); ok {
			sig = s
		}
		t.Logf("%d. [%s] %s - Score: %.3f", i+1, typ, name, res.Score)
		if sig != "" {
			t.Logf("   Signature: %s", sig)
		}
	}

	// Search for sentiment
	t.Log("\n=== Searching for 'collection management' ===")
	queryEmbed2, err := provider.Embed(ctx, "collection name generation")
	if err != nil {
		t.Fatalf("Failed to embed query: %v", err)
	}

	results2, err := qdrantClient.Search(ctx, queryEmbed2, 5)
	if err != nil {
		t.Fatalf("Search failed: %v", err)
	}

	t.Logf("Found %d results:", len(results2))
	for i, res := range results2 {
		name := "unknown"
		if n, ok := res.Payload["name"].(string); ok {
			name = n
		}
		typ := "unknown"
		if tp, ok := res.Payload["type"].(string); ok {
			typ = tp
		}
		pkg := ""
		if p, ok := res.Payload["package"].(string); ok {
			pkg = p
		}
		t.Logf("%d. [%s] %s.%s - Score: %.3f", i+1, typ, pkg, name, res.Score)
	}

	// Test specific function/method queries WITH LLM RESPONSES
	t.Log("\n=== Testing Code RAG with LLM Responses ===")

	// Query 1: Ask about workspace detection
	question1 := "How do I detect a workspace from a file path?"
	t.Logf("\n1. Question: '%s'", question1)
	q1, err := provider.Embed(ctx, question1)
	if err != nil {
		t.Fatalf("Failed to embed query: %v", err)
	}
	r1, err := qdrantClient.Search(ctx, q1, 3)
	if err != nil {
		t.Fatalf("Search failed: %v", err)
	}

	// Build context from search results
	var context1 strings.Builder
	context1.WriteString("Relevant code documentation:\n\n")
	for i, res := range r1 {
		name := res.Payload["name"].(string)
		typ := res.Payload["type"].(string)
		sig := res.Payload["signature"].(string)
		pkg := res.Payload["package"].(string)

		t.Logf("   Retrieved [%s] %s.%s - Score: %.3f", typ, pkg, name, res.Score)

		context1.WriteString(fmt.Sprintf("%d. %s (type: %s)\n", i+1, name, typ))
		if sig != "" {
			context1.WriteString(fmt.Sprintf("   Signature: %s\n", sig))
		}
		context1.WriteString("\n")
	}

	// Generate LLM response
	prompt1 := fmt.Sprintf(`Based on the following code documentation, answer the question.

%s

Question: %s

Provide a clear, concise answer based on the code documentation above.`, context1.String(), question1)

	t.Log("\n   🤖 Generating LLM response...")
	answer1, err := provider.Generate(ctx, prompt1)
	if err != nil {
		t.Logf("   ⚠️  LLM generation failed: %v", err)
	} else {
		t.Log("\n   📝 LLM Answer:")
		t.Logf("   %s", strings.TrimSpace(answer1))
	}

	// Query 2: Markdown indexing
	question2 := "How can I index markdown documentation files in this codebase?"
	t.Logf("\n2. Question: '%s'", question2)
	q2, err := provider.Embed(ctx, question2)
	if err != nil {
		t.Fatalf("Failed to embed query: %v", err)
	}
	r2, err := qdrantClient.Search(ctx, q2, 3)
	if err != nil {
		t.Fatalf("Search failed: %v", err)
	}

	var context2 strings.Builder
	context2.WriteString("Relevant code documentation:\n\n")
	for i, res := range r2 {
		name := res.Payload["name"].(string)
		typ := res.Payload["type"].(string)
		sig := res.Payload["signature"].(string)
		pkg := res.Payload["package"].(string)

		t.Logf("   Retrieved [%s] %s.%s - Score: %.3f", typ, pkg, name, res.Score)
		context2.WriteString(fmt.Sprintf("%d. %s (type: %s, package: %s)\n", i+1, name, typ, pkg))
		if sig != "" {
			context2.WriteString(fmt.Sprintf("   Signature: %s\n", sig))
		}
		context2.WriteString("\n")
	}

	prompt2 := fmt.Sprintf(`Based on the following code documentation, answer the question.

%s

Question: %s

Provide a clear, concise answer.`, context2.String(), question2)

	t.Log("\n   🤖 Generating LLM response...")
	answer2, err := provider.Generate(ctx, prompt2)
	if err != nil {
		t.Logf("   ⚠️  LLM generation failed: %v", err)
	} else {
		t.Log("\n   📝 LLM Answer:")
		t.Logf("   %s", strings.TrimSpace(answer2))
	}

	// Query 3: Collection management
	question3 := "What component should I use to manage workspace collections?"
	t.Logf("\n3. Question: '%s'", question3)
	q3, err := provider.Embed(ctx, question3)
	if err != nil {
		t.Fatalf("Failed to embed query: %v", err)
	}
	r3, err := qdrantClient.Search(ctx, q3, 3)
	if err != nil {
		t.Fatalf("Search failed: %v", err)
	}

	var context3 strings.Builder
	context3.WriteString("Relevant code documentation:\n\n")
	for i, res := range r3 {
		name := res.Payload["name"].(string)
		typ := res.Payload["type"].(string)
		sig := res.Payload["signature"].(string)

		t.Logf("   Retrieved [%s] %s - Score: %.3f", typ, name, res.Score)
		context3.WriteString(fmt.Sprintf("%d. %s (type: %s)\n", i+1, name, typ))
		if sig != "" {
			context3.WriteString(fmt.Sprintf("   Signature: %s\n", sig))
		}
		context3.WriteString("\n")
	}

	prompt3 := fmt.Sprintf(`Based on the following code documentation, answer the question.

%s

Question: %s

Provide a clear, concise answer.`, context3.String(), question3)

	t.Log("\n   🤖 Generating LLM response...")
	answer3, err := provider.Generate(ctx, prompt3)
	if err != nil {
		t.Logf("   ⚠️  LLM generation failed: %v", err)
	} else {
		t.Log("\n   📝 LLM Answer:")
		t.Logf("   %s", strings.TrimSpace(answer3))
	}

	t.Log("\n✓ Test complete - RagCode successfully indexed and can retrieve functions/methods semantically")
}

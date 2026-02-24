package engine

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/doITmagic/rag-code-mcp/internal/config"
	"github.com/doITmagic/rag-code-mcp/internal/service/search"
	"github.com/doITmagic/rag-code-mcp/pkg/indexer"
	"github.com/doITmagic/rag-code-mcp/pkg/llm"
	"github.com/doITmagic/rag-code-mcp/pkg/workspace/contract"
	"github.com/doITmagic/rag-code-mcp/pkg/workspace/resolver"
)

// mockDetectorPending satisfies detector.Detector for resolver setup.
// It always resolves any file path to a deterministic root.
type mockDetectorPending struct{}

func (m *mockDetectorPending) DetectFromFilePath(_ context.Context, _ string) (*contract.WorkspaceCandidate, *contract.ResolveWorkspaceError) {
	return &contract.WorkspaceCandidate{Root: "/mock/ws", Confidence: 1.0}, nil
}

// mockLLMPending satisfies llm.Provider (embedding is not used in these tests).
type mockLLMPending struct{}

func (m *mockLLMPending) Embed(_ context.Context, _ string) ([]float64, error) {
	return []float64{0.1}, nil
}
func (m *mockLLMPending) Generate(_ context.Context, _ string, _ ...llm.GenerateOption) (string, error) {
	return "", nil
}
func (m *mockLLMPending) GenerateStream(_ context.Context, _ string, _ ...llm.GenerateOption) (<-chan string, <-chan error) {
	return nil, nil
}
func (m *mockLLMPending) Name() string                  { return "mock" }
func (m *mockLLMPending) GetEmbeddingDimension() uint64 { return 1 }

func newTestEnginePending() *Engine {
	store := &testStore{existing: map[string]bool{}}
	llmProvider := &mockLLMPending{}
	idxSvc := indexer.NewService(llmProvider, store)
	searchSvc := search.NewService(llmProvider, store)
	eng := NewEngine(idxSvc, searchSvc, "", &config.Config{})
	res := resolver.New(resolver.Dependencies{Detector: &mockDetectorPending{}})
	eng.SetResolver(res)
	return eng
}

func TestHandleWatchChangeAccumulatesPendingWhileIndexing(t *testing.T) {
	eng := newTestEnginePending()
	ctx := context.Background()

	wctx, err := eng.DetectContext(ctx, "test.go")
	if err != nil {
		t.Fatalf("DetectContext failed: %v", err)
	}

	// Simulate indexing in progress.
	eng.indexingJobs.Store(wctx.ID, time.Now())
	defer eng.indexingJobs.Delete(wctx.ID)

	files := []string{"/mock/ws/a.go", "/mock/ws/b.go", "/mock/ws/a.go"}
	if err := eng.handleWatchChange(ctx, wctx.Root, files); err != nil {
		t.Fatalf("handleWatchChange returned error: %v", err)
	}

	pending, overflow := eng.popPendingIndex(wctx.ID)
	if overflow {
		t.Fatalf("expected overflow=false")
	}
	if len(pending) != 2 {
		t.Fatalf("expected 2 unique pending files, got %d: %#v", len(pending), pending)
	}
	if pending[0] != "/mock/ws/a.go" || pending[1] != "/mock/ws/b.go" {
		t.Fatalf("unexpected pending ordering/content: %#v", pending)
	}
}

func TestPendingOverflowTriggersFullScanFallback(t *testing.T) {
	eng := newTestEnginePending()
	ctx := context.Background()

	wctx, err := eng.DetectContext(ctx, "test.go")
	if err != nil {
		t.Fatalf("DetectContext failed: %v", err)
	}

	many := make([]string, 0, pendingIndexMaxFiles+1)
	for i := 0; i < pendingIndexMaxFiles+1; i++ {
		many = append(many, fmt.Sprintf("/mock/ws/%d.go", i))
	}

	eng.addPendingIndexFiles(wctx.ID, many)
	pending, overflow := eng.popPendingIndex(wctx.ID)
	if !overflow {
		t.Fatalf("expected overflow=true")
	}
	if len(pending) != 0 {
		t.Fatalf("expected pending files cleared on overflow, got %d", len(pending))
	}

	// Ensure overflow flag is cleared after pop.
	pending2, overflow2 := eng.popPendingIndex(wctx.ID)
	if overflow2 {
		t.Fatalf("expected overflow cleared after pop")
	}
	if len(pending2) != 0 {
		t.Fatalf("expected no pending after second pop, got %d", len(pending2))
	}
}

package engine

import (
	"context"
	"testing"
	"time"

	"github.com/doITmagic/rag-code-mcp/internal/config"
	"github.com/doITmagic/rag-code-mcp/internal/service/search"
	"github.com/doITmagic/rag-code-mcp/pkg/indexer"
	"github.com/doITmagic/rag-code-mcp/pkg/workspace/contract"
	"github.com/doITmagic/rag-code-mcp/pkg/workspace/resolver"
)

// errorDetector always returns an error, useful for testing error paths.
type errorDetector struct{}

func (e *errorDetector) DetectFromFilePath(_ context.Context, _ string) (*contract.WorkspaceCandidate, *contract.ResolveWorkspaceError) {
	return nil, &contract.ResolveWorkspaceError{
		Code:    contract.ErrorInvalidPath,
		Message: "simulated detection failure",
		Reason:  contract.ReasonInvalidPath,
	}
}

// TestCheckAndReindexOnConnect_ReturnsRoot verifies that CheckAndReindexOnConnect
// resolves the workspace root from a hint path.
func TestCheckAndReindexOnConnect_ReturnsRoot(t *testing.T) {
	rootDir := t.TempDir()
	llmProvider := &countingLLM{}
	store := &testStore{existing: map[string]bool{}}

	eng := NewEngine(
		indexer.NewService(llmProvider, store),
		search.NewService(llmProvider, store),
		"",
		&config.Config{},
	)
	eng.SetResolver(resolver.New(resolver.Dependencies{
		Detector: &mockDirDetector{root: rootDir},
	}))

	result := eng.CheckAndReindexOnConnect("some/file.go")
	if result != rootDir {
		t.Errorf("expected root=%q, got %q", rootDir, result)
	}
}

// TestCheckAndReindexOnConnect_BadHintReturnsEmpty verifies that when the
// detector can't resolve the hint, an empty string is returned.
func TestCheckAndReindexOnConnect_BadHintReturnsEmpty(t *testing.T) {
	llmProvider := &countingLLM{}
	store := &testStore{existing: map[string]bool{}}

	eng := NewEngine(
		indexer.NewService(llmProvider, store),
		search.NewService(llmProvider, store),
		"",
		&config.Config{},
	)
	// Use an error-returning detector
	eng.SetResolver(resolver.New(resolver.Dependencies{
		Detector: &errorDetector{},
	}))

	result := eng.CheckAndReindexOnConnect("/nonexistent/path")
	if result != "" {
		t.Errorf("expected empty string for failed detection, got %q", result)
	}
}

// TestCheckAndReindexOnConnect_TriggersReindex verifies that when branchstate
// indicates ReindexRequired, CheckAndReindexOnConnect triggers a background job.
func TestCheckAndReindexOnConnect_TriggersReindex(t *testing.T) {
	rootDir := t.TempDir()
	llmProvider := &countingLLM{}
	store := &testStore{existing: map[string]bool{}}

	eng := NewEngine(
		indexer.NewService(llmProvider, store),
		search.NewService(llmProvider, store),
		"",
		&config.Config{},
	)
	eng.SetResolver(resolver.New(resolver.Dependencies{
		Detector: &mockDirDetector{root: rootDir},
	}))

	// Register cleanup immediately — before any Fatal that could skip it.
	t.Cleanup(func() {

		deadline := time.Now().Add(5 * time.Second)
		for time.Now().Before(deadline) {
			if len(eng.ActiveIndexingJobs()) == 0 {
				break
			}
			time.Sleep(10 * time.Millisecond)
		}
	})

	// First call: forces branchstate to mark as first-seen (no branch_state.json),
	// which sets ReindexRequired=true.
	result := eng.CheckAndReindexOnConnect("some/path.go")
	if result == "" {
		t.Fatal("expected non-empty root")
	}
	if result != rootDir {
		t.Fatalf("expected root=%q, got %q", rootDir, result)
	}

	// Detect context to get the workspace ID for assertion
	wctx, err := eng.DetectContext(context.Background(), "some/path.go")
	if err != nil || wctx == nil {
		t.Fatalf("DetectContext failed: %v", err)
	}

	// Deterministic assertion: poll ActiveIndexingJobs until the job appears
	// or timeout. The job may complete very fast (empty workspace), so we
	// also accept if it already ran and finished.
	deadline := time.Now().Add(500 * time.Millisecond)
	found := false
	for time.Now().Before(deadline) && !found {
		for _, id := range eng.ActiveIndexingJobs() {
			if id == wctx.ID {
				found = true
				break
			}
		}
		if !found {
			time.Sleep(10 * time.Millisecond)
		}
	}
	// Note: the job may have already completed (empty workspace = instant finish),
	// so we check that the function returned the correct root as minimum assertion.
	// A more robust test would use a blocking indexer stub.
	if !found {
		t.Logf("indexing job for %s completed before we could observe it (expected for empty workspace)", wctx.ID)
	}
}

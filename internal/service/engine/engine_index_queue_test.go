package engine

import (
	"context"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/doITmagic/rag-code-mcp/internal/config"
	"github.com/doITmagic/rag-code-mcp/internal/service/search"
	"github.com/doITmagic/rag-code-mcp/pkg/indexer"
	"github.com/doITmagic/rag-code-mcp/pkg/llm"
	"github.com/doITmagic/rag-code-mcp/pkg/workspace/contract"
	"github.com/doITmagic/rag-code-mcp/pkg/workspace/resolver"
)

type exactRootDetector struct{}

func (*exactRootDetector) DetectFromFilePath(_ context.Context, path string) (*contract.WorkspaceCandidate, *contract.ResolveWorkspaceError) {
	root := path
	if info, err := os.Stat(path); err == nil && !info.IsDir() {
		root = filepath.Dir(path)
	}
	return &contract.WorkspaceCandidate{Root: root, Confidence: 1}, nil
}

type concurrencyLLM struct {
	active atomic.Int32
	max    atomic.Int32
}

func (p *concurrencyLLM) Embed(context.Context, string) ([]float64, error) {
	active := p.active.Add(1)
	for {
		maximum := p.max.Load()
		if active <= maximum || p.max.CompareAndSwap(maximum, active) {
			break
		}
	}
	time.Sleep(50 * time.Millisecond)
	p.active.Add(-1)
	return []float64{0.1, 0.2}, nil
}

func (*concurrencyLLM) Generate(context.Context, string, ...llm.GenerateOption) (string, error) {
	return "", nil
}
func (*concurrencyLLM) GenerateStream(context.Context, string, ...llm.GenerateOption) (<-chan string, <-chan error) {
	return nil, nil
}
func (*concurrencyLLM) Name() string                  { return "concurrency-test" }
func (*concurrencyLLM) GetEmbeddingDimension() uint64 { return 2 }

func TestWorkspaceIndexingIsSequential(t *testing.T) {
	provider := &concurrencyLLM{}
	store := &testStore{existing: map[string]bool{}}
	cfg := &config.Config{Workspace: config.WorkspaceConfig{AutoIndex: false}}
	registryPath := filepath.Join(t.TempDir(), "registry.json")
	eng := NewEngine(indexer.NewService(provider, store), search.NewService(provider, store), registryPath, cfg)
	eng.SetResolver(resolver.New(resolver.Dependencies{Detector: &exactRootDetector{}, Registry: eng.registry}))

	roots := []string{t.TempDir(), t.TempDir()}
	contexts := make([]*WorkspaceContext, 0, len(roots))
	for _, root := range roots {
		if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module test\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(root, "source.go")
		if err := os.WriteFile(path, []byte("package test\nfunc Work() {}\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		wctx, err := eng.DetectContext(context.Background(), path)
		if err != nil {
			t.Fatal(err)
		}
		contexts = append(contexts, wctx)
	}
	for _, wctx := range contexts {
		eng.StartIndexingAsync(wctx.Root, wctx.ID, nil, false)
	}

	deadline := time.Now().Add(5 * time.Second)
	for len(eng.ActiveIndexingJobs()) > 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if jobs := eng.ActiveIndexingJobs(); len(jobs) != 0 {
		t.Fatalf("indexing did not finish: %v", jobs)
	}
	if got := provider.max.Load(); got != 1 {
		t.Fatalf("concurrent embeddings = %d, want 1", got)
	}
}

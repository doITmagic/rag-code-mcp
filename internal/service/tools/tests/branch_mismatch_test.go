package tests

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/doITmagic/rag-code-mcp/internal/service/tools"
	"github.com/doITmagic/rag-code-mcp/pkg/storage"
	"github.com/doITmagic/rag-code-mcp/pkg/workspace/contract"
	"github.com/doITmagic/rag-code-mcp/pkg/workspace/resolver"
)

type branchChangedAnnotator struct{}

func (branchChangedAnnotator) Annotate(_ context.Context, _ string, resp *contract.ResolveWorkspaceResponse) *contract.ResolveWorkspaceError {
	resp.Branch = "feat-b"
	resp.MismatchRisk = "high"
	return nil
}

// Right after a branch change the collection may still hold the previous
// branch's code. An exact symbol hit must carry the same branch mismatch
// warning as a semantic one, not look like a clean success.
func TestExactSearchWarnsOnBranchMismatch(t *testing.T) {
	file := filepath.Join(t.TempDir(), "extractor.go")
	if err := os.WriteFile(file, []byte("package provider\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	store := &mockVectorStore{ExactSearchFunc: func(_ context.Context, _ string, _ map[string]interface{}, _ int) ([]storage.SearchResult, error) {
		return []storage.SearchResult{{Score: 1, Point: storage.Point{ID: "hook", Payload: map[string]interface{}{
			"name": "FeatBExtractorHook", "content": "func FeatBExtractorHook() {}", "file_path": file, "type": "function",
		}}}}, nil
	}}
	eng := setupTestEngine(store)
	eng.SetResolver(resolver.New(resolver.Dependencies{Detector: &mockDetector{}, BranchAnnotator: branchChangedAnnotator{}}))
	exact := true
	out, err := tools.NewSmartSearchTool(eng).Execute(context.Background(), tools.SmartSearchInput{Query: "FeatBExtractorHook", FilePath: file, ExactSymbol: &exact})
	if err != nil {
		t.Fatal(err)
	}
	var resp tools.ToolResponse
	if err := json.Unmarshal([]byte(out), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.Status != "success" || !strings.Contains(resp.Warning, "mismatch") {
		t.Fatalf("status=%q warning=%q, want success with a branch mismatch warning", resp.Status, resp.Warning)
	}
}

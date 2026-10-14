package tools

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/doITmagic/rag-code-mcp/pkg/indexer"
)

func decode(t *testing.T, s string) ToolResponse {
	t.Helper()
	var r ToolResponse
	if err := json.Unmarshal([]byte(s), &r); err != nil {
		t.Fatalf("bad response %q: %v", s, err)
	}
	return r
}

// The home directory must be refused before the engine is touched: resolving
// it registers it, and registering a root absorbs (and wipes) every
// workspace beneath it. A nil engine proves the check comes first.
func TestIndexWorkspaceRefusesHomeBeforeResolving(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip(err)
	}
	tool := NewIndexWorkspaceTool(nil)
	for _, root := range []string{home, os.TempDir(), string(os.PathSeparator)} {
		out, err := tool.Execute(context.Background(), map[string]interface{}{"workspace_root": root})
		if err != nil {
			t.Fatal(err)
		}
		if r := decode(t, out); r.Status != "error" || !strings.Contains(r.Error, "Refusing") {
			t.Errorf("%s: want refusal, got %s %q", root, r.Status, r.Error)
		}
	}
}

func TestCallHierarchyRejectsUnknownDirection(t *testing.T) {
	tool := NewCallHierarchyTool(nil)
	out, err := tool.Execute(context.Background(), map[string]interface{}{"symbol_name": "X", "direction": "sideways"})
	if err != nil {
		t.Fatal(err)
	}
	if r := decode(t, out); r.Status != "error" || !strings.Contains(r.Error, "direction") {
		t.Fatalf("want direction error, got %s %q", r.Status, r.Error)
	}
}

// A response whose workspace came from the registry fallback carries the
// warning whichever tool built it.
func TestJSONAddsFallbackWarning(t *testing.T) {
	out, err := ToolResponse{Status: "success", Context: ContextMetadata{WorkspaceRoot: "/w", DetectionSource: "registry_fallback"}}.JSON()
	if err != nil {
		t.Fatal(err)
	}
	if r := decode(t, out); !strings.Contains(r.Warning, "inferred from history") {
		t.Fatalf("warning = %q", r.Warning)
	}
	out, _ = ToolResponse{Status: "success", Warning: "Branch mismatch risk: high", Context: ContextMetadata{DetectionSource: "registry_fallback"}}.JSON()
	if r := decode(t, out); r.Warning != "Branch mismatch risk: high" {
		t.Fatalf("existing warning overwritten: %q", r.Warning)
	}
	out, _ = ToolResponse{Status: "success", Context: ContextMetadata{DetectionSource: "file_path"}}.JSON()
	if r := decode(t, out); r.Warning != "" {
		t.Fatalf("unexpected warning: %q", r.Warning)
	}
}

func TestJSONOmitsCompletedIndexingProgress(t *testing.T) {
	completed := &indexer.IndexStatus{StartedAt: "start", EndedAt: "end"}
	out, err := ToolResponse{Status: "success", Context: ContextMetadata{IndexingStatus: completed}}.JSON()
	if err != nil {
		t.Fatal(err)
	}
	if response := decode(t, out); response.Context.IndexingStatus != nil {
		t.Fatalf("completed indexing progress leaked: %+v", response.Context.IndexingStatus)
	}

	active := &indexer.IndexStatus{StartedAt: "start"}
	out, err = ToolResponse{Status: "success", Context: ContextMetadata{IndexingStatus: active}}.JSON()
	if err != nil {
		t.Fatal(err)
	}
	if response := decode(t, out); response.Context.IndexingStatus == nil {
		t.Fatal("active indexing progress was omitted")
	}
}

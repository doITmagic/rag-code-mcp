package tools

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/doITmagic/rag-code-mcp/pkg/indexer"
	"github.com/doITmagic/rag-code-mcp/pkg/telemetry"
)

func TestSearchPresentationDoesNotInventConfidence(t *testing.T) {
	path := filepath.Join(t.TempDir(), "source.go")
	if err := os.WriteFile(path, []byte(strings.Repeat("x", 5000)), 0600); err != nil {
		t.Fatal(err)
	}
	for _, compact := range []bool{true, false} {
		response := ToolResponse{Status: "success"}
		serializeResults(&response, []mergedResult{{name: "Unrelated", filePath: path, score: 0.5, content: "func Unrelated() {}"}}, compact, false, "MissingSymbol", true)
		if strings.Contains(response.Message, "high-confidence") {
			t.Fatal(response.Message)
		}
		if response.Context.Telemetry == nil || response.Context.Telemetry.EfficiencyPct >= 100 {
			t.Fatalf("compact=%v savings=%+v", compact, response.Context.Telemetry)
		}
	}
}

func TestEmptySearchIsCounted(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, ".ragcode"), 0700); err != nil {
		t.Fatal(err)
	}
	recordSearchMetric(searchMetadata{workspaceRoot: root}, "missing", nil, false, nil, time.Now())
	m := telemetry.ReadAggregatedMetrics(root)
	if m == nil || m.TotalSearches != 1 || m.SearchesWithResults != 0 {
		t.Fatalf("metrics=%+v", m)
	}
}

func TestSymbolQueryClassification(t *testing.T) {
	for _, q := range []string{"CalculateOspreyDiscount", "heron_helper", "IbexCart.add", "App\\A::validate"} {
		if !isSymbolQuery(q) {
			t.Errorf("not detected: %s", q)
		}
	}
	for _, q := range []string{"discount", "find total calculation", "how does A work?"} {
		if isSymbolQuery(q) {
			t.Errorf("semantic query treated as exact: %s", q)
		}
	}
}

func TestDefaultScoreFilterRejectsWeakNeighbours(t *testing.T) {
	got := applyScoreFilter([]mergedResult{{score: 0.705}, {score: 0.55}}, 0, true)
	if len(got) != 0 {
		t.Fatalf("weak semantic neighbours survived: %+v", got)
	}

	got = applyScoreFilter([]mergedResult{{score: 0.89}, {score: 0.73}, {score: 0.71}}, 0, true)
	if len(got) != 2 {
		t.Fatalf("relevant results = %d, want 2", len(got))
	}
}

func TestExplicitScoreOverridesDefaultFloor(t *testing.T) {
	got := applyScoreFilter([]mergedResult{{score: 0.60}}, 0.50, true)
	if len(got) != 1 {
		t.Fatalf("explicit min_score should be authoritative: %+v", got)
	}
}

func TestDefaultScoreFilterDoesNotApplyToFallback(t *testing.T) {
	got := applyScoreFilter([]mergedResult{{score: 0.30}}, 0, false)
	if len(got) != 1 {
		t.Fatalf("fallback result was filtered: %+v", got)
	}
}

func TestNoResultsReportsPartialIndex(t *testing.T) {
	root := t.TempDir()
	indexer.SaveIndexStatus(root, &indexer.IndexStatus{StartedAt: "2026-09-17T00:00:00Z"})

	out, err := noResultsResponse("MissingSymbol", searchMetadata{workspaceRoot: root})
	if err != nil {
		t.Fatal(err)
	}
	response := decode(t, out)
	if response.Status != "indexing_in_progress" || response.Context.IndexingStatus == nil {
		t.Fatalf("response=%+v", response)
	}
}

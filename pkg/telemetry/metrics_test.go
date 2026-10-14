package telemetry

import (
	"os"
	"path/filepath"
	"testing"
)

func TestAppendAndRead(t *testing.T) {
	tmp := t.TempDir()
	// AppendSearchMetric writes only into an existing .ragcode dir — it never
	// creates one, so an indexed workspace has to be simulated here.
	if err := os.MkdirAll(filepath.Join(tmp, ".ragcode"), 0o755); err != nil {
		t.Fatal(err)
	}

	AppendSearchMetric(tmp, SearchMetric{
		Tool: "rag_search", Query: "auth", ResultCount: 5,
		TopScore: 0.85, Source: "vector", BytesSaved: 14000, TokensSaved: 3500, ResponseMs: 120,
	})
	AppendSearchMetric(tmp, SearchMetric{
		Tool: "rag_search", Query: "config", ResultCount: 0,
		TopScore: 0, Source: "fallback", BytesSaved: 0, TokensSaved: 0, ResponseMs: 80,
	})
	AppendSearchMetric(tmp, SearchMetric{
		Tool: "rag_find_usages", Query: "MyFunc", ResultCount: 3,
		TopScore: 0.92, Source: "vector", BytesSaved: 5000, TokensSaved: 1250, ResponseMs: 60,
	})

	// Verify file exists
	path := filepath.Join(tmp, ".ragcode", metricsFile)
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("metrics file not created: %v", err)
	}

	agg := ReadAggregatedMetrics(tmp)
	if agg == nil {
		t.Fatal("expected aggregated metrics, got nil")
	}

	if agg.TotalSearches != 3 {
		t.Errorf("TotalSearches=%d, want 3", agg.TotalSearches)
	}
	if agg.SearchesWithResults != 2 {
		t.Errorf("SearchesWithResults=%d, want 2", agg.SearchesWithResults)
	}
	if agg.FallbackSearches != 1 {
		t.Errorf("FallbackSearches=%d, want 1", agg.FallbackSearches)
	}
	if agg.VectorSearches != 2 {
		t.Errorf("VectorSearches=%d, want 2", agg.VectorSearches)
	}
	if agg.TotalBytesSaved != 19000 {
		t.Errorf("TotalBytesSaved=%d, want 19000", agg.TotalBytesSaved)
	}
	if agg.TotalTokensSaved != 4750 {
		t.Errorf("TotalTokensSaved=%d, want 4750", agg.TotalTokensSaved)
	}
}

func TestReadEmptyWorkspace(t *testing.T) {
	tmp := t.TempDir()
	agg := ReadAggregatedMetrics(tmp)
	if agg != nil {
		t.Error("expected nil for workspace without metrics")
	}
}

func TestReadEmptyString(t *testing.T) {
	agg := ReadAggregatedMetrics("")
	if agg != nil {
		t.Error("expected nil for empty workspace")
	}
}

func TestAppendEmptyWorkspace(t *testing.T) {
	// Should not panic
	AppendSearchMetric("", SearchMetric{Tool: "test"})
}

func TestAppendNeverCreatesRagcodeDir(t *testing.T) {
	tmp := t.TempDir()

	AppendSearchMetric(tmp, SearchMetric{Tool: "rag_search", Query: "x"})

	if _, err := os.Stat(filepath.Join(tmp, ".ragcode")); !os.IsNotExist(err) {
		t.Fatal(".ragcode must not be created by a metrics write — it is a workspace marker")
	}
}

func TestAppendIgnoresRelativeRoot(t *testing.T) {
	AppendSearchMetric(".", SearchMetric{Tool: "rag_search", Query: "x"})

	if _, err := os.Stat(filepath.Join(".", ".ragcode")); !os.IsNotExist(err) {
		t.Fatal("a relative root resolves against the process cwd and must be refused")
	}
}

package telemetry

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
)

// AggregatedMetrics holds cumulative statistics from search_metrics.jsonl.
type AggregatedMetrics struct {
	TotalSearches       int     `json:"total_searches"`
	SearchesWithResults int     `json:"searches_with_results"`
	FallbackSearches    int     `json:"fallback_searches"`
	VectorSearches      int     `json:"vector_searches"`
	HybridSearches      int     `json:"hybrid_searches"`
	AvgTopScore         float32 `json:"avg_top_score"`
	TotalBytesSaved     int64   `json:"total_bytes_saved"`
	TotalTokensSaved    int64   `json:"total_tokens_saved"`
	AvgResponseMs       int64   `json:"avg_response_ms"`
}

// ReadAggregatedMetrics reads and aggregates all metrics from the JSONL file.
// Returns nil if no metrics file exists or on read errors.
func ReadAggregatedMetrics(workspaceRoot string) *AggregatedMetrics {
	if workspaceRoot == "" {
		return nil
	}
	path := filepath.Join(workspaceRoot, ".ragcode", metricsFile)
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()

	var agg AggregatedMetrics
	var scoreSum float32
	var msSum int64

	scanner := bufio.NewScanner(f)
	// Increase buffer to 1MB to handle long JSONL lines (large query strings, etc.)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	for scanner.Scan() {
		var m SearchMetric
		if json.Unmarshal(scanner.Bytes(), &m) != nil {
			continue
		}
		agg.TotalSearches++
		if m.ResultCount > 0 {
			agg.SearchesWithResults++
		}
		switch m.Source {
		case "fallback":
			agg.FallbackSearches++
		case "hybrid":
			agg.HybridSearches++
		default:
			agg.VectorSearches++
		}
		scoreSum += m.TopScore
		agg.TotalBytesSaved += m.BytesSaved
		agg.TotalTokensSaved += m.TokensSaved
		msSum += m.ResponseMs
	}

	if err := scanner.Err(); err != nil {
		return nil
	}

	if agg.TotalSearches == 0 {
		return nil
	}
	agg.AvgTopScore = scoreSum / float32(agg.TotalSearches)
	agg.AvgResponseMs = msSum / int64(agg.TotalSearches)
	return &agg
}

package telemetry

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
)

// AggregatedMetrics holds cumulative statistics from search_metrics.jsonl.
type AggregatedMetrics struct {
	TotalSearches      int     `json:"total_searches"`
	SearchesWithResults int    `json:"searches_with_results"`
	FallbackSearches   int     `json:"fallback_searches"`
	VectorSearches     int     `json:"vector_searches"`
	AvgTopScore        float32 `json:"avg_top_score"`
	TotalBytesSaved    int64   `json:"total_bytes_saved"`
	TotalTokensSaved   int64   `json:"total_tokens_saved"`
	AvgResponseMs      int64   `json:"avg_response_ms"`
}

// ReadAggregatedMetrics reads and aggregates all metrics from the JSONL file.
// Returns nil if no metrics file exists.
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
	for scanner.Scan() {
		var m SearchMetric
		if json.Unmarshal(scanner.Bytes(), &m) != nil {
			continue
		}
		agg.TotalSearches++
		if m.ResultCount > 0 {
			agg.SearchesWithResults++
		}
		if m.Source == "fallback" {
			agg.FallbackSearches++
		} else {
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

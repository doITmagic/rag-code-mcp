package telemetry

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// SearchMetric records a single tool invocation for cumulative analytics.
type SearchMetric struct {
	Timestamp   time.Time `json:"ts"`
	Tool        string    `json:"tool"`                   // "rag_search", "rag_find_usages", etc.
	Query       string    `json:"query,omitempty"`        // search query
	ResultCount int       `json:"result_count"`           // number of results returned
	TopScore    float32   `json:"top_score,omitempty"`    // score of best result
	Source      string    `json:"source,omitempty"`       // "vector", "fallback", "hybrid"
	BytesSaved  int64     `json:"bytes_saved,omitempty"`  // bytes avoided via RAG
	TokensSaved int64     `json:"tokens_saved,omitempty"` // estimated tokens saved
	ResponseMs  int64     `json:"response_ms,omitempty"`  // response time in milliseconds
}

const metricsFile = "search_metrics.jsonl"

// mu protects concurrent writes to the same metrics file.
var mu sync.Mutex

// AppendSearchMetric appends a single metric line to {workspaceRoot}/.ragcode/search_metrics.jsonl.
// Thread-safe via mutex. Fails silently (logs nothing) to avoid impacting tool response times.
func AppendSearchMetric(workspaceRoot string, m SearchMetric) {
	if workspaceRoot == "" {
		return
	}

	m.Timestamp = time.Now()

	line, err := json.Marshal(m)
	if err != nil {
		return
	}
	line = append(line, '\n')

	dir := filepath.Join(workspaceRoot, ".ragcode")
	path := filepath.Join(dir, metricsFile)

	mu.Lock()
	defer mu.Unlock()

	_ = os.MkdirAll(dir, 0o755)
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	defer f.Close()
	_, _ = f.Write(line)
}

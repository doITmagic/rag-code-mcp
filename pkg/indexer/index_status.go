package indexer

import (
	"encoding/json"
	"os"
	"path/filepath"

	"github.com/doITmagic/rag-code-mcp/internal/logger"
)

const indexStatusFile = "index_status.json"

// IndexStatus represents the current state of indexing for a workspace.
// Written by the indexer to {workspaceRoot}/.ragcode/index_status.json.
// Read by tools to include progress in MCP responses.
type IndexStatus struct {
	StartedAt string                `json:"started_at"`           // RFC3339
	EndedAt   string               `json:"ended_at,omitempty"`   // RFC3339
	Elapsed   string               `json:"elapsed,omitempty"`    // human-readable duration
	Error     string               `json:"error,omitempty"`
	Languages map[string]LangStatus `json:"languages,omitempty"`
}

// LangStatus holds indexing stats for a single language.
type LangStatus struct {
	OnDisk    int `json:"on_disk"`    // total files on disk for this language
	Changed   int `json:"changed"`   // files that need processing
	Processed int `json:"processed"` // files processed so far
}

// SaveIndexStatus writes the IndexStatus to {workspaceRoot}/.ragcode/index_status.json.
func SaveIndexStatus(workspaceRoot string, status *IndexStatus) {
	if workspaceRoot == "" || status == nil {
		return
	}
	dir := filepath.Join(workspaceRoot, ".ragcode")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		logger.Instance.Warn("index_status: cannot create .ragcode dir: %v", err)
		return
	}
	path := filepath.Join(dir, indexStatusFile)
	b, err := json.MarshalIndent(status, "", "  ")
	if err != nil {
		logger.Instance.Warn("index_status: marshal failed: %v", err)
		return
	}
	if err := os.WriteFile(path, b, 0o644); err != nil {
		logger.Instance.Warn("index_status: write failed for %s: %v", path, err)
	}
}

// LoadIndexStatus reads the IndexStatus from {workspaceRoot}/.ragcode/index_status.json.
// Returns nil if the file doesn't exist or can't be parsed.
func LoadIndexStatus(workspaceRoot string) *IndexStatus {
	path := filepath.Join(workspaceRoot, ".ragcode", indexStatusFile)
	b, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var s IndexStatus
	if err := json.Unmarshal(b, &s); err != nil {
		logger.Instance.Warn("index_status: parse failed for %s: %v", path, err)
		return nil
	}
	return &s
}

// GetLastInterruptedWorkspace checks a list of roots and picks the one 
// that is incomplete (StartedAt without EndedAt) with the most recent Start time.
func GetLastInterruptedWorkspace(roots []string) string {
	var bestRoot string
	var bestStartedAt string

	for _, root := range roots {
		status := LoadIndexStatus(root)
		if status == nil {
			continue
		}
		if status.EndedAt != "" {
			continue // Already finished
		}

		if status.StartedAt != "" && status.StartedAt > bestStartedAt {
			// Basic lexicographical comparison works for RFC3339 timestamps
			bestStartedAt = status.StartedAt
			bestRoot = root
		}
	}

	return bestRoot
}

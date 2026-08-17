package indexer

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"

	"github.com/doITmagic/rag-code-mcp/internal/logger"
)

const indexStatusFile = "index_status.json"

// IndexStatus represents the current state of indexing for a workspace.
// Written by the indexer to {workspaceRoot}/.ragcode/index_status.json.
// Read by tools to include progress in MCP responses.
type IndexStatus struct {
	StartedAt string                `json:"started_at"`         // RFC3339
	EndedAt   string                `json:"ended_at,omitempty"` // RFC3339
	Elapsed   string                `json:"elapsed,omitempty"`  // human-readable duration
	Error     string                `json:"error,omitempty"`
	Languages map[string]LangStatus `json:"languages,omitempty"`
}

// LangStatus holds indexing stats for a single language.
type LangStatus struct {
	OnDisk    int            `json:"on_disk"`             // total files on disk for this language
	Changed   int            `json:"-"`                   // internal: files that need processing (hidden from AI consumers)
	Processed int            `json:"processed"`           // files processed so far
	Breakdown map[string]int `json:"breakdown,omitempty"` // extension → count (e.g. ".ts": 37, ".js": 2)
}

// callerChain returns a compact caller stack (skipping skip frames) for debugging.
func callerChain(skip, depth int) string {
	pcs := make([]uintptr, depth)
	n := runtime.Callers(skip+1, pcs)
	if n == 0 {
		return "<unknown>"
	}
	frames := runtime.CallersFrames(pcs[:n])
	var parts []string
	for {
		frame, more := frames.Next()
		// Use short function name
		fn := frame.Function
		if idx := strings.LastIndex(fn, "/"); idx >= 0 {
			fn = fn[idx+1:]
		}
		parts = append(parts, fmt.Sprintf("%s:%d", fn, frame.Line))
		if !more {
			break
		}
	}
	return strings.Join(parts, " ← ")
}

// hasParentRagcode walks up the directory tree to check if a `.ragcode` folder exists
// up to 10 levels above the starting root.
func hasParentRagcode(root string) bool {
	dir := root
	if abs, err := filepath.Abs(dir); err == nil {
		dir = abs
	}
	for i := 0; i < 10; i++ {
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent

		ragcodePath := filepath.Join(dir, ".ragcode")
		if stat, err := os.Stat(ragcodePath); err == nil && stat.IsDir() {
			return true
		}
	}
	return false
}

// parentRagcodeCache caches hasParentRagcode results per workspace root.
// This avoids up to 10 os.Stat calls on every SaveIndexStatus invocation
// during indexing, where SaveIndexStatus is called very frequently.
var parentRagcodeCache sync.Map // map[string]bool

// ClearParentRagcodeCache should be called when workspace topology changes
// (e.g., after absorbing children or re-registering workspaces).
func ClearParentRagcodeCache() {
	parentRagcodeCache.Range(func(key, _ any) bool {
		parentRagcodeCache.Delete(key)
		return true
	})
}

// cachedHasParentRagcode returns hasParentRagcode result, using a per-root cache.
func cachedHasParentRagcode(root string) bool {
	abs := root
	if a, err := filepath.Abs(root); err == nil {
		abs = a
	}
	if cached, ok := parentRagcodeCache.Load(abs); ok {
		return cached.(bool)
	}
	result := hasParentRagcode(root)
	parentRagcodeCache.Store(abs, result)
	return result
}

// SaveIndexStatus writes the IndexStatus to {workspaceRoot}/.ragcode/index_status.json.
// The write is atomic: data is written to a temp file first, then renamed into place,
// so concurrent readers always see a complete JSON file.
func SaveIndexStatus(workspaceRoot string, status *IndexStatus) {
	if workspaceRoot == "" || status == nil {
		return
	}
	dir := filepath.Join(workspaceRoot, ".ragcode")
	dirExisted := true
	if _, statErr := os.Stat(dir); os.IsNotExist(statErr) {
		dirExisted = false
		// Only check parent .ragcode when we're about to create a NEW .ragcode dir.
		// When the dir already exists, the check is unnecessary (it was validated at creation time).
		if cachedHasParentRagcode(workspaceRoot) {
			logger.Instance.Debug("[INDEX_STATUS] 🚫 Blocked creating .ragcode in %s (parent already has .ragcode)", workspaceRoot)
			return
		}
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		logger.Instance.Warn("index_status: cannot create .ragcode dir: %v", err)
		return
	}

	// Log every write with caller stack to trace the source of spurious .ragcode directories
	callers := callerChain(2, 4)
	if !dirExisted {
		logger.Instance.Warn("[INDEX_STATUS] 🆕 CREATED .ragcode dir: workspace=%s, started_at=%s, ended_at=%s, callers=[%s]",
			workspaceRoot, status.StartedAt, status.EndedAt, callers)
	} else {
		logger.Instance.Debug("[INDEX_STATUS] 📝 Writing index_status.json: workspace=%s, started_at=%s, ended_at=%s, langs=%d, callers=[%s]",
			workspaceRoot, status.StartedAt, status.EndedAt, len(status.Languages), callers)
	}
	b, err := json.MarshalIndent(status, "", "  ")
	if err != nil {
		logger.Instance.Warn("index_status: marshal failed: %v", err)
		return
	}
	// Write to a temp file in the same directory so that rename is atomic.
	tmp, err := os.CreateTemp(dir, indexStatusFile+".tmp-*")
	if err != nil {
		logger.Instance.Warn("index_status: cannot create temp file: %v", err)
		return
	}
	tmpName := tmp.Name()
	if _, err := tmp.Write(b); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		logger.Instance.Warn("index_status: write to temp failed: %v", err)
		return
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		logger.Instance.Warn("index_status: close temp failed: %v", err)
		return
	}
	path := filepath.Join(dir, indexStatusFile)
	if err := os.Rename(tmpName, path); err != nil {
		// Windows-safe fallback: os.Rename fails when dest exists on Windows.
		_ = os.Remove(path)
		if err2 := os.Rename(tmpName, path); err2 != nil {
			os.Remove(tmpName)
			logger.Instance.Warn("index_status: rename failed for %s: %v", path, err2)
		}
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

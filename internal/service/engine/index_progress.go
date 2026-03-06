package engine

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/doITmagic/rag-code-mcp/internal/logger"
)

const indexStatusFile = "index_status.json"

type IndexLanguageProgress struct {
	TotalFiles int       `json:"total_files"`
	DoneFiles  int       `json:"done_files"`
	Percent    int       `json:"percent"`
	UpdatedAt  time.Time `json:"updated_at"`
}

type IndexProgress struct {
	JobID         string                           `json:"job_id"`
	WorkspaceID   string                           `json:"workspace_id"`
	WorkspaceRoot string                           `json:"workspace_root"`
	State         string                           `json:"state"` // starting|running|completed|failed
	StartedAt     time.Time                        `json:"started_at"`
	CompletedAt   *time.Time                       `json:"completed_at,omitempty"`
	Languages     map[string]IndexLanguageProgress `json:"languages,omitempty"`
	UpdatedAt     time.Time                        `json:"updated_at"`
	Error         string                           `json:"error,omitempty"`
}

type progressStore struct {
	mu   sync.Mutex
	jobs map[string]*IndexProgress
}

func newProgressStore() *progressStore {
	return &progressStore{jobs: map[string]*IndexProgress{}}
}

func (s *progressStore) get(workspaceID string, workspaceRoot string) *IndexProgress {
	s.mu.Lock()
	defer s.mu.Unlock()
	if p, ok := s.jobs[workspaceID]; ok {
		cp := *p
		if p.Languages != nil {
			cp.Languages = make(map[string]IndexLanguageProgress, len(p.Languages))
			for k, v := range p.Languages {
				cp.Languages[k] = v
			}
		}
		if p.CompletedAt != nil {
			t := *p.CompletedAt
			cp.CompletedAt = &t
		}
		return &cp
	}
	// Not in memory (e.g. after restart) — try loading from disk.
	if workspaceRoot != "" {
		if p := loadIndexStatus(workspaceRoot); p != nil && p.WorkspaceID == workspaceID {
			s.jobs[workspaceID] = p // cache in memory for subsequent calls
			cp := *p
			if p.Languages != nil {
				cp.Languages = make(map[string]IndexLanguageProgress, len(p.Languages))
				for k, v := range p.Languages {
					cp.Languages[k] = v
				}
			}
			if p.CompletedAt != nil {
				t := *p.CompletedAt
				cp.CompletedAt = &t
			}
			return &cp
		}
	}
	return nil
}

func (s *progressStore) start(workspaceID, workspaceRoot, jobID string, now time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.jobs[workspaceID] = &IndexProgress{
		JobID:         jobID,
		WorkspaceID:   workspaceID,
		WorkspaceRoot: workspaceRoot,
		State:         "starting",
		StartedAt:     now,
		UpdatedAt:     now,
		Languages:     map[string]IndexLanguageProgress{},
	}
}

func (s *progressStore) update(workspaceID, lang string, done, total int, now time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	p, ok := s.jobs[workspaceID]
	if !ok {
		p = &IndexProgress{
			WorkspaceID: workspaceID,
			State:       "running",
			StartedAt:   now,
			UpdatedAt:   now,
			Languages:   map[string]IndexLanguageProgress{},
		}
		s.jobs[workspaceID] = p
	}
	if p.State == "starting" {
		p.State = "running"
	}
	pct := 0
	if total > 0 {
		pct = done * 100 / total
		if pct < 0 {
			pct = 0
		}
		if pct > 100 {
			pct = 100
		}
	}
	p.Languages[lang] = IndexLanguageProgress{
		TotalFiles: total,
		DoneFiles:  done,
		Percent:    pct,
		UpdatedAt:  now,
	}
	p.UpdatedAt = now
}

func (s *progressStore) complete(workspaceID, workspaceRoot string, now time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	p, ok := s.jobs[workspaceID]
	if !ok {
		return
	}
	p.State = "completed"
	p.UpdatedAt = now
	p.CompletedAt = &now
	// Persist to disk so index_age survives process restarts.
	saveIndexStatus(workspaceRoot, p)
}

func (s *progressStore) fail(workspaceID, workspaceRoot string, now time.Time, errMsg string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	p, ok := s.jobs[workspaceID]
	if !ok {
		return
	}
	p.State = "failed"
	p.Error = errMsg
	p.UpdatedAt = now
	// Persist failed state too so we know something went wrong.
	saveIndexStatus(workspaceRoot, p)
}

// saveIndexStatus writes the IndexProgress snapshot to {workspaceRoot}/.ragcode/index_status.json.
func saveIndexStatus(workspaceRoot string, p *IndexProgress) {
	if workspaceRoot == "" {
		return
	}
	dir := filepath.Join(workspaceRoot, ".ragcode")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		logger.Instance.Warn("index_status: cannot create .ragcode dir: %v", err)
		return
	}
	path := filepath.Join(dir, indexStatusFile)
	b, err := json.MarshalIndent(p, "", "  ")
	if err != nil {
		logger.Instance.Warn("index_status: marshal failed: %v", err)
		return
	}
	if err := os.WriteFile(path, b, 0o644); err != nil {
		logger.Instance.Warn("index_status: write failed for %s: %v", path, err)
	}
}

// loadIndexStatus reads the last IndexProgress from {workspaceRoot}/.ragcode/index_status.json.
// Returns nil if the file doesn't exist or can't be parsed.
func loadIndexStatus(workspaceRoot string) *IndexProgress {
	path := filepath.Join(workspaceRoot, ".ragcode", indexStatusFile)
	b, err := os.ReadFile(path)
	if err != nil {
		return nil // file doesn't exist yet — first run
	}
	var p IndexProgress
	if err := json.Unmarshal(b, &p); err != nil {
		logger.Instance.Warn("index_status: parse failed for %s: %v", path, err)
		return nil
	}
	return &p
}

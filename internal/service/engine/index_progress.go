package engine

import (
	"sync"
	"time"
)

type IndexLanguageProgress struct {
	TotalFiles int       `json:"total_files"`
	DoneFiles  int       `json:"done_files"`
	Percent    int       `json:"percent"`
	UpdatedAt  time.Time `json:"updated_at"`
}

type IndexProgress struct {
	JobID        string                           `json:"job_id"`
	WorkspaceID  string                           `json:"workspace_id"`
	WorkspaceRoot string                          `json:"workspace_root"`
	State        string                           `json:"state"` // starting|running|completed|failed
	StartedAt    time.Time                        `json:"started_at"`
	CompletedAt  *time.Time                       `json:"completed_at,omitempty"`
	Languages    map[string]IndexLanguageProgress  `json:"languages,omitempty"`
	UpdatedAt    time.Time                        `json:"updated_at"`
	Error        string                           `json:"error,omitempty"`
}

type progressStore struct {
	mu   sync.Mutex
	jobs map[string]*IndexProgress
}

func newProgressStore() *progressStore {
	return &progressStore{jobs: map[string]*IndexProgress{}}
}

func (s *progressStore) get(workspaceID string) *IndexProgress {
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
		return &cp
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

func (s *progressStore) complete(workspaceID string, now time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	p, ok := s.jobs[workspaceID]
	if !ok {
		return
	}
	p.State = "completed"
	p.UpdatedAt = now
	p.CompletedAt = &now
}

func (s *progressStore) fail(workspaceID string, now time.Time, err string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	p, ok := s.jobs[workspaceID]
	if !ok {
		return
	}
	p.State = "failed"
	p.Error = err
	p.UpdatedAt = now
}

package workspace

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// FileState represents the state of a single file
type FileState struct {
	ModTime time.Time `json:"mod_time"`
	Size    int64     `json:"size"`
	// Hash    string    `json:"hash,omitempty"` // Optional: content hash for better accuracy
}

// WorkspaceState tracks the state of files in a workspace
type WorkspaceState struct {
	Files       map[string]FileState `json:"files"`
	LastIndexed time.Time            `json:"last_indexed"`
	mu          sync.RWMutex
}

// NewWorkspaceState creates a new workspace state
func NewWorkspaceState() *WorkspaceState {
	return &WorkspaceState{
		Files: make(map[string]FileState),
	}
}

// LoadState loads workspace state from disk
func LoadState(path string) (*WorkspaceState, error) {
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return NewWorkspaceState(), nil
		}
		return nil, err
	}
	defer f.Close()

	var state WorkspaceState
	if err := json.NewDecoder(f).Decode(&state); err != nil {
		return nil, err
	}

	if state.Files == nil {
		state.Files = make(map[string]FileState)
	}

	return &state, nil
}

// SaveState saves workspace state to disk
func (s *WorkspaceState) Save(path string) error {
	s.mu.RLock()
	defer s.mu.RUnlock()

	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}

	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()

	s.LastIndexed = time.Now()
	return json.NewEncoder(f).Encode(s)
}

// UpdateFile updates the state for a file
func (s *WorkspaceState) UpdateFile(path string, info os.FileInfo) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.Files[path] = FileState{
		ModTime: info.ModTime(),
		Size:    info.Size(),
	}
}

// RemoveFile removes a file from the state
func (s *WorkspaceState) RemoveFile(path string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.Files, path)
}

// GetFileState returns the state of a file
func (s *WorkspaceState) GetFileState(path string) (FileState, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	state, ok := s.Files[path]
	return state, ok
}

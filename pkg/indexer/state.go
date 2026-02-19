package indexer

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// FileState represents the state of a single file in the index.
type FileState struct {
	Path    string    `json:"path"`
	ModTime time.Time `json:"mod_time"`
	Size    int64     `json:"size"`
	Hash    string    `json:"hash,omitempty"` // Reserved for future content hashing
}

// State represents the persistent state of a workspace index.
type State struct {
	Files map[string]FileState `json:"files"`
	mu    sync.RWMutex
}

// NewState creates a new, empty index state.
func NewState() *State {
	return &State{
		Files: make(map[string]FileState),
	}
}

// LoadState loads the index state from a file.
func LoadState(path string) (*State, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return NewState(), nil
		}
		return nil, err
	}

	state := NewState()
	if err := json.Unmarshal(data, state); err != nil {
		return nil, err
	}

	return state, nil
}

// Save saves the index state to a file.
func (s *State) Save(path string) error {
	s.mu.RLock()
	defer s.mu.RUnlock()

	// Ensure directory exists
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}

	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}

	return os.WriteFile(path, data, 0644)
}

// UpdateFile updates the state for a single file.
func (s *State) UpdateFile(path string, info os.FileInfo) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.Files[path] = FileState{
		Path:    path,
		ModTime: info.ModTime(),
		Size:    info.Size(),
	}
}

// RemoveFile removes a file from the state.
func (s *State) RemoveFile(path string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	delete(s.Files, path)
}

// GetFileState retrieves the state for a single file.
func (s *State) GetFileState(path string) (FileState, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	state, ok := s.Files[path]
	return state, ok
}

// IsChanged checks if a file has changed compared to its stored state.
func (s *State) IsChanged(path string, info os.FileInfo) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()

	state, ok := s.Files[path]
	if !ok {
		return true // New file
	}

	// Simple check based on mod time and size (fastest)
	return !info.ModTime().Equal(state.ModTime) || info.Size() != state.Size
}

package memory

import (
	"fmt"
	"sync"
)

// AgentState represents the current state of an agent
type AgentState struct {
	CurrentTask string
	History     []string
	Variables   map[string]interface{}
	mu          sync.RWMutex
}

// NewAgentState creates a new agent state
func NewAgentState() *AgentState {
	return &AgentState{
		History:   make([]string, 0),
		Variables: make(map[string]interface{}),
	}
}

// SetTask sets the current task
func (s *AgentState) SetTask(task string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.CurrentTask = task
	s.History = append(s.History, task)
}

// GetTask returns the current task
func (s *AgentState) GetTask() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.CurrentTask
}

// SetVariable sets a state variable
func (s *AgentState) SetVariable(key string, value interface{}) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.Variables[key] = value
}

// GetVariable gets a state variable
func (s *AgentState) GetVariable(key string) (interface{}, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	val, ok := s.Variables[key]
	return val, ok
}

// GetHistory returns the task history
func (s *AgentState) GetHistory() []string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	result := make([]string, len(s.History))
	copy(result, s.History)
	return result
}

// Clear clears the state
func (s *AgentState) Clear() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.CurrentTask = ""
	s.History = make([]string, 0)
	s.Variables = make(map[string]interface{})
}

// String returns a string representation of the state
func (s *AgentState) String() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return fmt.Sprintf("Task: %s, History: %v, Variables: %v", s.CurrentTask, s.History, s.Variables)
}

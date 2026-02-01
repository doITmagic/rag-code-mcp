package memory

import (
	"fmt"
	"sync"
)

// Message represents a conversation message
type Message struct {
	Role    string // system, user, assistant
	Content string
}

// ShortTermMemory manages the current conversation context
type ShortTermMemory struct {
	messages []Message
	maxSize  int
	mu       sync.RWMutex
}

// NewShortTermMemory creates a new short-term memory
func NewShortTermMemory(maxSize int) *ShortTermMemory {
	return &ShortTermMemory{
		messages: make([]Message, 0, maxSize),
		maxSize:  maxSize,
	}
}

// Add adds a message to the memory
func (m *ShortTermMemory) Add(role, content string) {
	m.mu.Lock()
	defer m.mu.Unlock()

	msg := Message{
		Role:    role,
		Content: content,
	}

	m.messages = append(m.messages, msg)

	// Keep only the last maxSize messages
	if len(m.messages) > m.maxSize {
		m.messages = m.messages[len(m.messages)-m.maxSize:]
	}
}

// GetAll returns all messages in memory
func (m *ShortTermMemory) GetAll() []Message {
	m.mu.RLock()
	defer m.mu.RUnlock()

	result := make([]Message, len(m.messages))
	copy(result, m.messages)
	return result
}

// GetLast returns the last n messages
func (m *ShortTermMemory) GetLast(n int) []Message {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if n > len(m.messages) {
		n = len(m.messages)
	}

	result := make([]Message, n)
	copy(result, m.messages[len(m.messages)-n:])
	return result
}

// Clear clears all messages
func (m *ShortTermMemory) Clear() {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.messages = make([]Message, 0, m.maxSize)
}

// Size returns the current number of messages
func (m *ShortTermMemory) Size() int {
	m.mu.RLock()
	defer m.mu.RUnlock()

	return len(m.messages)
}

// String returns a string representation of the conversation
func (m *ShortTermMemory) String() string {
	m.mu.RLock()
	defer m.mu.RUnlock()

	result := ""
	for _, msg := range m.messages {
		result += fmt.Sprintf("[%s]: %s\n", msg.Role, msg.Content)
	}
	return result
}

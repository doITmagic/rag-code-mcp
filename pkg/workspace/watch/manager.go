package watch

import (
	"context"
	"fmt"
	"sync"
)

// OnChangeFunc is invoked when file changes are detected for a workspace.
type OnChangeFunc func(ctx context.Context, root string, changedFiles []string) error

// Watcher abstracts a filesystem watcher implementation.
type Watcher interface {
	Start()
	Stop()
}

// Factory creates a watcher for a workspace root.
type Factory func(root string, opts Options, onChange OnChangeFunc) (Watcher, error)

// Manager manages filesystem watchers per workspace.
type Manager struct {
	opts    Options
	factory Factory

	mu       sync.Mutex
	watchers map[string]Watcher
}

// NewManager creates a new watcher manager.
func NewManager(opts Options) *Manager {
	return &Manager{
		opts:     opts,
		factory:  defaultFactory,
		watchers: make(map[string]Watcher),
	}
}

// SetFactory overrides the watcher factory (useful for tests).
func (m *Manager) SetFactory(factory Factory) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if factory == nil {
		m.factory = defaultFactory
		return
	}
	m.factory = factory
}

// Start begins watching a workspace root if not already running.
func (m *Manager) Start(root string, onChange OnChangeFunc) error {
	if root == "" {
		return fmt.Errorf("workspace root is empty")
	}

	m.mu.Lock()
	if _, exists := m.watchers[root]; exists {
		m.mu.Unlock()
		return nil
	}
	factory := m.factory
	opts := m.opts
	m.mu.Unlock()

	watcher, err := factory(root, opts, onChange)
	if err != nil {
		return err
	}

	m.mu.Lock()
	if _, exists := m.watchers[root]; exists {
		m.mu.Unlock()
		watcher.Stop()
		return nil
	}
	m.watchers[root] = watcher
	m.mu.Unlock()

	watcher.Start()
	return nil
}

// Stop halts the watcher for a workspace root.
func (m *Manager) Stop(root string) {
	m.mu.Lock()
	watcher, ok := m.watchers[root]
	if ok {
		delete(m.watchers, root)
	}
	m.mu.Unlock()
	if ok {
		watcher.Stop()
	}
}

// StopAll stops all watchers.
func (m *Manager) StopAll() {
	m.mu.Lock()
	watchers := m.watchers
	m.watchers = make(map[string]Watcher)
	m.mu.Unlock()

	for _, watcher := range watchers {
		watcher.Stop()
	}
}

func defaultFactory(root string, opts Options, onChange OnChangeFunc) (Watcher, error) {
	return NewFileWatcher(root, opts, onChange)
}

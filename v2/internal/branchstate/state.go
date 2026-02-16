package branchstate

import (
	context "context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/doITmagic/rag-code-mcp/v2/internal/contract"
	"github.com/doITmagic/rag-code-mcp/v2/pkg/workspace"
)

var ErrGitMetadataUnavailable = errors.New("git metadata unavailable")

// Logger defines minimal observability hooks for branchstate.
type Logger interface {
	Debug(ctx context.Context, msg string, fields map[string]any)
}

// State captures persisted branch/head metadata.
type State struct {
	SchemaVersion  string    `json:"schema_version"`
	LastBranch     string    `json:"last_branch"`
	LastHeadSHA    string    `json:"last_head_sha"`
	LastWorktreeID string    `json:"last_worktree_id"`
	LastIndexedAt  time.Time `json:"last_indexed_at"`
}

const currentSchemaVersion = "v1"

// Manager handles branch state persistence and comparisons.
type Manager struct {
	clock    func() time.Time
	cache    map[string]cachedGitState
	cacheTTL time.Duration
	mu       sync.Mutex
	logger   Logger
}

// NewManager creates a branch state manager.
func NewManager(opts ...Option) *Manager {
	mgr := &Manager{
		clock:    time.Now,
		cache:    make(map[string]cachedGitState),
		cacheTTL: 2 * time.Second,
	}
	for _, opt := range opts {
		opt(mgr)
	}
	return mgr
}

// Option customizes the Manager.
type Option func(*Manager)

// WithCacheTTL overrides the default in-memory git-state cache TTL.
func WithCacheTTL(ttl time.Duration) Option {
	return func(m *Manager) {
		m.cacheTTL = ttl
	}
}

// WithLogger sets a debug logger for branchstate lifecycle events.
func WithLogger(logger Logger) Option {
	return func(m *Manager) {
		m.logger = logger
	}
}

type cachedGitState struct {
	state  *State
	err    error
	readAt time.Time
}

// CompareAndUpdate compares current git state with persisted state and updates the file when needed.
func (m *Manager) CompareAndUpdate(ctx context.Context, workspaceRoot string) (*State, bool, contract.ReasonCode, *contract.ResolveWorkspaceError) {
	statePath := filepath.Join(workspaceRoot, ".ragcode", "branch_state.json")
	persisted, err := m.loadState(statePath)
	if err != nil {
		return nil, false, "", &contract.ResolveWorkspaceError{
			Code:    contract.ErrorInvalidPath,
			Message: fmt.Sprintf("failed to load branch state: %v", err),
			Reason:  contract.ReasonInvalidPath,
		}
	}

	current, err := m.readGitState(ctx, workspaceRoot)
	if err != nil {
		if errors.Is(err, ErrGitMetadataUnavailable) || errors.Is(err, workspace.ErrNotARepository) {
			m.log(ctx, "branchstate.git_metadata_unavailable", map[string]any{"workspace_root": workspaceRoot})
			return nil, false, contract.ReasonRootsUnavailable, nil
		}
		return nil, false, "", &contract.ResolveWorkspaceError{
			Code:    contract.ErrorInvalidPath,
			Message: fmt.Sprintf("failed to read git state: %v", err),
			Reason:  contract.ReasonInvalidPath,
		}
	}

	reindex := persisted == nil || persisted.LastBranch != current.LastBranch || persisted.LastHeadSHA != current.LastHeadSHA || persisted.LastWorktreeID != current.LastWorktreeID
	reason := contract.ReasonCode("")
	if reindex {
		state := &State{
			SchemaVersion:  currentSchemaVersion,
			LastBranch:     current.LastBranch,
			LastHeadSHA:    current.LastHeadSHA,
			LastWorktreeID: current.LastWorktreeID,
			LastIndexedAt:  m.clock(),
		}
		if err := m.saveState(statePath, state); err != nil {
			return nil, false, "", &contract.ResolveWorkspaceError{
				Code:    contract.ErrorInvalidPath,
				Message: fmt.Sprintf("failed to save branch state: %v", err),
				Reason:  contract.ReasonInvalidPath,
			}
		}
		if persisted == nil {
			reason = contract.ReasonFirstSeen
		} else if persisted.LastBranch != current.LastBranch {
			reason = contract.ReasonBranchChanged
		} else if persisted.LastHeadSHA != current.LastHeadSHA {
			reason = contract.ReasonHeadChanged
		}
		m.log(ctx, "branchstate.reindex_required", map[string]any{
			"workspace_root": workspaceRoot,
			"reason":         reason,
			"branch":         current.LastBranch,
			"head_sha":       current.LastHeadSHA,
		})
	} else {
		m.log(ctx, "branchstate.reindex_not_required", map[string]any{
			"workspace_root": workspaceRoot,
			"branch":         current.LastBranch,
			"head_sha":       current.LastHeadSHA,
		})
	}

	return current, reindex, reason, nil
}

// Annotate implements resolver.BranchAnnotator.
func (m *Manager) Annotate(ctx context.Context, root string, resp *contract.ResolveWorkspaceResponse) *contract.ResolveWorkspaceError {
	statePath := filepath.Join(root, ".ragcode", "branch_state.json")
	persisted, _ := m.loadState(statePath)

	state, reindex, reason, err := m.CompareAndUpdate(ctx, root)
	if err != nil {
		return err
	}

	risk := "low"
	if persisted == nil {
		risk = "high"
	} else if persisted.LastBranch != state.LastBranch {
		risk = "high"
	} else if persisted.LastHeadSHA != state.LastHeadSHA {
		risk = "medium"
	}

	if state != nil {
		resp.Branch = state.LastBranch
		resp.HeadSHA = state.LastHeadSHA
		resp.WorktreeID = state.LastWorktreeID
	}
	resp.MismatchRisk = risk
	resp.ReindexRequired = reindex
	if reason != "" && resp.Reason == "" {
		resp.Reason = reason
	}
	return nil
}

func (m *Manager) loadState(path string) (*State, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read branch state: %w", err)
	}
	var state State
	if err := json.Unmarshal(data, &state); err != nil {
		return nil, fmt.Errorf("decode branch state json: %w", err)
	}
	return &state, nil
}

func (m *Manager) saveState(path string, state *State) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	encoded, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, encoded, 0o644)
}

func (m *Manager) readGitState(ctx context.Context, root string) (*State, error) {
	m.mu.Lock()
	entry, ok := m.cache[root]
	if ok && m.clock().Sub(entry.readAt) < m.cacheTTL {
		m.mu.Unlock()
		return entry.state, entry.err
	}
	m.mu.Unlock()

	pkgState, err := workspace.GetState(ctx, root)
	if err != nil {
		m.storeCache(root, nil, err)
		return nil, err
	}

	state := &State{
		SchemaVersion:  currentSchemaVersion,
		LastBranch:     pkgState.Branch,
		LastHeadSHA:    pkgState.HeadSHA,
		LastWorktreeID: pkgState.WorktreeID,
		LastIndexedAt:  m.clock(),
	}
	m.storeCache(root, state, nil)
	return state, nil
}

func (m *Manager) storeCache(root string, state *State, err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.cache == nil {
		m.cache = make(map[string]cachedGitState)
	}
	m.cache[root] = cachedGitState{state: state, err: err, readAt: m.clock()}
}

func (m *Manager) log(ctx context.Context, msg string, fields map[string]any) {
	if m.logger == nil {
		return
	}
	if fields == nil {
		fields = map[string]any{}
	}
	m.logger.Debug(ctx, msg, fields)
}

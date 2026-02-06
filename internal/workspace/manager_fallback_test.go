package workspace

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/doITmagic/rag-code-mcp/internal/config"
	"github.com/doITmagic/rag-code-mcp/internal/llm"
	"github.com/doITmagic/rag-code-mcp/internal/storage"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type MockProvider struct {
	llm.Provider
}

func (m *MockProvider) Embed(ctx context.Context, text string) ([]float64, error) {
	return []float64{0.1, 0.2}, nil
}
func (m *MockProvider) Name() string { return "mock" }

func TestDetectWorkspace_Fallbacks(t *testing.T) {
	// Setup temp structure
	tempDir, err := os.MkdirTemp("", "manager-test-*")
	require.NoError(t, err)
	defer os.RemoveAll(tempDir)

	// Create a mock project
	projectRoot := filepath.Join(tempDir, "my-project")
	require.NoError(t, os.MkdirAll(projectRoot, 0755))
	require.NoError(t, os.WriteFile(filepath.Join(projectRoot, "go.mod"), []byte("module my-project"), 0644))

	// Setup Manager
	cfg := &config.Config{}
	cfg.Workspace.Enabled = true
	cfg.Workspace.CollectionPrefix = "test-prefix"

	// Use dummy dependencies
	mgr := NewManager(&storage.QdrantClient{}, &MockProvider{}, cfg)

	// Setup registry
	regPath := filepath.Join(tempDir, "workspaces.json")
	reg, err := NewRegistry(regPath)
	require.NoError(t, err)
	mgr.registry = reg

	t.Run("Priority 2.5: CWD Fallback", func(t *testing.T) {
		// Change CWD to project root
		oldCwd, _ := os.Getwd()
		require.NoError(t, os.Chdir(projectRoot))
		defer func() {
			err := os.Chdir(oldCwd)
			if err != nil {
				t.Logf("failed to restore CWD: %v", err)
			}
		}()

		// Call DetectWorkspace with no params
		info, err := mgr.DetectWorkspace(nil)
		assert.NoError(t, err)
		assert.NotNil(t, info)
		assert.Equal(t, projectRoot, info.Root)
		assert.Equal(t, "test-prefix", info.CollectionPrefix)

		// Verify it was cached by CWD
		cached := mgr.cache.Get(projectRoot)
		assert.NotNil(t, cached)
		assert.Equal(t, projectRoot, cached.Root)
	})

	t.Run("Priority 3: Registry Fallback", func(t *testing.T) {
		// Ensure CWD is NOT a workspace
		emptyDir := filepath.Join(tempDir, "empty")
		_ = os.MkdirAll(emptyDir, 0755)
		oldCwd, _ := os.Getwd()
		require.NoError(t, os.Chdir(emptyDir))
		defer func() {
			err := os.Chdir(oldCwd)
			if err != nil {
				t.Logf("failed to restore CWD: %v", err)
			}
		}()

		// Add project to registry
		info := &Info{
			ID:   "proj-1",
			Root: projectRoot,
		}
		require.NoError(t, reg.RegisterOrUpdate(info))

		// Call DetectWorkspace with no params
		fallbackInfo, err := mgr.DetectWorkspace(nil)
		assert.NoError(t, err)
		assert.NotNil(t, fallbackInfo)
		assert.Equal(t, projectRoot, fallbackInfo.Root)
		assert.Equal(t, "test-prefix", fallbackInfo.CollectionPrefix)
		assert.Contains(t, fallbackInfo.AIWarning, "Automatically selected the last active workspace")
	})

	t.Run("Priority Order Check", func(t *testing.T) {
		// Create two projects
		p1Root := filepath.Join(tempDir, "p1")
		p2Root := filepath.Join(tempDir, "p2")
		_ = os.MkdirAll(p1Root, 0755)
		_ = os.MkdirAll(p2Root, 0755)
		_ = os.WriteFile(filepath.Join(p1Root, "go.mod"), []byte("module p1"), 0644)
		_ = os.WriteFile(filepath.Join(p2Root, "go.mod"), []byte("module p2"), 0644)

		// 1. Add P1 to registry (lowest priority)
		_ = reg.RegisterOrUpdate(&Info{ID: "p1", Root: p1Root})
		// Wait for save
		time.Sleep(600 * time.Millisecond)

		// 2. Set CWD to P2 (medium priority)
		oldCwd, _ := os.Getwd()
		_ = os.Chdir(p2Root)
		defer os.Chdir(oldCwd)

		// Detection should pick CWD (P2) over Registry (P1)
		info, err := mgr.DetectWorkspace(nil)
		assert.NoError(t, err)
		assert.Equal(t, p2Root, info.Root, "Should prioritize CWD over Registry")

		// 3. Provide file_path to P1 (highest priority)
		params := map[string]interface{}{"file_path": filepath.Join(p1Root, "main.go")}
		info, err = mgr.DetectWorkspace(params)
		assert.NoError(t, err)
		assert.Equal(t, p1Root, info.Root, "Should prioritize file_path over CWD")
	})
}

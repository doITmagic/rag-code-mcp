package workspace

import (
	"context"
	"os"
	"path/filepath"
	"testing"

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
}

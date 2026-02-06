package workspace

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRegistry(t *testing.T) {
	// Setup temp registry file
	tempDir, err := os.MkdirTemp("", "registry-test-*")
	require.NoError(t, err)
	defer os.RemoveAll(tempDir)

	regPath := filepath.Join(tempDir, "workspaces.json")

	t.Run("NewRegistry with non-existent file", func(t *testing.T) {
		reg, err := NewRegistry(regPath)
		assert.NoError(t, err)
		assert.NotNil(t, reg)
		assert.Empty(t, reg.Entries)
	})

	t.Run("RegisterOrUpdate new entry", func(t *testing.T) {
		reg, _ := NewRegistry(regPath)
		info := &Info{
			ID:        "test-id-1",
			Root:      "/workspace/1",
			Languages: []string{"go", "python"},
		}

		err := reg.RegisterOrUpdate(info)
		assert.NoError(t, err)
		assert.Len(t, reg.Entries, 1)

		entry := reg.Entries["test-id-1"]
		assert.Equal(t, "test-id-1", entry.ID)
		assert.Equal(t, "/workspace/1", entry.Root)
		assert.Equal(t, []string{"go", "python"}, entry.Languages)
		assert.False(t, entry.LastUsed.IsZero())

		// Verify it was saved to disk
		_, err = os.Stat(regPath)
		assert.NoError(t, err)
	})

	t.Run("RegisterOrUpdate update existing entry", func(t *testing.T) {
		reg, _ := NewRegistry(regPath)
		info := &Info{
			ID:        "test-id-1",
			Root:      "/workspace/1",
			Languages: []string{"go"},
		}

		// Initial registration
		_ = reg.RegisterOrUpdate(info)
		firstTime := reg.Entries["test-id-1"].LastUsed

		// Small delay to ensure timestamp change
		time.Sleep(10 * time.Millisecond)

		// Update languages
		info.Languages = []string{"javascript"}
		err := reg.RegisterOrUpdate(info)
		assert.NoError(t, err)

		entry := reg.Entries["test-id-1"]
		assert.Equal(t, []string{"javascript"}, entry.Languages)
		assert.True(t, entry.LastUsed.After(firstTime))
	})

	t.Run("Load from disk", func(t *testing.T) {
		// New registry instance pointing to same file
		reg, err := NewRegistry(regPath)
		assert.NoError(t, err)
		assert.Len(t, reg.Entries, 1)
		assert.Equal(t, "test-id-1", reg.Entries["test-id-1"].ID)
	})

	t.Run("GetLastUsed", func(t *testing.T) {
		reg, _ := NewRegistry(regPath)

		// Add second workspace
		info2 := &Info{
			ID:   "test-id-2",
			Root: "/workspace/2",
		}
		_ = reg.RegisterOrUpdate(info2)

		// test-id-2 should be last used now
		last := reg.GetLastUsed()
		assert.NotNil(t, last)
		assert.Equal(t, "test-id-2", last.ID)

		// Re-update first one
		info1 := &Info{
			ID:   "test-id-1",
			Root: "/workspace/1",
		}
		_ = reg.RegisterOrUpdate(info1)

		// test-id-1 should be last used now
		last = reg.GetLastUsed()
		assert.Equal(t, "test-id-1", last.ID)
	})
}

func TestRegistryConcurrency(t *testing.T) {
	tempDir, _ := os.MkdirTemp("", "registry-concurrent-*")
	defer os.RemoveAll(tempDir)
	regPath := filepath.Join(tempDir, "workspaces.json")

	reg, _ := NewRegistry(regPath)

	const count = 100
	done := make(chan bool)

	for i := 0; i < count; i++ {
		go func(id int) {
			info := &Info{
				ID:   fmt.Sprintf("id-%d", id),
				Root: fmt.Sprintf("/root/%d", id),
			}
			_ = reg.RegisterOrUpdate(info)
			_ = reg.GetLastUsed()
			done <- true
		}(i)
	}

	for i := 0; i < count; i++ {
		<-done
	}

	assert.Len(t, reg.Entries, count)
}

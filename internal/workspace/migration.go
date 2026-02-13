package workspace

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
)

// CheckAndPrepareMigration checks if collection needs migration due to dimension mismatch
// Returns: (newCollectionName, oldCollectionName, needsMigration, error)
func (m *Manager) CheckAndPrepareMigration(ctx context.Context, info *Info, language string) (string, string, bool, error) {
	collectionName := info.CollectionNameForLanguage(language)

	// Use per-collection lock to prevent concurrent reset/migration of the same collection
	lock := m.getCollectionMutex(collectionName)
	lock.Lock()
	defer lock.Unlock()

	// Check if collection exists
	exists, err := m.qdrant.CollectionExists(ctx, collectionName)
	if err != nil {
		return collectionName, "", false, fmt.Errorf("failed to check collection: %w", err)
	}

	if !exists {
		// Collection doesn't exist, no migration needed
		return collectionName, "", false, nil
	}

	// Try to get collection info to check vector dimensions
	collectionInfo, err := m.qdrant.GetCollectionInfo(ctx, collectionName)
	if err != nil {
		log.Printf("⚠️ Could not get collection info, proceeding without migration: %v", err)
		return collectionName, "", false, nil
	}

	// Get current embedding dimension from LLM config
	currentDimension := m.llm.GetEmbeddingDimension()
	existingDimension := collectionInfo.VectorSize

	// Case 1: Dimension mismatch - hard reset
	if existingDimension > 0 && currentDimension > 0 && existingDimension != currentDimension {
		log.Printf("🔄 Dimension mismatch detected in collection '%s': %d vs %d", collectionName, existingDimension, currentDimension)
		log.Printf("🧹 Resetting collection '%s' to use %d dimensions", collectionName, currentDimension)

		if err := m.qdrant.DeleteCollection(ctx, collectionName); err != nil {
			log.Printf("⚠️ Failed to delete old collection during reset: %v", err)
		}

		if err := m.qdrant.CreateCollection(ctx, collectionName, int(currentDimension)); err != nil {
			return collectionName, "", false, fmt.Errorf("failed to recreate collection with new dimension: %w", err)
		}

		return collectionName, "", true, nil
	}

	// Case 2: Collection exists but is empty - trigger full re-index to be safe
	if collectionInfo.PointsCount == 0 {
		log.Printf("ℹ️ Collection '%s' exists but is empty. Triggering full re-index.", collectionName)
		return collectionName, "", true, nil
	}

	return collectionName, "", false, nil
}

// DeleteLanguageCollection deletes the Qdrant collection and associated state for a language
func (m *Manager) DeleteLanguageCollection(ctx context.Context, info *Info, language string) error {
	collectionName := info.CollectionNameForLanguage(language)

	// Use per-collection lock to prevent racing with migration/init operations
	lock := m.getCollectionMutex(collectionName)
	lock.Lock()
	defer lock.Unlock()

	// Remove from cache
	m.memoryMu.Lock()
	if mem, ok := m.memories[collectionName]; ok {
		if closer, ok := mem.(interface{ Close() error }); ok {
			closer.Close()
		}
		delete(m.memories, collectionName)
	}
	m.memoryMu.Unlock()

	// Delete from Qdrant
	if err := m.qdrant.DeleteCollection(ctx, collectionName); err != nil {
		log.Printf("⚠️ Failed to delete collection %s from Qdrant: %v", collectionName, err)
	}

	return nil
}

// MigrateExistingWorkspaces scans AllowedWorkspacePaths (and common project dirs)
// for directories containing .ragcode/state.json — a marker that they were previously indexed.
// Each found workspace is detected via DetectFromPath and registered in the persistent registry.
// This ensures users upgrading from older versions have their registry pre-populated at startup.
func (m *Manager) MigrateExistingWorkspaces() {
	if m.detector == nil || m.registry == nil {
		return
	}

	// If registry already has workspaces, skip migration
	if m.registry.Len() > 0 {
		log.Printf("[migrate] Registry already has %d workspace(s), skipping migration", m.registry.Len())
		return
	}

	// Collect candidate directories to scan
	var candidates []string
	if m.config != nil {
		candidates = append(candidates, m.config.Workspace.AllowedWorkspacePaths...)
	}

	if len(candidates) == 0 {
		log.Printf("[migrate] No AllowedWorkspacePaths configured, skipping migration")
		return
	}

	log.Printf("[migrate] Scanning %d allowed path(s) for previously indexed workspaces...", len(candidates))

	found := 0
	for _, base := range candidates {
		// Expand tilde
		if strings.HasPrefix(base, "~/") {
			if home, err := os.UserHomeDir(); err == nil {
				base = filepath.Join(home, base[2:])
			}
		}

		absBase, err := filepath.Abs(base)
		if err != nil {
			continue
		}

		// Check if the base itself has .ragcode/state.json
		if m.tryMigrateDir(absBase) {
			found++
		}

		// Scan one level deep (immediate subdirectories = typical project layout)
		entries, err := os.ReadDir(absBase)
		if err != nil {
			continue
		}
		for _, entry := range entries {
			if !entry.IsDir() {
				continue
			}
			subDir := filepath.Join(absBase, entry.Name())
			if m.tryMigrateDir(subDir) {
				found++
			}
		}
	}

	if found > 0 {
		log.Printf("[migrate] ✓ Migrated %d workspace(s) into registry", found)
	} else {
		log.Printf("[migrate] No previously indexed workspaces found")
	}
}

// tryMigrateDir checks if dir contains .ragcode/state.json, detects workspace, and registers it.
func (m *Manager) tryMigrateDir(dir string) bool {
	stateFile := filepath.Join(dir, ".ragcode", "state.json")
	if _, err := os.Stat(stateFile); err != nil {
		return false
	}

	// Already registered?
	if entry := m.registry.FindByRoot(dir); entry != nil {
		return false
	}

	info, err := m.detector.DetectFromPath(dir)
	if err != nil {
		log.Printf("[migrate] Skipping %s: %v", dir, err)
		return false
	}

	if m.config != nil && m.config.Workspace.CollectionPrefix != "" {
		info.CollectionPrefix = m.config.Workspace.CollectionPrefix
	}

	if err := m.registry.Register(info); err != nil {
		log.Printf("[migrate] Failed to register %s: %v", dir, err)
		return false
	}

	log.Printf("[migrate] Registered workspace: %s (%s)", info.Root, info.ID)
	return true
}

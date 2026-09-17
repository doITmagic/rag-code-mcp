package indexer

import (
	"os"
	"path/filepath"
	"strings"
)

// FindDeletedRoot walks up from filePath to find the highest deleted directory.
// If only the file itself is missing but its parent directory exists, returns filePath.
// If a parent directory is also missing, walks further up to find the topmost deleted ancestor
// below workspaceRoot.
func FindDeletedRoot(filePath, workspaceRoot string) string {
	highestDeleted := filePath
	dir := filepath.Dir(filePath)
	wsClean := filepath.Clean(workspaceRoot)

	for {
		dirClean := filepath.Clean(dir)
		// Stop if we've reached the workspace root or filesystem root
		if dirClean == wsClean || dirClean == "/" || dirClean == "." {
			break
		}

		if _, err := os.Stat(dir); os.IsNotExist(err) {
			// This directory is also deleted — record it and keep going up
			highestDeleted = dir
			dir = filepath.Dir(dir)
			continue
		}
		// Directory exists — stop here
		break
	}

	return filepath.Clean(highestDeleted)
}

// IsDirectoryDeletion returns true if the deleted root is a directory path
// (i.e., a parent directory was deleted, not just the individual file).
func IsDirectoryDeletion(deletedRoot, originalFile string) bool {
	return filepath.Clean(deletedRoot) != filepath.Clean(originalFile)
}

// CollectStaleFiles iterates State.Files and returns paths
// that no longer exist on disk.
func CollectStaleFiles(state *State) []string {
	state.mu.RLock()
	defer state.mu.RUnlock()

	var stale []string
	for path := range state.Files {
		if _, err := os.Stat(path); os.IsNotExist(err) {
			stale = append(stale, path)
		}
	}
	return stale
}

// GroupByDeletedRoot groups stale file paths by their highest deleted ancestor directory.
// Returns two collections:
//   - dirPrefixes: map[prefix][]filePath — files under a deleted directory
//   - individualFiles: files whose parent directory still exists
func GroupByDeletedRoot(staleFiles []string, workspaceRoot string) (map[string][]string, []string) {
	dirPrefixes := make(map[string][]string)
	var individualFiles []string

	for _, f := range staleFiles {
		root := FindDeletedRoot(f, workspaceRoot)
		if IsDirectoryDeletion(root, f) {
			prefix := root
			if !strings.HasSuffix(prefix, string(os.PathSeparator)) {
				prefix += string(os.PathSeparator)
			}
			dirPrefixes[prefix] = append(dirPrefixes[prefix], f)
		} else {
			individualFiles = append(individualFiles, f)
		}
	}

	return dirPrefixes, individualFiles
}

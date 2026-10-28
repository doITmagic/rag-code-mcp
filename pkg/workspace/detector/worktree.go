package detector

import (
	"os"
	"path/filepath"
	"strings"
)

// IsLinkedWorktree reports whether root has its own Git worktree administration
// directory. Unlike a submodule's gitfile, a linked worktree also has commondir.
func IsLinkedWorktree(root string) bool {
	data, err := os.ReadFile(filepath.Join(root, ".git"))
	if err != nil || !strings.HasPrefix(string(data), "gitdir:") {
		return false
	}
	gitDir := strings.TrimSpace(strings.TrimPrefix(string(data), "gitdir:"))
	if gitDir == "" {
		return false
	}
	if !filepath.IsAbs(gitDir) {
		gitDir = filepath.Join(root, gitDir)
	}
	data, err = os.ReadFile(filepath.Join(gitDir, "commondir"))
	if err != nil {
		return false
	}
	commonDir := strings.TrimSpace(string(data))
	if commonDir == "" {
		return false
	}
	if !filepath.IsAbs(commonDir) {
		commonDir = filepath.Join(gitDir, commonDir)
	}
	info, err := os.Stat(commonDir)
	return err == nil && info.IsDir() && filepath.Clean(commonDir) != filepath.Clean(gitDir)
}

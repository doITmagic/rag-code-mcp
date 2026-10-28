package contract

import (
	"crypto/sha1"
	"encoding/hex"
	"path/filepath"
	"runtime"
	"strings"
)

// WorkspacePathKey preserves distinct paths on case-sensitive operating systems.
func WorkspacePathKey(path string) string {
	path = filepath.Clean(path)
	if runtime.GOOS == "windows" {
		path = strings.ToLower(path)
	}
	return path
}

func DeriveWorkspaceID(root, branch, worktreeID string) string {
	normalizedRoot := WorkspacePathKey(strings.TrimSpace(root))
	normalizedBranch := strings.TrimSpace(branch)
	normalizedWorktree := strings.TrimSpace(worktreeID)
	if normalizedWorktree != "" {
		normalizedWorktree = WorkspacePathKey(normalizedWorktree)
	}
	// New collections avoid reusing vectors mixed by the old case-folded identities.
	payload := "v2|" + normalizedRoot + "|" + normalizedBranch + "|" + normalizedWorktree
	digest := sha1.Sum([]byte(payload))
	return hex.EncodeToString(digest[:])
}

func DerivePathContextKey(root, branch, headSHA, worktreeID string) string {
	normalizedRoot := WorkspacePathKey(strings.TrimSpace(root))
	normalizedBranch := strings.TrimSpace(branch)
	normalizedHead := strings.ToLower(strings.TrimSpace(headSHA))
	normalizedWorktree := strings.TrimSpace(worktreeID)
	if normalizedWorktree != "" {
		normalizedWorktree = WorkspacePathKey(normalizedWorktree)
	}
	payload := "v2|" + normalizedRoot + "|" + normalizedBranch + "|" + normalizedHead + "|" + normalizedWorktree
	digest := sha1.Sum([]byte(payload))
	return hex.EncodeToString(digest[:])
}

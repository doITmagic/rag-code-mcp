package contract

import (
	"crypto/sha1"
	"encoding/hex"
	"path/filepath"
	"strings"
)

func DeriveWorkspaceID(root, branch, worktreeID string) string {
	normalizedRoot := strings.ToLower(filepath.Clean(strings.TrimSpace(root)))
	normalizedBranch := strings.ToLower(strings.TrimSpace(branch))
	normalizedWorktree := strings.ToLower(strings.TrimSpace(worktreeID))
	payload := normalizedRoot + "|" + normalizedBranch + "|" + normalizedWorktree
	digest := sha1.Sum([]byte(payload))
	return hex.EncodeToString(digest[:])
}

func DerivePathContextKey(root, branch, headSHA, worktreeID string) string {
	normalizedRoot := strings.ToLower(filepath.Clean(strings.TrimSpace(root)))
	normalizedBranch := strings.ToLower(strings.TrimSpace(branch))
	normalizedHead := strings.ToLower(strings.TrimSpace(headSHA))
	normalizedWorktree := strings.ToLower(strings.TrimSpace(worktreeID))
	payload := normalizedRoot + "|" + normalizedBranch + "|" + normalizedHead + "|" + normalizedWorktree
	digest := sha1.Sum([]byte(payload))
	return hex.EncodeToString(digest[:])
}

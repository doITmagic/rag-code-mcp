package workspace

import (
	"context"
	"errors"
	"os/exec"
	"strings"
)

var ErrNotARepository = errors.New("not a git repository")

// State captures the current Git context of a directory.
type State struct {
	Branch     string
	HeadSHA    string
	WorktreeID string
}

// GetState returns the current Git state for the given directory.
func GetState(ctx context.Context, root string) (*State, error) {
	// 1. Get branch
	branch, err := runGit(ctx, root, "rev-parse", "--abbrev-ref", "HEAD")
	if err != nil {
		return nil, ErrNotARepository
	}

	// 2. Get Head SHA
	head, err := runGit(ctx, root, "rev-parse", "HEAD")
	if err != nil {
		return nil, err
	}

	// 3. Get Worktree ID (unique per worktree/checkout)
	worktree, err := runGit(ctx, root, "rev-parse", "--git-common-dir")
	if err != nil {
		// Fallback for older git or non-worktree setups
		worktree = root
	}

	return &State{
		Branch:     strings.TrimSpace(branch),
		HeadSHA:    strings.TrimSpace(head),
		WorktreeID: strings.TrimSpace(worktree),
	}, nil
}

func runGit(ctx context.Context, dir string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return string(out), nil
}

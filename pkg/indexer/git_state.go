package indexer

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

var ErrWorkspaceChanged = errors.New("workspace Git state changed during indexing")

type gitStateKey struct{}
type gitState struct {
	root, branch, head string
	headPath           string
	headInfo           os.FileInfo
	snapshotErr        error
}

// WithGitState binds writes to the Git state used to choose the collection.
func WithGitState(ctx context.Context, root, branch, head string) context.Context {
	if branch == "" || head == "" {
		return ctx
	}
	expected := gitState{root: root, branch: branch, head: head}
	cmd := exec.CommandContext(ctx, "git", "rev-parse", "--git-path", "HEAD")
	cmd.Dir = root
	output, err := cmd.Output()
	expected.snapshotErr = err
	if err == nil {
		expected.headPath = strings.TrimSpace(string(output))
		if !filepath.IsAbs(expected.headPath) {
			expected.headPath = filepath.Join(root, expected.headPath)
		}
		expected.headInfo, expected.snapshotErr = os.Stat(expected.headPath)
	}
	return context.WithValue(ctx, gitStateKey{}, expected)
}

func checkWorkspace(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	expected, ok := ctx.Value(gitStateKey{}).(gitState)
	if !ok {
		return nil
	}
	if expected.snapshotErr != nil {
		return fmt.Errorf("%w: cannot snapshot Git HEAD: %v", ErrWorkspaceChanged, expected.snapshotErr)
	}
	cmd := exec.CommandContext(ctx, "git", "rev-parse", "--git-path", "index.lock", "HEAD", "--abbrev-ref", "HEAD")
	cmd.Dir = expected.root
	output, err := cmd.Output()
	if err != nil {
		return fmt.Errorf("%w: cannot read Git state: %v", ErrWorkspaceChanged, err)
	}
	parts := strings.Split(strings.TrimSpace(string(output)), "\n")
	if len(parts) != 3 {
		return fmt.Errorf("%w: incomplete Git state", ErrWorkspaceChanged)
	}
	lockPath := parts[0]
	if !filepath.IsAbs(lockPath) {
		lockPath = filepath.Join(expected.root, lockPath)
	}
	// Checkout changes working files before HEAD; its index lock covers that interval.
	if _, err := os.Stat(lockPath); !os.IsNotExist(err) {
		return fmt.Errorf("%w: Git index is locked or unavailable", ErrWorkspaceChanged)
	}
	// HEAD is replaced on checkout, even when two checkouts return to the same branch and commit.
	info, err := os.Stat(expected.headPath)
	if err != nil || !os.SameFile(expected.headInfo, info) || !expected.headInfo.ModTime().Equal(info.ModTime()) {
		return fmt.Errorf("%w: Git HEAD was replaced or is unavailable", ErrWorkspaceChanged)
	}
	branch := parts[2]
	if branch == "HEAD" {
		branch = "detached"
	}
	if parts[1] != expected.head || branch != expected.branch {
		return fmt.Errorf("%w: expected %s at %s, found %s at %s", ErrWorkspaceChanged, expected.branch, expected.head, branch, parts[1])
	}
	return nil
}

package indexer

import (
	"context"
	"errors"
	"fmt"
	"io"
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
	reflogPath         string
	reflogSize         int64
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
		expected.reflogPath = filepath.Join(filepath.Dir(expected.headPath), "logs", "HEAD")
		if reflog, err := os.Stat(expected.reflogPath); err == nil {
			expected.reflogSize = reflog.Size()
		}
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
	cmd := exec.CommandContext(ctx, "git", "rev-parse", "HEAD", "--abbrev-ref", "HEAD")
	cmd.Dir = expected.root
	output, err := cmd.Output()
	if err != nil {
		return fmt.Errorf("%w: cannot read Git state: %v", ErrWorkspaceChanged, err)
	}
	parts := strings.Split(strings.TrimSpace(string(output)), "\n")
	if len(parts) != 2 {
		return fmt.Errorf("%w: incomplete Git state", ErrWorkspaceChanged)
	}
	// index.lock is not checked: git status/add take it routinely and a crashed
	// Git leaves it behind, which would abort or block indexing indefinitely.
	// HEAD is replaced on checkout, even when two checkouts return to the same branch and commit.
	info, err := os.Stat(expected.headPath)
	replaced := err == nil && (!os.SameFile(expected.headInfo, info) || !expected.headInfo.ModTime().Equal(info.ModTime()))
	if err != nil || replaced && !onlySameBranchCheckouts(expected) {
		return fmt.Errorf("%w: Git HEAD was replaced or is unavailable", ErrWorkspaceChanged)
	}
	branch := parts[1]
	if branch == "HEAD" {
		branch = "detached"
	}
	// A commit, pull or reset on the same branch keeps the collection; the
	// watcher picks up any files it changed, so only the branch is compared.
	if branch != expected.branch {
		return fmt.Errorf("%w: expected %s at %s, found %s at %s", ErrWorkspaceChanged, expected.branch, expected.head, branch, parts[0])
	}
	return nil
}

// onlySameBranchCheckouts reports whether the HEAD reflog entries written
// since the snapshot explain a replaced HEAD without leaving the branch, as
// "checkout <current branch>" does. Any other checkout or a rebase may have
// passed through another branch and come back, so it does not count.
func onlySameBranchCheckouts(expected gitState) bool {
	f, err := os.Open(expected.reflogPath)
	if err != nil {
		return false
	}
	defer f.Close()
	if _, err := f.Seek(expected.reflogSize, io.SeekStart); err != nil {
		return false
	}
	data, err := io.ReadAll(f)
	if err != nil || len(data) == 0 {
		return false
	}
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		_, message, _ := strings.Cut(line, "\t")
		action, detail, _ := strings.Cut(message, ": ")
		if strings.HasPrefix(action, "rebase") {
			return false
		}
		if action == "checkout" {
			from, to, ok := strings.Cut(strings.TrimPrefix(detail, "moving from "), " to ")
			if !ok || from != to {
				return false
			}
		}
	}
	return true
}

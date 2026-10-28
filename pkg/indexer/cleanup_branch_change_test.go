package indexer

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

type cleanupSwitchStore struct {
	mockStore
	switchBranch func()
}

func (s *cleanupSwitchStore) DeleteByPrefix(context.Context, string, string, string) (int, error) {
	return 0, errors.New("force per-file fallback")
}

func (s *cleanupSwitchStore) DeleteByFilter(ctx context.Context, collection, field string, value interface{}) error {
	err := s.mockStore.DeleteByFilter(ctx, collection, field, value)
	if len(s.deletedFilters) == 1 {
		s.switchBranch()
	}
	return err
}

func TestInterruptedStaleCleanupPersistsEachDeletion(t *testing.T) {
	root, _, git := branchFixture(t)
	dir := filepath.Join(root, "removed")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	state := NewState()
	metadata := make(map[string]os.FileInfo)
	for _, name := range []string{"One", "Two"} {
		path := filepath.Join(dir, strings.ToLower(name)+".go")
		if err := os.WriteFile(path, []byte("package example\nfunc "+name+"() {}\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		metadata[path] = info
		state.UpdateFile(path, info)
	}
	git("add", "removed")
	git("commit", "-qm", "files for stale cleanup")
	head, err := exec.Command("git", "-C", root, "rev-parse", "HEAD").Output()
	if err != nil {
		t.Fatal(err)
	}
	statePath := StatePath(root, "baseline")
	if err := state.Save(statePath); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(dir); err != nil {
		t.Fatal(err)
	}
	store := &cleanupSwitchStore{switchBranch: func() { git("switch", "-q", "variant") }}
	service := NewService(&mockEmbedder{}, store)
	ctx := WithGitState(context.Background(), root, "baseline", strings.TrimSpace(string(head)))
	opts := Options{Language: "go", ExcludePatterns: []string{"a.go", "z.go"}}
	if err := service.IndexWorkspace(ctx, root, "baseline", opts); !errors.Is(err, ErrWorkspaceChanged) {
		t.Fatalf("expected checkout to abort per-file cleanup, got %v", err)
	}
	if len(store.deletedFilters) != 1 {
		t.Fatalf("expected exactly one deletion before checkout, got %v", store.deletedFilters)
	}
	deleted := store.deletedFilters[0]
	state, err = LoadState(statePath)
	if err != nil {
		t.Fatal(err)
	}
	if _, exists := state.GetFileState(deleted); exists {
		t.Fatal("saved state still marks the deleted vectors as indexed")
	}
	if len(state.Files) != 1 {
		t.Fatalf("untouched file state should remain, got %v", state.Files)
	}

	git("switch", "-q", "baseline")
	for path, info := range metadata {
		if err := os.Chtimes(path, info.ModTime(), info.ModTime()); err != nil {
			t.Fatal(err)
		}
	}
	repaired := &mockStore{}
	ctx = WithGitState(context.Background(), root, "baseline", strings.TrimSpace(string(head)))
	if err := NewService(&mockEmbedder{}, repaired).IndexWorkspace(ctx, root, "baseline", opts); err != nil {
		t.Fatal(err)
	}
	if len(repaired.upsertPoints) != 1 || repaired.upsertPoints[0].Payload["file_path"] != deleted {
		t.Fatalf("returning to baseline did not repair exactly the removed vectors: %v", repaired.upsertPoints)
	}
}

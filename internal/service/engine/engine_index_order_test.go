package engine

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/doITmagic/rag-code-mcp/internal/config"
	"github.com/doITmagic/rag-code-mcp/internal/service/search"
	"github.com/doITmagic/rag-code-mcp/pkg/indexer"
	_ "github.com/doITmagic/rag-code-mcp/pkg/parser/docs"
	_ "github.com/doITmagic/rag-code-mcp/pkg/parser/generic"
	"github.com/doITmagic/rag-code-mcp/pkg/storage"
	"github.com/doITmagic/rag-code-mcp/pkg/workspace/resolver"
)

type indexOrderStore struct {
	testStore
	created []string
	paths   []string
	onGo    func()
}

func (s *indexOrderStore) CreateCollection(_ context.Context, collection string, _ int) error {
	s.existing[collection] = true
	language := collection[strings.LastIndex(collection, "-")+1:]
	s.created = append(s.created, language)
	if language == "go" && s.onGo != nil {
		s.onGo()
	}
	return nil
}

func (s *indexOrderStore) Upsert(_ context.Context, _ string, points []storage.Point) (*storage.UpdateResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, point := range points {
		s.paths = append(s.paths, point.Payload["file_path"].(string))
	}
	return nil, nil
}

func TestWorkspaceIndexesSourceBeforeDocsAndSharesScan(t *testing.T) {
	root := t.TempDir()
	write := func(name, content string) string {
		t.Helper()
		path := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
		return path
	}
	write("main.go", "package main\nfunc Work() {}\n")
	for i := range 2 {
		write(fmt.Sprintf("source%d.py", i), "def work():\n    pass\n")
	}
	for i := range 5 {
		write(fmt.Sprintf("docs/page%d.md", i), "# Guide\nDocumentation.\n")
	}
	for i := range 6 {
		write(fmt.Sprintf("data/notes%d.txt", i), "Notes for this project.\n")
	}
	excluded := write("lucrari/result.json", "{\"result\": 1}")
	// Newly generated files during indexing must not cause a new tree walk for
	// every remaining language. A subsequent run will discover this file.
	lateDoc := filepath.Join(root, "late.md")
	store := &indexOrderStore{testStore: testStore{existing: map[string]bool{}}}
	store.onGo = func() { write("late.md", "# Later\nCreated during indexing.\n") }
	provider := &mockLLM{}
	cfg := &config.Config{Workspace: config.WorkspaceConfig{ExcludePatterns: []string{"lucrari"}}}
	eng := NewEngine(indexer.NewService(provider, store), search.NewService(provider, store), filepath.Join(t.TempDir(), "registry.json"), cfg)
	t.Cleanup(eng.Close)
	eng.SetResolver(resolver.New(resolver.Dependencies{Detector: &exactRootDetector{}, Registry: eng.registry}))

	if err := eng.IndexWorkspace(context.Background(), root, false); err != nil {
		t.Fatal(err)
	}
	if want := []string{"python", "go", "docs", "generic"}; !reflect.DeepEqual(store.created, want) {
		t.Fatalf("collection creation order = %v, want %v", store.created, want)
	}
	for _, path := range store.paths {
		if path == excluded || path == lateDoc {
			t.Fatalf("first run indexed a path outside its inventory: %s", path)
		}
	}
	status := eng.GetIndexStatus(root)
	for lang, count := range map[string]int{"go": 1, "python": 2, "docs": 5, "generic": 6} {
		if got := status.Languages[lang]; got.OnDisk != count || got.Processed != count {
			t.Errorf("%s progress = %+v, want on_disk=processed=%d", lang, got, count)
		}
	}

	before := len(store.paths)
	if err := eng.IndexWorkspace(context.Background(), root, false); err != nil {
		t.Fatal(err)
	}
	if got := store.paths[before:]; len(got) != 1 || got[0] != lateDoc {
		t.Fatalf("incremental run indexed %v, want only %s", got, lateDoc)
	}
}

// A directory moved into an excluded location (or out of the workspace) emits
// only its own vanished path. Its files must still lose their index entries.
func TestIndexFilesReconcilesDirectoryMovedIntoExcludedDir(t *testing.T) {
	root := t.TempDir()
	src := filepath.Join(root, "a", "sub", "x.go")
	if err := os.MkdirAll(filepath.Dir(src), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(src, []byte("package sub\nfunc X() {}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	store := &indexOrderStore{testStore: testStore{existing: map[string]bool{}}}
	provider := &mockLLM{}
	eng := NewEngine(indexer.NewService(provider, store), search.NewService(provider, store), filepath.Join(t.TempDir(), "registry.json"), &config.Config{})
	eng.SetResolver(resolver.New(resolver.Dependencies{Detector: &exactRootDetector{}, Registry: eng.registry}))
	t.Cleanup(eng.Close)
	ctx := context.Background()
	if err := eng.IndexWorkspace(ctx, root, false); err != nil {
		t.Fatal(err)
	}
	wctx, err := eng.DetectContextAsRoot(ctx, root, false)
	if err != nil {
		t.Fatal(err)
	}
	statePath := indexer.StatePath(root, wctx.CollectionName("go"))
	if st, _ := indexer.LoadState(statePath); st == nil || len(st.Files) != 1 {
		t.Fatalf("expected x.go tracked after initial index, got %+v", st)
	}

	if err := os.MkdirAll(filepath.Join(root, "tmp"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(filepath.Join(root, "a"), filepath.Join(root, "tmp", "a")); err != nil {
		t.Fatal(err)
	}
	// What the watcher delivers for this move: the vanished source and the
	// excluded destination parent.
	if err := eng.IndexFiles(ctx, root, []string{filepath.Join(root, "a"), filepath.Join(root, "tmp")}); err != nil {
		t.Fatal(err)
	}
	st, err := indexer.LoadState(statePath)
	if err != nil {
		t.Fatal(err)
	}
	if _, tracked := st.Files[src]; tracked {
		t.Fatalf("x.go still tracked after its directory moved into tmp/: %v", st.Files)
	}
}

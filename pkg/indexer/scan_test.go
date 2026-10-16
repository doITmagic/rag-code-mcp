package indexer

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestIndexWorkspaceReusesScanAndChecksCurrentMetadata(t *testing.T) {
	root := t.TempDir()
	write := func(name, content string) string {
		t.Helper()
		path := filepath.Join(root, name)
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
		return path
	}
	goFile := write("main.go", "package main\nfunc Work() {}\n")
	docFile := write("README.md", "# Before\nOriginal content.\n")
	lockfile := write("package-lock.json", "{}")
	createFile(t, filepath.Join(root, "lucrari", "data.json"))
	createFile(t, filepath.Join(root, "node_modules", "lib.js"))
	createFile(t, filepath.Join(root, "app.min.js"))
	store := &mockStore{}
	svc := NewService(&mockEmbedder{}, store)
	ctx := context.Background()
	excludes := []string{"lucrari/"}

	// Seed an unchanged doc and a previously indexed lockfile. Reusing the scan
	// must still notice a later doc edit and clean up the tracked exclusion.
	state := NewState()
	for _, path := range []string{docFile, lockfile} {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		state.UpdateFile(path, info)
	}
	if err := state.Save(StatePath(root, "docs")); err != nil {
		t.Fatal(err)
	}
	scan, err := svc.ScanWorkspace(ctx, root, excludes)
	if err != nil {
		t.Fatal(err)
	}
	if scan.Counts["go"] != 1 || scan.Counts["docs"] != 1 || scan.Counts["javascript"] != 0 {
		t.Fatalf("scan counts include excluded files: %v", scan.Counts)
	}
	if err := svc.IndexWorkspace(ctx, root, "go", Options{Language: "go", ExcludePatterns: excludes, Scan: scan}); err != nil {
		t.Fatal(err)
	}

	// Files created after the scan belong to the next run, including languages
	// that had no files initially. An empty inventory must not trigger a rewalk.
	lateGo := write("later.go", "package main\nfunc Later() {}\n")
	write("later.py", "def later():\n    pass\n")
	write("README.md", "# After\nUpdated documentation with different length.\n")
	for _, lang := range []string{"go", "python", "docs"} {
		if err := svc.IndexWorkspace(ctx, root, lang, Options{Language: lang, ExcludePatterns: excludes, Scan: scan}); err != nil {
			t.Fatal(err)
		}
	}
	paths := map[string]bool{}
	updatedDoc := false
	for _, point := range store.upsertPoints {
		paths[point.Payload["file_path"].(string)] = true
		if content, _ := point.Payload["content"].(string); strings.Contains(content, "Updated documentation") {
			updatedDoc = true
		}
	}
	if len(paths) != 2 || !paths[goFile] || !paths[docFile] || !updatedDoc {
		t.Fatalf("indexed paths = %v, updated doc = %t; want only original Go file and updated doc", paths, updatedDoc)
	}
	state, err = LoadState(StatePath(root, "docs"))
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := state.GetFileState(lockfile); ok {
		t.Fatal("tracked excluded file was retained in state")
	}

	// Direct callers without a scan still discover new files on the next run.
	if err := svc.IndexWorkspace(ctx, root, "go", Options{Language: "go", ExcludePatterns: excludes}); err != nil {
		t.Fatal(err)
	}
	state, err = LoadState(StatePath(root, "go"))
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := state.GetFileState(lateGo); !ok {
		t.Fatal("fresh run did not discover the new Go file")
	}
}

func TestIndexWorkspaceRejectsMismatchedScan(t *testing.T) {
	root := t.TempDir()
	svc := &Service{} // Validation must happen before any storage calls.
	scan, err := svc.ScanWorkspace(context.Background(), root, []string{"lucrari"})
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name     string
		root     string
		excludes []string
	}{
		{"different root", t.TempDir(), []string{"lucrari"}},
		{"different exclusions", root, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := svc.IndexWorkspace(context.Background(), tc.root, "go", Options{Scan: scan, ExcludePatterns: tc.excludes})
			if err == nil {
				t.Fatal("expected mismatched scan to be rejected")
			}
		})
	}
}

func TestScanWorkspaceCancellationAndMissingRoot(t *testing.T) {
	svc := &Service{}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := svc.ScanWorkspace(ctx, t.TempDir(), nil); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled scan returned %v", err)
	}
	if _, err := svc.ScanWorkspace(context.Background(), filepath.Join(t.TempDir(), "missing"), nil); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("missing root scan returned %v", err)
	}
}

package watch

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// On Windows "c:\users\x" is the home directory too; a case-sensitive check
// let it through, and registering it absorbed every workspace beneath it.
func TestIsInvalidRoot(t *testing.T) {
	home := filepath.Join(t.TempDir(), "Home")
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	cases := map[string]bool{
		home:                               true,
		home + string(os.PathSeparator):    true,
		os.TempDir():                       true,
		filepath.Join(home, "src", "proj"): false,
		filepath.Join(home, "proj"):        false,
	}
	if runtime.GOOS == "windows" {
		cases[strings.ToLower(home)] = true
		cases[strings.ToUpper(home)] = true
	}
	for path, want := range cases {
		if got := IsInvalidRoot(path); got != want {
			t.Errorf("IsInvalidRoot(%q) = %v, want %v", path, got, want)
		}
	}
}

// Events under excluded dirs must be dropped. On Windows a write inside
// .ragcode is reported as a change to ".ragcode" on the root watch; indexing
// writes there, so passing it on made every index run trigger the next one.
func TestIsExcludedPath(t *testing.T) {
	root := t.TempDir()
	fw, err := NewFileWatcher(root, Options{}, nil)
	if err != nil {
		t.Fatal(err)
	}

	cases := map[string]bool{
		filepath.Join(root, ".ragcode"):                          true,
		filepath.Join(root, ".ragcode", "index_status.json"):     true,
		filepath.Join(root, "sub", "node_modules", "x", "a.js"):  true,
		filepath.Join(root, ".git", "index"):                     true,
		filepath.Join(root, "main.go"):                           false,
		filepath.Join(root, ".goreleaser.yaml"):                  false, // hidden file: indexed
		filepath.Join(root, "sub", ".eslintrc.js"):               false,
		filepath.Join(root, ".github", "ci.yml"):                 true, // hidden dir: not indexed
		filepath.Join(root, "internal", "service", "engine.go"):  false,
		filepath.Join(filepath.Dir(root), "outside", ".ragcode"): false,
	}
	for path, want := range cases {
		if got := fw.isExcludedPath(path); got != want {
			t.Errorf("isExcludedPath(%q) = %v, want %v", path, got, want)
		}
	}
}

// Renaming a watched directory inside the workspace must not leave its
// subdirectories reporting events under the old path.
func TestRenamedDirectoryReportsNestedEventsUnderNewPath(t *testing.T) {
	root := t.TempDir()
	deep := filepath.Join(root, "a", "sub", "deep")
	if err := os.MkdirAll(deep, 0o700); err != nil {
		t.Fatal(err)
	}
	got := make(chan []string, 8)
	fw, err := NewFileWatcher(root, Options{Debounce: 20 * time.Millisecond}, func(_ context.Context, _ string, files []string) error {
		got <- files
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	fw.Start()
	t.Cleanup(fw.Stop)
	time.Sleep(50 * time.Millisecond)

	if err := os.Rename(filepath.Join(root, "a"), filepath.Join(root, "b")); err != nil {
		t.Fatal(err)
	}
	<-got // rename batch
	want := filepath.Join(root, "b", "sub", "deep", "x.go")
	if err := os.WriteFile(want, []byte("package deep\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	select {
	case files := <-got:
		if len(files) != 1 || files[0] != want {
			t.Fatalf("nested event reported as %v, want [%s]", files, want)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no event for file created under renamed directory")
	}
}

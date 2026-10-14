package watch

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
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

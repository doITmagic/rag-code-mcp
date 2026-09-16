package watch

import (
	"path/filepath"
	"testing"
)

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

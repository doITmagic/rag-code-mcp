package tools

import (
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestIsWithinRoot(t *testing.T) {
	root := filepath.FromSlash("/proj")
	cases := map[string]bool{
		filepath.FromSlash("/proj/a.go"):       true,
		filepath.FromSlash("/proj/sub/b.go"):   true,
		filepath.FromSlash("/proj"):            false,
		filepath.FromSlash("/project/a.go"):    false,
		filepath.FromSlash("/other/proj/a.go"): false,
	}
	for path, want := range cases {
		if got := isWithinRoot(root, path); got != want {
			t.Errorf("isWithinRoot(%q, %q) = %v, want %v", root, path, got, want)
		}
	}
	// Windows paths are case-insensitive; a lower-case drive letter in the
	// root used to make every result look outside the workspace.
	if runtime.GOOS == "windows" {
		if !isWithinRoot(`c:\proj`, `C:\proj\a.go`) {
			t.Error("drive letter case should not matter on Windows")
		}
		if !isWithinRoot(strings.ToUpper(`c:\proj`), `c:\proj\a.go`) {
			t.Error("path case should not matter on Windows")
		}
	}
}

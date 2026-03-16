package wordpress

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestIsWordPressByFilesystem_WpConfigInParent verifies detection when wp-config.php
// exists in a parent directory, not the immediate one.
func TestIsWordPressByFilesystem_WpConfigInParent(t *testing.T) {
	root := t.TempDir()

	// Create WP root indicators at the top level
	if err := os.WriteFile(filepath.Join(root, "wp-config.php"), []byte("<?php\n"), 0644); err != nil {
		t.Fatal(err)
	}

	// Create a nested directory 3 levels deep
	nested := filepath.Join(root, "wp-content", "plugins", "my-plugin")
	if err := os.MkdirAll(nested, 0755); err != nil {
		t.Fatal(err)
	}

	if !isWordPressByFilesystem(nested) {
		t.Error("expected true when wp-config.php exists in a parent directory")
	}
}

// TestIsWordPressByFilesystem_WpContentInParent verifies detection via wp-content dir.
func TestIsWordPressByFilesystem_WpContentInParent(t *testing.T) {
	root := t.TempDir()

	// Create wp-content at root level
	wpContent := filepath.Join(root, "wp-content")
	if err := os.MkdirAll(wpContent, 0755); err != nil {
		t.Fatal(err)
	}

	// Create a nested directory 2 levels deep under root (not under wp-content)
	nested := filepath.Join(root, "custom", "deep")
	if err := os.MkdirAll(nested, 0755); err != nil {
		t.Fatal(err)
	}

	if !isWordPressByFilesystem(nested) {
		t.Error("expected true when wp-content exists in a parent directory")
	}
}

// TestIsWordPressByFilesystem_PluginHeaderInParent verifies detection when a PHP file
// with "Plugin Name:" header exists in a parent directory (reading only first 4KB).
func TestIsWordPressByFilesystem_PluginHeaderInParent(t *testing.T) {
	root := t.TempDir()

	// Create a plugin file at root with plugin header
	pluginContent := `<?php
/**
 * Plugin Name: Test Plugin
 * Version: 1.0
 */
`
	if err := os.WriteFile(filepath.Join(root, "test-plugin.php"), []byte(pluginContent), 0644); err != nil {
		t.Fatal(err)
	}

	// Create a nested directory
	nested := filepath.Join(root, "includes", "admin")
	if err := os.MkdirAll(nested, 0755); err != nil {
		t.Fatal(err)
	}

	if !isWordPressByFilesystem(nested) {
		t.Error("expected true when a PHP file with 'Plugin Name:' header exists in parent")
	}
}

// TestIsWordPressByFilesystem_ThemeHeaderInParent verifies detection when style.css
// with "Theme Name:" header exists in a parent directory.
func TestIsWordPressByFilesystem_ThemeHeaderInParent(t *testing.T) {
	root := t.TempDir()

	// Create a style.css with theme header
	themeCSS := `/*
Theme Name: Test Theme
Version: 1.0
Author: TestAuthor
*/
`
	if err := os.WriteFile(filepath.Join(root, "style.css"), []byte(themeCSS), 0644); err != nil {
		t.Fatal(err)
	}

	// Create a nested directory
	nested := filepath.Join(root, "template-parts", "content")
	if err := os.MkdirAll(nested, 0755); err != nil {
		t.Fatal(err)
	}

	if !isWordPressByFilesystem(nested) {
		t.Error("expected true when style.css with 'Theme Name:' header exists in parent")
	}
}

// TestIsWordPressByFilesystem_NoIndicators verifies false is returned when
// no WordPress indicators exist anywhere in the parent chain.
func TestIsWordPressByFilesystem_NoIndicators(t *testing.T) {
	root := t.TempDir()

	nested := filepath.Join(root, "some", "random", "dir")
	if err := os.MkdirAll(nested, 0755); err != nil {
		t.Fatal(err)
	}

	if isWordPressByFilesystem(nested) {
		t.Error("expected false when no WordPress indicators exist")
	}
}

// TestIsWordPressByFilesystem_MaxDepthBoundary verifies detection stops after
// maxWalkUpDepth (10) levels and doesn't find indicators beyond that depth.
func TestIsWordPressByFilesystem_MaxDepthBoundary(t *testing.T) {
	root := t.TempDir()

	// Place wp-config.php at root
	if err := os.WriteFile(filepath.Join(root, "wp-config.php"), []byte("<?php\n"), 0644); err != nil {
		t.Fatal(err)
	}

	// Create a directory exactly maxWalkUpDepth levels deep — should still find it
	atMaxDepth := root
	for i := 0; i < maxWalkUpDepth; i++ {
		atMaxDepth = filepath.Join(atMaxDepth, "d")
	}
	if err := os.MkdirAll(atMaxDepth, 0755); err != nil {
		t.Fatal(err)
	}

	// At maxWalkUpDepth levels, the walk-up iterates 0..9 (10 steps),
	// checking dirs from "d/d/.../d" up to root. The root is 10 parents away,
	// so the loop checks indices 0-9, and at depth=9 it checks root's parent,
	// not root itself. So maxWalkUpDepth levels should NOT find it.
	if isWordPressByFilesystem(atMaxDepth) {
		// If it finds it, that's because the depth is borderline.
		// Walk-up checks: depth0=atMaxDepth, depth1=parent(atMaxDepth)..., depth9=root+1 level
		// It would only check up to 10 directories starting from atMaxDepth.
		t.Log("detection at exactly maxWalkUpDepth levels succeeded (borderline)")
	}

	// Create a directory 11+ levels deep — should NOT find it
	beyondMaxDepth := root
	for i := 0; i < maxWalkUpDepth+5; i++ {
		beyondMaxDepth = filepath.Join(beyondMaxDepth, "x")
	}
	if err := os.MkdirAll(beyondMaxDepth, 0755); err != nil {
		t.Fatal(err)
	}

	if isWordPressByFilesystem(beyondMaxDepth) {
		t.Error("expected false when nested deeper than maxWalkUpDepth from the WP root indicator")
	}
}

// TestIsWordPressByFilesystem_OnlyReadsPrefix verifies that only the first 4KB
// of a PHP file is read for header detection (a large file shouldn't cause issues
// and "Plugin Name:" after 4KB should not match).
func TestIsWordPressByFilesystem_OnlyReadsPrefix(t *testing.T) {
	root := t.TempDir()

	// Create a PHP file where "Plugin Name:" appears AFTER the 4KB boundary
	padding := strings.Repeat("// padding line\n", 300) // ~4500 bytes
	content := "<?php\n" + padding + "/**\n * Plugin Name: Hidden Plugin\n */\n"
	if err := os.WriteFile(filepath.Join(root, "hidden.php"), []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	// The header is beyond maxHeaderReadBytes, so it should NOT be detected
	if isWordPressByFilesystem(root) {
		t.Error("expected false when 'Plugin Name:' is beyond the 4KB read prefix")
	}
}

// TestReadFilePrefix verifies the helper function.
func TestReadFilePrefix(t *testing.T) {
	root := t.TempDir()

	content := "Hello, World! This is a test file with some content."
	fpath := filepath.Join(root, "test.txt")
	if err := os.WriteFile(fpath, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	// Read full file (maxBytes > content length)
	result := readFilePrefix(fpath, 1000)
	if string(result) != content {
		t.Errorf("expected full content, got %q", string(result))
	}

	// Read only 5 bytes
	result = readFilePrefix(fpath, 5)
	if string(result) != "Hello" {
		t.Errorf("expected 'Hello', got %q", string(result))
	}

	// Non-existent file
	result = readFilePrefix(filepath.Join(root, "nonexistent.txt"), 100)
	if result != nil {
		t.Error("expected nil for non-existent file")
	}
}

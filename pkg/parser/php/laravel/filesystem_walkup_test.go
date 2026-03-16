package laravel

import (
	"os"
	"path/filepath"
	"testing"
)

// TestIsLaravelProjectByPaths_ArtisanInParent verifies detection when the artisan
// file exists in a parent directory and the input is a deeply nested PHP file.
func TestIsLaravelProjectByPaths_ArtisanInParent(t *testing.T) {
	root := t.TempDir()

	// Create artisan file at root (strongest Laravel indicator)
	if err := os.WriteFile(filepath.Join(root, "artisan"), []byte("#!/usr/bin/env php\n"), 0644); err != nil {
		t.Fatal(err)
	}

	// Create a deeply nested file path like a real Laravel project
	controllerDir := filepath.Join(root, "app", "Http", "Controllers")
	if err := os.MkdirAll(controllerDir, 0755); err != nil {
		t.Fatal(err)
	}
	controllerFile := filepath.Join(controllerDir, "UserController.php")
	if err := os.WriteFile(controllerFile, []byte("<?php\n"), 0644); err != nil {
		t.Fatal(err)
	}

	if !IsLaravelProjectByPaths([]string{controllerFile}) {
		t.Error("expected true when artisan exists in a parent directory")
	}
}

// TestIsLaravelProjectByPaths_ComposerJsonInParent verifies detection when
// composer.json with laravel/framework exists in a parent directory.
func TestIsLaravelProjectByPaths_ComposerJsonInParent(t *testing.T) {
	root := t.TempDir()

	// Create composer.json with laravel/framework dependency
	composerJSON := `{
    "require": {
        "php": "^8.1",
        "laravel/framework": "^10.0"
    }
}`
	if err := os.WriteFile(filepath.Join(root, "composer.json"), []byte(composerJSON), 0644); err != nil {
		t.Fatal(err)
	}

	// Create a nested model file
	modelDir := filepath.Join(root, "app", "Models")
	if err := os.MkdirAll(modelDir, 0755); err != nil {
		t.Fatal(err)
	}
	modelFile := filepath.Join(modelDir, "User.php")
	if err := os.WriteFile(modelFile, []byte("<?php\n"), 0644); err != nil {
		t.Fatal(err)
	}

	if !IsLaravelProjectByPaths([]string{modelFile}) {
		t.Error("expected true when composer.json with laravel/framework exists in parent")
	}
}

// TestIsLaravelProjectByPaths_DirectoryInput verifies detection works when
// the input is a directory, not a file.
func TestIsLaravelProjectByPaths_DirectoryInput(t *testing.T) {
	root := t.TempDir()

	if err := os.WriteFile(filepath.Join(root, "artisan"), []byte("#!/usr/bin/env php\n"), 0644); err != nil {
		t.Fatal(err)
	}

	nested := filepath.Join(root, "app", "Http")
	if err := os.MkdirAll(nested, 0755); err != nil {
		t.Fatal(err)
	}

	if !IsLaravelProjectByPaths([]string{nested}) {
		t.Error("expected true when input is a directory with artisan in parent")
	}
}

// TestIsLaravelProjectByPaths_NoIndicators verifies false when no Laravel
// indicators exist anywhere in the parent chain.
func TestIsLaravelProjectByPaths_NoIndicators(t *testing.T) {
	root := t.TempDir()

	nested := filepath.Join(root, "some", "random", "dir")
	if err := os.MkdirAll(nested, 0755); err != nil {
		t.Fatal(err)
	}
	phpFile := filepath.Join(nested, "index.php")
	if err := os.WriteFile(phpFile, []byte("<?php\n"), 0644); err != nil {
		t.Fatal(err)
	}

	if IsLaravelProjectByPaths([]string{phpFile}) {
		t.Error("expected false when no Laravel indicators exist")
	}
}

// TestIsLaravelProjectByPaths_NonexistentPath verifies graceful handling of
// paths that don't exist on disk.
func TestIsLaravelProjectByPaths_NonexistentPath(t *testing.T) {
	if IsLaravelProjectByPaths([]string{"/nonexistent/path/to/file.php"}) {
		t.Error("expected false for nonexistent path")
	}
}

// TestIsLaravelRoot_MaxDepthBoundary verifies that isLaravelRoot respects
// the maxWalkUpDepth limit and doesn't traverse beyond it.
func TestIsLaravelRoot_MaxDepthBoundary(t *testing.T) {
	root := t.TempDir()

	// Place artisan at root
	if err := os.WriteFile(filepath.Join(root, "artisan"), []byte("#!/usr/bin/env php\n"), 0644); err != nil {
		t.Fatal(err)
	}

	// Create a directory beyond maxWalkUpDepth levels deep
	beyondMaxDepth := root
	for i := 0; i < maxWalkUpDepth+5; i++ {
		beyondMaxDepth = filepath.Join(beyondMaxDepth, "d")
	}
	if err := os.MkdirAll(beyondMaxDepth, 0755); err != nil {
		t.Fatal(err)
	}

	if isLaravelRoot(beyondMaxDepth) {
		t.Error("expected false when nested deeper than maxWalkUpDepth from artisan")
	}
}

// TestIsLaravelRoot_WithinMaxDepth verifies detection succeeds when artisan
// is within maxWalkUpDepth levels.
func TestIsLaravelRoot_WithinMaxDepth(t *testing.T) {
	root := t.TempDir()

	// Place artisan at root
	if err := os.WriteFile(filepath.Join(root, "artisan"), []byte("#!/usr/bin/env php\n"), 0644); err != nil {
		t.Fatal(err)
	}

	// Create a directory exactly 5 levels deep (well within limit)
	nested := root
	for i := 0; i < 5; i++ {
		nested = filepath.Join(nested, "level")
	}
	if err := os.MkdirAll(nested, 0755); err != nil {
		t.Fatal(err)
	}

	if !isLaravelRoot(nested) {
		t.Error("expected true when artisan is within maxWalkUpDepth levels")
	}
}

// TestIsLaravelRoot_ComposerJsonWithoutLaravel verifies that a composer.json
// without laravel/framework does not trigger detection.
func TestIsLaravelRoot_ComposerJsonWithoutLaravel(t *testing.T) {
	root := t.TempDir()

	// Create composer.json WITHOUT laravel/framework
	composerJSON := `{
    "require": {
        "php": "^8.1",
        "symfony/framework-bundle": "^6.0"
    }
}`
	if err := os.WriteFile(filepath.Join(root, "composer.json"), []byte(composerJSON), 0644); err != nil {
		t.Fatal(err)
	}

	if isLaravelRoot(root) {
		t.Error("expected false when composer.json doesn't contain laravel/framework")
	}
}

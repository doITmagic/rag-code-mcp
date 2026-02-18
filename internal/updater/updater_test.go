package updater

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestGetCachePath(t *testing.T) {
	tempDir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", tempDir)
	t.Setenv("AppData", tempDir)
	t.Setenv("HOME", tempDir)

	path, err := getCachePath()
	if err != nil {
		t.Fatalf("getCachePath failed: %v", err)
	}

	if path == "" {
		t.Error("Expected non-empty path")
	}

	dir := filepath.Dir(path)
	if _, err := os.Stat(dir); err != nil {
		t.Fatalf("Cache directory does not exist: %v", err)
	}
}

func TestSaveAndGetUpdateCache(t *testing.T) {
	tempDir := t.TempDir()

	// Isolate user config dir to the temp directory
	t.Setenv("XDG_CONFIG_HOME", tempDir)
	t.Setenv("AppData", tempDir)
	t.Setenv("HOME", tempDir) // Fallback for some systems

	info := &UpdateInfo{
		LatestVersion: "v1.2.3",
		Tag:           "v1.2.3",
		AssetURL:      "http://example.com/asset",
		ChecksumURL:   "http://example.com/checksum",
	}

	err := SaveUpdateCache(info)
	if err != nil {
		t.Fatalf("SaveUpdateCache failed: %v", err)
	}

	cached, err := GetCachedUpdate()
	if err != nil {
		t.Fatalf("GetCachedUpdate failed: %v", err)
	}

	if cached.LatestVersion != "v1.2.3" {
		t.Errorf("Expected version v1.2.3, got %s", cached.LatestVersion)
	}

	if time.Since(cached.LastCheck) > time.Minute {
		t.Error("LastCheck is too old")
	}
}

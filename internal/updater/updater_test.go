package updater

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestGetCachePath(t *testing.T) {
	path, err := getCachePath()
	if err != nil {
		t.Fatalf("getCachePath failed: %v", err)
	}

	if path == "" {
		t.Error("Expected non-empty path")
	}

	dir := filepath.Dir(path)
	info, err := os.Stat(dir)
	if err != nil {
		t.Fatalf("Cache directory does not exist: %v", err)
	}

	if !info.IsDir() {
		t.Errorf("Expected %s to be a directory", dir)
	}
}

func TestSaveAndGetUpdateCache(t *testing.T) {
	// Backup existing cache if any
	realPath, err := getCachePath()
	if err == nil {
		if _, err := os.Stat(realPath); err == nil {
			backupPath := realPath + ".bak"
			if err := os.Rename(realPath, backupPath); err != nil {
				t.Logf("Failed to backup existing cache: %v", err)
			}
			defer func() {
				if err := os.Rename(backupPath, realPath); err != nil {
					t.Logf("Failed to restore backup cache: %v", err)
				}
			}()
		}
	}

	info := &UpdateInfo{
		LatestVersion: "v1.2.3",
		Tag:           "v1.2.3",
		AssetURL:      "http://example.com/asset",
		ChecksumURL:   "http://example.com/checksum",
	}

	err = SaveUpdateCache(info)
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

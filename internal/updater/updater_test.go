package updater

import (
	"context"
	"os"
	"path/filepath"
	"strings"
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

func TestFetchRemoteStableModel(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping network test in short mode")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	model, err := fetchRemoteStableModel(ctx)
	if err != nil {
		if strings.Contains(err.Error(), "status 403") {
			t.Skip("Skipping: GitHub API rate limit (403)")
		}
		t.Fatalf("fetchRemoteStableModel failed: %v", err)
	}

	if model == "" {
		t.Error("Expected non-empty model string")
	}
	t.Logf("Fetched remote stable model from GitHub: %s", model)
}

func TestCheckForUpdates(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping network test in short mode")
	}

	tempDir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", tempDir)

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	// Testing with a very old version to trigger the update logic
	info, err := CheckForUpdates(ctx, "0.0.1", true)
	if err != nil {
		// GitHub API rate-limits unauthenticated requests (403) — skip in CI
		if strings.Contains(err.Error(), "status 403") {
			t.Skip("Skipping: GitHub API rate limit (403) — expected in CI without auth token")
		}
		t.Fatalf("CheckForUpdates failed: %v", err)
	}

	if info == nil {
		t.Log("CheckForUpdates returned nil (no releases yet or some other reason)")
	} else {
		t.Logf("Update found! LatestVersion: %s", info.LatestVersion)
		t.Logf("Tag: %s", info.Tag)
		t.Logf("AssetURL: %s", info.AssetURL)
		t.Logf("ChecksumURL: %s", info.ChecksumURL)
		t.Logf("RemoteStableModel: %s", info.RemoteStableModel)

		if info.LatestVersion == "" {
			t.Error("Expected LatestVersion to be populated")
		}
	}
}

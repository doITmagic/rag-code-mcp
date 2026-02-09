package updater

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/Masterminds/semver/v3"
)

type UpdateCache struct {
	LastCheck     time.Time   `json:"last_check"`
	LatestVersion string      `json:"latest_version"`
	UpdateDetails *UpdateInfo `json:"update_details,omitempty"`
}

var (
	cacheFile = "update_cache.json"
)

func getCachePath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return cacheFile // Fallback to CWD
	}
	configDir := filepath.Join(home, ".config", "rag-code-mcp")
	_ = os.MkdirAll(configDir, 0755)
	return filepath.Join(configDir, cacheFile)
}

func GetCachedUpdate() (*UpdateCache, error) {
	path := getCachePath()
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var cache UpdateCache
	if err := json.Unmarshal(data, &cache); err != nil {
		return nil, err
	}
	return &cache, nil
}

func SaveUpdateCache(info *UpdateInfo) error {
	cache := UpdateCache{
		LastCheck: time.Now(),
	}
	if info != nil {
		cache.LatestVersion = info.LatestVersion
		cache.UpdateDetails = info
	}

	data, err := json.Marshal(cache)
	if err != nil {
		return err
	}
	return os.WriteFile(getCachePath(), data, 0644)
}

const (
	GitHubOwner = "doITmagic"
	GitHubRepo  = "rag-code-mcp"
)

type UpdateInfo struct {
	LatestVersion string
	Tag           string
	AssetURL      string
	ChecksumURL   string
}

// CheckForUpdates queries GitHub for the latest release and compares it with the current version.
// If force is false, it returns cached results if available and less than 24 hours old.
func CheckForUpdates(currentVersion string, force bool) (*UpdateInfo, error) {
	if currentVersion == "" || currentVersion == "dev" {
		return nil, nil // Skip checks for dev versions
	}

	if !force {
		cache, err := GetCachedUpdate()
		if err == nil && cache != nil {
			// Check if cache is fresh (24h)
			if time.Since(cache.LastCheck) < 24*time.Hour {
				// Still need to compare versions because currentVersion might have changed
				if cache.UpdateDetails != nil {
					curr, err := semver.NewVersion(currentVersion)
					if err == nil {
						latest, err := semver.NewVersion(cache.UpdateDetails.LatestVersion)
						if err == nil && latest.GreaterThan(curr) {
							return cache.UpdateDetails, nil
						}
					}
				}
				return nil, nil // No new version in cache or already on latest
			}
		}
	}

	curr, err := semver.NewVersion(currentVersion)
	if err != nil {
		return nil, fmt.Errorf("invalid current version %q: %w", currentVersion, err)
	}

	url := fmt.Sprintf("https://api.github.com/repos/%s/%s/releases/latest", GitHubOwner, GitHubRepo)
	resp, err := http.Get(url)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch latest release: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("github api returned status %d", resp.StatusCode)
	}

	var release struct {
		TagName string `json:"tag_name"`
		Assets  []struct {
			Name               string `json:"name"`
			BrowserDownloadURL string `json:"browser_download_url"`
		} `json:"assets"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&release); err != nil {
		return nil, fmt.Errorf("failed to decode github response: %w", err)
	}

	latest, err := semver.NewVersion(release.TagName)
	if err != nil {
		return nil, fmt.Errorf("invalid latest version %q: %w", release.TagName, err)
	}

	var info *UpdateInfo
	if latest.GreaterThan(curr) {
		info = &UpdateInfo{
			LatestVersion: latest.String(),
			Tag:           release.TagName,
		}

		// Match asset for current platform
		archiveName := fmt.Sprintf("rag-code-mcp_%s_%s", runtime.GOOS, runtime.GOARCH)
		if runtime.GOOS == "windows" {
			archiveName += ".zip"
		} else {
			archiveName += ".tar.gz"
		}

		for _, asset := range release.Assets {
			if asset.Name == archiveName {
				info.AssetURL = asset.BrowserDownloadURL
			}
			if asset.Name == "checksums.txt" {
				info.ChecksumURL = asset.BrowserDownloadURL
			}
		}

		if info.AssetURL == "" {
			return nil, fmt.Errorf("no asset found for platform %s/%s", runtime.GOOS, runtime.GOARCH)
		}
	}

	// Always save cache after successful network call, even if info is nil (meaning we are on latest)
	_ = SaveUpdateCache(info)

	return info, nil
}

// DownloadAndVerify downloads the archive and checks its integrity.
func (info *UpdateInfo) DownloadAndVerify(destPath string) error {
	// 1. Download archive
	if err := downloadFile(info.AssetURL, destPath); err != nil {
		return fmt.Errorf("failed to download asset: %w", err)
	}

	// 2. Download checksums.txt
	if info.ChecksumURL == "" {
		return fmt.Errorf("no checksum URL available")
	}

	resp, err := http.Get(info.ChecksumURL)
	if err != nil {
		return fmt.Errorf("failed to download checksums: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("failed to download checksums: status %d", resp.StatusCode)
	}

	// 3. Verify hash
	expectedHash := ""
	scanner := bufio.NewScanner(resp.Body)
	assetName := getAssetName(info.AssetURL)
	for scanner.Scan() {
		line := scanner.Text()
		if strings.Contains(line, assetName) {
			parts := strings.Fields(line)
			if len(parts) > 0 {
				expectedHash = parts[0]
				break
			}
		}
	}

	if expectedHash == "" {
		return fmt.Errorf("checksum for %s not found in checksums.txt", assetName)
	}

	actualHash, err := calculateSHA256(destPath)
	if err != nil {
		return fmt.Errorf("failed to calculate checksum: %w", err)
	}

	if actualHash != expectedHash {
		return fmt.Errorf("checksum mismatch: expected %s, got %s", expectedHash, actualHash)
	}

	return nil
}

// ApplyUpdate extracts the archive and replaces the binaries, then runs the installer for environment sync.
func ApplyUpdate(archivePath string) error {
	self, err := os.Executable()
	if err != nil {
		return fmt.Errorf("could not determine executable path: %w", err)
	}

	// Resolve symlinks if any
	self, err = filepath.EvalSymlinks(self)
	if err != nil {
		return fmt.Errorf("could not resolve symlinks for %s: %w", self, err)
	}

	binDir := filepath.Dir(self)

	tempDir, err := os.MkdirTemp("", "ragcode-update-*")
	if err != nil {
		return fmt.Errorf("could not create temp dir: %w", err)
	}
	defer os.RemoveAll(tempDir)

	// Extraction logic
	if strings.HasSuffix(archivePath, ".tar.gz") {
		cmd := exec.Command("tar", "-xzf", archivePath, "-C", tempDir)
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("failed to extract tar.gz: %w", err)
		}
	} else if strings.HasSuffix(archivePath, ".zip") {
		return fmt.Errorf("zip extraction not yet implemented in updater")
	}

	// Binaries to update
	binaries := []string{"rag-code-mcp", "index-all", "ragcode-installer"}

	for _, binName := range binaries {
		srcPath := filepath.Join(tempDir, binName)
		if runtime.GOOS == "windows" {
			srcPath += ".exe"
		}

		// Skip if binary not in archive
		if _, err := os.Stat(srcPath); err != nil {
			continue
		}

		dstPath := filepath.Join(binDir, binName)
		if runtime.GOOS == "windows" {
			dstPath += ".exe"
		}

		// Swap logic for each binary
		oldPath := dstPath + ".old"

		// If the destination exists, move it to .old
		if _, err := os.Stat(dstPath); err == nil {
			_ = os.Remove(oldPath) // Remove existing .old if any
			if err := os.Rename(dstPath, oldPath); err != nil {
				return fmt.Errorf("failed to move %s to .old: %w", binName, err)
			}
		}

		if err := moveFile(srcPath, dstPath); err != nil {
			return fmt.Errorf("failed to replace %s: %w", binName, err)
		}

		if err := os.Chmod(dstPath, 0755); err != nil {
			return fmt.Errorf("failed to set executable permissions for %s: %w", binName, err)
		}

		_ = os.Remove(oldPath)
	}

	// Execute the new installer to sync models and IDE configs
	installerName := "ragcode-installer"
	if runtime.GOOS == "windows" {
		installerName += ".exe"
	}
	installerPath := filepath.Join(binDir, installerName)

	if _, err := os.Stat(installerPath); err == nil {
		// Run installer in background or wait for it?
		// We should wait to ensure everything is ready before informing the user.
		cmd := exec.Command(installerPath, "--upgrade", "--skip-build")
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("update binaries succeeded, but installer failed: %w", err)
		}
	}

	return nil
}

func moveFile(src, dst string) error {
	err := os.Rename(src, dst)
	if err == nil {
		return nil
	}

	// Cross-device rename fallback
	input, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	return os.WriteFile(dst, input, 0755)
}

func downloadFile(url, dest string) error {
	resp, err := http.Get(url)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("failed to download: status %d", resp.StatusCode)
	}

	out, err := os.Create(dest)
	if err != nil {
		return err
	}
	defer out.Close()

	_, err = io.Copy(out, resp.Body)
	return err
}

func calculateSHA256(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()

	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}

	return hex.EncodeToString(h.Sum(nil)), nil
}

func getAssetName(url string) string {
	parts := strings.Split(url, "/")
	return parts[len(parts)-1]
}

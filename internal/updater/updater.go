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

	"github.com/Masterminds/semver/v3"
)

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
func CheckForUpdates(currentVersion string) (*UpdateInfo, error) {
	if currentVersion == "" || currentVersion == "dev" {
		return nil, nil // Skip checks for dev versions
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

	if !latest.GreaterThan(curr) {
		return nil, nil // No update needed
	}

	info := &UpdateInfo{
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

// ApplyUpdate extracts the binary from the archive and replaces the current executable.
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

	tempDir, err := os.MkdirTemp("", "ragcode-update-*")
	if err != nil {
		return fmt.Errorf("could not create temp dir: %w", err)
	}
	defer os.RemoveAll(tempDir)

	binaryName := filepath.Base(self)

	// Extraction logic
	if strings.HasSuffix(archivePath, ".tar.gz") {
		cmd := exec.Command("tar", "-xzf", archivePath, "-C", tempDir)
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("failed to extract tar.gz: %w", err)
		}
	} else if strings.HasSuffix(archivePath, ".zip") {
		// Basic unzip command for windows/linux if available
		// Ideally use archive/zip in Go for better cross-platform
		return fmt.Errorf("zip extraction not yet implemented in updater")
	}

	newBinPath := filepath.Join(tempDir, binaryName)
	if _, err := os.Stat(newBinPath); err != nil {
		return fmt.Errorf("binary %s not found in archive", binaryName)
	}

	// Swap logic
	// On Linux/macOS we can rename the new binary over the old one
	// but it's safer to move the old one to a .old suffix and move the new one in.
	oldBinPath := self + ".old"
	if err := os.Rename(self, oldBinPath); err != nil {
		return fmt.Errorf("failed to move current binary to %s: %w", oldBinPath, err)
	}

	if err := moveFile(newBinPath, self); err != nil {
		// Rollback if possible
		_ = os.Rename(oldBinPath, self)
		return fmt.Errorf("failed to replace binary: %w", err)
	}

	if err := os.Chmod(self, 0755); err != nil {
		return fmt.Errorf("failed to set executable permissions: %w", err)
	}

	// Clean up old binary (might fail if still in use on some OSs, but that's fine)
	_ = os.Remove(oldBinPath)

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

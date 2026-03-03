package updater

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/Masterminds/semver/v3"
	"github.com/codeclysm/extract/v3"
	"gopkg.in/yaml.v3"
)

type UpdateCache struct {
	LastCheck     time.Time   `json:"last_check"`
	LatestVersion string      `json:"latest_version"`
	UpdateDetails *UpdateInfo `json:"update_details,omitempty"`
}

var (
	cacheFile  = "update_cache.json"
	cacheMutex sync.Mutex
)

func getCachePath() (string, error) {
	configDir, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("failed to get user config dir: %w", err)
	}

	appConfigDir := filepath.Join(configDir, "rag-code-mcp")
	if err := os.MkdirAll(appConfigDir, 0755); err != nil {
		return "", fmt.Errorf("failed to create config dir: %w", err)
	}

	return filepath.Join(appConfigDir, cacheFile), nil
}

func GetCachedUpdate() (*UpdateCache, error) {
	cacheMutex.Lock()
	defer cacheMutex.Unlock()

	path, err := getCachePath()
	if err != nil {
		return nil, err
	}
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
	cacheMutex.Lock()
	defer cacheMutex.Unlock()

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

	path, err := getCachePath()
	if err != nil {
		return err
	}

	// Write to a temporary file in the same directory and atomically replace
	dir := filepath.Dir(path)
	tmpFile, err := os.CreateTemp(dir, "update_cache_*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmpFile.Name()

	// Ensure cleanup if we fail before rename
	success := false
	defer func() {
		if !success {
			if err := tmpFile.Close(); err != nil {
				fmt.Fprintf(os.Stderr, "[WARN] Failed to close temporary cache file during cleanup: %v\n", err)
			}
			if err := os.Remove(tmpName); err != nil && !os.IsNotExist(err) {
				fmt.Fprintf(os.Stderr, "[WARN] Failed to remove temporary cache file during cleanup: %v\n", err)
			}
		}
	}()

	if _, err := tmpFile.Write(data); err != nil {
		return err
	}
	if err := tmpFile.Sync(); err != nil {
		return err
	}
	if err := tmpFile.Close(); err != nil {
		return err
	}

	// On success, rename the temp file to the final path (atomic on same filesystem).
	// On Windows, os.Rename may not reliably replace an existing file, so remove it first.
	if runtime.GOOS == "windows" {
		if _, err := os.Stat(path); err == nil {
			if err := os.Remove(path); err != nil {
				return fmt.Errorf("failed to remove existing cache file: %w", err)
			}
		}
	}
	if err := os.Rename(tmpName, path); err != nil {
		return err
	}

	// Set restrictive permissions; treat as best-effort if it fails (notably on some filesystems or Windows)
	if err := os.Chmod(path, 0600); err != nil {
		fmt.Fprintf(os.Stderr, "[WARN] Failed to set restrictive permissions on cache: %v\n", err)
	}

	success = true
	return nil
}

const (
	GitHubOwner = "doITmagic"
	GitHubRepo  = "rag-code-mcp"
)

type UpdateInfo struct {
	LatestVersion     string
	Tag               string
	AssetURL          string
	ChecksumURL       string
	RemoteStableModel string
}

// CheckForUpdates queries GitHub for the latest release and compares it with the current version.
// If force is false, it returns cached results if available and less than 24 hours old.
func CheckForUpdates(ctx context.Context, currentVersion string, force bool) (*UpdateInfo, error) {
	if currentVersion == "" || currentVersion == "dev" {
		return nil, nil // Skip checks for dev versions
	}

	if !force {
		cache, err := GetCachedUpdate()
		if err == nil && cache != nil {
			// Check if cache is fresh (24h)
			if time.Since(cache.LastCheck) < 24*time.Hour {
				// Still need to compare versions because currentVersion might have changed
				curr, errCurr := semver.NewVersion(currentVersion)
				if cache.UpdateDetails != nil {
					latest, errLatest := semver.NewVersion(cache.UpdateDetails.LatestVersion)
					if errCurr == nil && errLatest == nil {
						if latest.GreaterThan(curr) {
							return cache.UpdateDetails, nil
						}
						// Already on latest (or newer) version according to cache
						return nil, nil
					}
					// If semver parsing fails for current or cached version, treat as cache miss
					// and fall through to the network-based check below.
				} else if errCurr == nil {
					// Fresh cache with no update details means no update was available last check
					return nil, nil
				}
			}
		}
	}

	curr, err := semver.NewVersion(currentVersion)
	if err != nil {
		return nil, fmt.Errorf("invalid current version %q: %w", currentVersion, err)
	}

	url := fmt.Sprintf("https://api.github.com/repos/%s/%s/releases/latest", GitHubOwner, GitHubRepo)

	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
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

	// Always record the latest version/tag so callers can cache this information
	// even when no update is required. Additional update details (asset URLs,
	// checksums) are only populated when a newer version is actually available.
	info := &UpdateInfo{
		LatestVersion: latest.String(),
		Tag:           release.TagName,
	}

	updateAvailable := latest.GreaterThan(curr)
	if updateAvailable {
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

	// Always fetch remote stable model to show in tool results
	if remoteModel, err := fetchRemoteStableModel(ctx); err == nil {
		info.RemoteStableModel = remoteModel
	}

	// Always save cache after successful network call, including when no update is available.
	if err := SaveUpdateCache(info); err != nil {
		// Log error but don't fail the update check itself
		fmt.Fprintf(os.Stderr, "[WARN] Failed to save update cache: %v\n", err)
	}

	if updateAvailable {
		return info, nil
	}
	return nil, nil
}

// DownloadAndVerify downloads the archive and checks its integrity.
func (info *UpdateInfo) DownloadAndVerify(ctx context.Context, destPath string) error {
	// 1. Download archive
	if err := downloadFile(ctx, info.AssetURL, destPath); err != nil {
		return fmt.Errorf("failed to download asset: %w", err)
	}

	// 2. Download checksums.txt
	if info.ChecksumURL == "" {
		return fmt.Errorf("no checksum URL available")
	}

	req, err := http.NewRequestWithContext(ctx, "GET", info.ChecksumURL, nil)
	if err != nil {
		return fmt.Errorf("failed to create checksum request: %w", err)
	}

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
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
	if err := extractArchive(archivePath, tempDir); err != nil {
		return fmt.Errorf("failed to extract archive: %w", err)
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
		if runtime.GOOS == "windows" {
			// Fallback: write as .new and tell user
			newBinPermanent := self + ".new"
			if err := moveFile(newBinPath, newBinPermanent); err != nil {
				return fmt.Errorf("failed to write new binary: %w", err)
			}
			return fmt.Errorf("could not replace running binary on Windows: %w. New version saved to %s. Please close the server and rename it manually.", err, filepath.Base(newBinPermanent))
		}
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

	// Oprirea fortata a proceselor existente pentru a permite restartul
	// Adaugam un delay pentru a permite MCP-ului sa trimita raspunsul de succes inapoi catre client
	go func() {
		time.Sleep(1 * time.Second)
		StopRunningProcess(self)
		os.Exit(0)
	}()

	return nil
}

func fetchRemoteStableModel(ctx context.Context) (string, error) {
	// We read the raw config.yaml from the main branch to find the current default/stable embedding model
	url := fmt.Sprintf("https://raw.githubusercontent.com/%s/%s/main/config.yaml", GitHubOwner, GitHubRepo)
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return "", err
	}

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("failed to fetch remote config: status %d", resp.StatusCode)
	}

	// Limit response to 64KB to prevent decompression bombs or malicious large payloads.
	limitedBody := io.LimitReader(resp.Body, 64*1024)

	var cfg struct {
		LLM struct {
			OllamaEmbed string `yaml:"ollama_embed"`
		} `yaml:"llm"`
	}

	if err := yaml.NewDecoder(limitedBody).Decode(&cfg); err != nil {
		return "", fmt.Errorf("failed to parse remote config.yaml: %w", err)
	}

	if cfg.LLM.OllamaEmbed == "" {
		return "", fmt.Errorf("ollama_embed key not found or empty in remote config.yaml")
	}

	// Validate model name: allow alphanum, hyphens, dots, slashes, colons (tag separator).
	// Rejects anything that looks like shell injection or corrupted data.
	var validModel = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9./_-]*(:[a-zA-Z0-9][a-zA-Z0-9._-]*)?$`)
	if !validModel.MatchString(cfg.LLM.OllamaEmbed) {
		return "", fmt.Errorf("remote ollama_embed value %q is not a valid model name", cfg.LLM.OllamaEmbed)
	}

	return cfg.LLM.OllamaEmbed, nil
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

func downloadFile(ctx context.Context, url, dest string) error {
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return err
	}

	// Longer timeout for binary download
	client := &http.Client{Timeout: 5 * time.Minute}
	resp, err := client.Do(req)
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

	_, copyErr := io.Copy(out, resp.Body)
	closeErr := out.Close()

	if copyErr != nil {
		return copyErr
	}
	return closeErr
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

// extractArchive extracts a .tar.gz or .zip archive to dest using codeclysm/extract
// which handles ZipSlip prevention and symlink safety across all platforms.
func extractArchive(src, dest string) error {
	f, err := os.Open(src)
	if err != nil {
		return err
	}
	defer f.Close()
	return extract.Archive(context.Background(), f, dest, nil)
}

// StopRunningProcess attempts to forcefully close any existing processes using the provided binary path.
func StopRunningProcess(binPath string) {
	if _, err := os.Stat(binPath); os.IsNotExist(err) {
		return
	}

	if runtime.GOOS == "windows" {
		_ = exec.Command("taskkill", "/F", "/IM", filepath.Base(binPath)).Run()
		time.Sleep(500 * time.Millisecond)
		return
	}

	// 1. Precise kill using full path
	_ = exec.Command("pkill", "-f", binPath).Run()

	// 2. Fallback using lsof to find PIDs mapping this binary
	if _, err := exec.LookPath("lsof"); err == nil {
		cmd := exec.Command("lsof", "-t", binPath)
		if output, err := cmd.Output(); err == nil {
			pids := strings.Fields(string(output))
			for _, pid := range pids {
				_ = exec.Command("kill", "-9", pid).Run()
			}
		}
	}

	time.Sleep(500 * time.Millisecond)
}

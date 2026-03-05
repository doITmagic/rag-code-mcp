package skills

import (
	"archive/tar"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	defaultRegistryURL = "https://raw.githubusercontent.com/doITmagic/ai-agent-skills/main/registry.json"
	defaultRepoOwner   = "doITmagic"
	defaultRepoName    = "ai-agent-skills"
	defaultRepoBranch  = "main"
)

// RemoteSkillInfo represents a skill entry from the remote registry.json
type RemoteSkillInfo struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Version     string   `json:"version"`
	Author      string   `json:"author"`
	Tags        []string `json:"tags"`
	Path        string   `json:"path"` // e.g. "skills/oxygen-builder"
}

// RemoteRegistry represents the parsed registry.json
type RemoteRegistry struct {
	Version  string            `json:"version"`
	Registry string            `json:"registry"`
	Skills   []RemoteSkillInfo `json:"skills"`
}

// httpClient is a package-level client with sensible timeouts.
var httpClient = &http.Client{Timeout: 30 * time.Second}

// FetchRemoteRegistry downloads and parses the registry.json from GitHub.
func FetchRemoteRegistry() (*RemoteRegistry, error) {
	return FetchRemoteRegistryFromURL(defaultRegistryURL)
}

// FetchRemoteRegistryFromURL downloads and parses a registry.json from the given URL.
func FetchRemoteRegistryFromURL(registryURL string) (*RemoteRegistry, error) {
	resp, err := httpClient.Get(registryURL)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch registry from %s: %w", registryURL, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("registry fetch returned status %d from %s", resp.StatusCode, registryURL)
	}

	var registry RemoteRegistry
	if err := json.NewDecoder(resp.Body).Decode(&registry); err != nil {
		return nil, fmt.Errorf("failed to decode registry JSON: %w", err)
	}

	return &registry, nil
}

// downloadSkillFromGitHub downloads a specific skill from a GitHub repository
// by fetching the full repo tarball and extracting only the required skill subtree.
// owner/repoName/branch identify the GitHub repo. skillPath is the path within
// the repo (e.g. "skills/oxygen-builder"). destDir is the local filesystem destination.
func downloadSkillFromGitHub(owner, repoName, branch, skillPath, destDir string) error {
	if branch == "" {
		branch = "main"
	}
	tarURL := fmt.Sprintf(
		"https://github.com/%s/%s/archive/refs/heads/%s.tar.gz",
		owner, repoName, branch,
	)

	resp, err := httpClient.Get(tarURL)
	if err != nil {
		return fmt.Errorf("failed to download skill archive: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("skill archive download returned status %d", resp.StatusCode)
	}

	return extractSkillFromTarGz(resp.Body, skillPath, destDir)
}

// extractSkillFromTarGz reads a .tar.gz stream and extracts the subtree
// that matches the given skillPath prefix within the archive.
// Files are written to destDir preserving their relative structure.
func extractSkillFromTarGz(r io.Reader, skillPath, destDir string) error {
	gz, err := gzip.NewReader(r)
	if err != nil {
		return fmt.Errorf("failed to create gzip reader: %w", err)
	}
	defer gz.Close()

	tr := tar.NewReader(gz)

	topLevelDir := ""
	targetPrefix := ""
	extractedCount := 0

	for {
		header, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("error reading tar archive: %w", err)
		}

		// Skip PAX extended headers and any special metadata entries.
		// GitHub tarballs include a pax_global_header as first entry.
		if header.Typeflag == tar.TypeXGlobalHeader || header.Typeflag == tar.TypeXHeader {
			continue
		}
		if strings.HasPrefix(header.Name, "pax_global_header") || strings.HasPrefix(header.Name, "@PaxHeader") {
			continue
		}

		// Determine top-level directory from the first real entry, then compute prefix.
		if topLevelDir == "" {
			parts := strings.SplitN(header.Name, "/", 2)
			topLevelDir = parts[0] + "/"
			targetPrefix = topLevelDir + skillPath + "/"
		}

		if !strings.HasPrefix(header.Name, targetPrefix) {
			continue
		}

		// Compute relative path inside the skill folder
		relPath := strings.TrimPrefix(header.Name, targetPrefix)
		if relPath == "" {
			// This is the skill directory itself
			if err := os.MkdirAll(destDir, 0755); err != nil {
				return err
			}
			continue
		}

		// Security: prevent path traversal
		targetPath := filepath.Join(destDir, filepath.FromSlash(relPath))
		if !strings.HasPrefix(targetPath, filepath.Clean(destDir)+string(os.PathSeparator)) {
			return fmt.Errorf("invalid file path in archive: %s", header.Name)
		}

		switch header.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(targetPath, 0755); err != nil {
				return fmt.Errorf("failed to create directory %s: %w", targetPath, err)
			}

		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(targetPath), 0755); err != nil {
				return err
			}
			f, err := os.OpenFile(targetPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, os.FileMode(header.Mode))
			if err != nil {
				return fmt.Errorf("failed to create file %s: %w", targetPath, err)
			}
			if _, err := io.Copy(f, tr); err != nil {
				f.Close()
				return fmt.Errorf("failed to write file %s: %w", targetPath, err)
			}
			f.Close()
			extractedCount++
		}
	}

	if extractedCount == 0 {
		return fmt.Errorf("skill path '%s' not found in archive — check the skill ID or registry path", skillPath)
	}

	return nil
}

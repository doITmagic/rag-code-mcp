package main

import (
	"archive/tar"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/doITmagic/coderag-mcp/internal/healthcheck"
)

const (
	repoSlug   = "doITmagic/coderag-mcp"
	releaseURL = "https://github.com/" + repoSlug + "/releases/latest/download/coderag-linux.tar.gz"
	installDir = ".local/share/coderag"
	binDir     = "bin"
)

type colors struct {
	blue   string
	green  string
	yellow string
	red    string
	reset  string
}

var c = colors{
	blue:   "\033[0;34m",
	green:  "\033[0;32m",
	yellow: "\033[1;33m",
	red:    "\033[0;31m",
	reset:  "\033[0m",
}

func log(msg string)     { fmt.Printf("%s==> %s%s\n", c.blue, msg, c.reset) }
func success(msg string) { fmt.Printf("%s✓ %s%s\n", c.green, msg, c.reset) }
func warn(msg string)    { fmt.Printf("%s! %s%s\n", c.yellow, msg, c.reset) }
func fail(msg string)    { fmt.Printf("%s✗ %s%s\n", c.red, msg, c.reset); os.Exit(1) }

func requireCommands(cmds ...string) {
	for _, cmd := range cmds {
		if _, err := exec.LookPath(cmd); err != nil {
			fail(fmt.Sprintf("Command '%s' is required for the installer.", cmd))
		}
	}
}

func homeDir() string {
	if h := os.Getenv("HOME"); h != "" {
		return h
	}
	fail("Could not determine HOME directory")
	return ""
}

func downloadFile(url, dest string) error {
	resp, err := http.Get(url)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	f, err := os.Create(dest)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = io.Copy(f, resp.Body)
	return err
}

func extractTarGz(src, dst string) error {
	f, err := os.Open(src)
	if err != nil {
		return err
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return err
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
		target := filepath.Join(dst, hdr.Name)
		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0755); err != nil {
				return err
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(target), 0755); err != nil {
				return err
			}
			f, err := os.OpenFile(target, os.O_CREATE|os.O_RDWR, os.FileMode(hdr.Mode))
			if err != nil {
				return err
			}
			if _, err := io.Copy(f, tr); err != nil {
				f.Close()
				return err
			}
			f.Close()
		}
	}
	return nil
}

func installFromRelease(tmpDir string) error {
	archive := filepath.Join(tmpDir, "release.tar.gz")
	log("Downloading CodeRAG release (may take a while)...")
	if err := downloadFile(releaseURL, archive); err != nil {
		return fmt.Errorf("could not download release: %w", err)
	}
	extractDir := filepath.Join(tmpDir, "extract")
	if err := extractTarGz(archive, extractDir); err != nil {
		return fmt.Errorf("extraction failed: %w", err)
	}
	entries, err := os.ReadDir(extractDir)
	if err != nil || len(entries) == 0 {
		return fmt.Errorf("no valid content found in downloaded release")
	}
	root := filepath.Join(extractDir, entries[0].Name())
	home := homeDir()
	targetInstallDir := filepath.Join(home, installDir)
	targetBinDir := filepath.Join(targetInstallDir, binDir)
	if err := os.MkdirAll(targetBinDir, 0755); err != nil {
		return err
	}
	// Copy binaries
	for _, bin := range []string{"coderag-mcp", "index-all"} {
		src := filepath.Join(root, "bin", bin)
		dst := filepath.Join(targetBinDir, bin)
		if _, err := os.Stat(src); err != nil {
			return fmt.Errorf("missing binary %s in release", bin)
		}
		if err := os.Rename(src, dst); err != nil {
			return err
		}
	}
	// Copy scripts/config
	for _, f := range []string{"start.sh", "config.yaml", "install.sh"} {
		src := filepath.Join(root, f)
		dst := filepath.Join(targetInstallDir, f)
		if _, err := os.Stat(src); err == nil {
			if err := os.Rename(src, dst); err != nil {
				return err
			}
		}
	}
	return nil
}

func buildLocal() error {
	log("Local build – compiling binaries...")
	home := homeDir()
	targetInstallDir := filepath.Join(home, installDir)
	targetBinDir := filepath.Join(targetInstallDir, binDir)
	if err := os.MkdirAll(targetBinDir, 0755); err != nil {
		return err
	}
	// Detect repo directory (assuming running from repo)
	repoRoot, err := filepath.Abs(".")
	if err != nil {
		return err
	}
	// Build coderag-mcp
	cmd := exec.Command("go", "build", "-o", filepath.Join(targetBinDir, "coderag-mcp"), "./cmd/coderag-mcp")
	cmd.Dir = repoRoot
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("build coderag-mcp failed: %w\n%s", err, out)
	}
	// Build index-all
	cmd = exec.Command("go", "build", "-o", filepath.Join(targetBinDir, "index-all"), "./cmd/index-all")
	cmd.Dir = repoRoot
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("build index-all failed: %w\n%s", err, out)
	}
	// Copy scripts/config from repo
	for _, f := range []string{"start.sh", "config.yaml", "install.sh"} {
		src := filepath.Join(repoRoot, f)
		dst := filepath.Join(targetInstallDir, f)
		if _, err := os.Stat(src); err == nil {
			if err := exec.Command("cp", src, dst).Run(); err != nil {
				return err
			}
		}
	}
	return nil
}

func ensurePath() {
	home := homeDir()
	binPath := filepath.Join(home, installDir, binDir)
	profile := filepath.Join(home, ".bashrc")
	if runtime.GOOS == "windows" {
		profile = filepath.Join(home, ".profile")
	}
	entry := fmt.Sprintf("export PATH=\"%s:$PATH\"", binPath)
	// Check if PATH is already added
	if data, err := os.ReadFile(profile); err == nil {
		if strings.Contains(string(data), binPath) {
			return
		}
	}
	f, err := os.OpenFile(profile, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		warn(fmt.Sprintf("could not update %s: %v", profile, err))
		return
	}
	defer f.Close()
	fmt.Fprintf(f, "%s\n", entry)
	success(fmt.Sprintf("PATH updated in %s (reload shell to apply)", profile))
}

func configureMCPClient(targetPath, label string) {
	home := homeDir()
	binPath := filepath.Join(home, installDir, binDir, "coderag-mcp")
	path := filepath.Join(home, targetPath)
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		warn(fmt.Sprintf("could not create directory for %s: %v", path, err))
		return
	}
	config := map[string]interface{}{}
	if data, err := os.ReadFile(path); err == nil {
		if err := json.Unmarshal(data, &config); err != nil {
			warn(fmt.Sprintf("could not parse config at %s: %v", path, err))
		}
	}

	// Check if mcpServers section exists
	mcpServers := map[string]interface{}{}
	if servers, exists := config["mcpServers"]; exists {
		if serversMap, ok := servers.(map[string]interface{}); ok {
			mcpServers = serversMap
		}
	}

	// Add coderag only to mcpServers (not top-level)
	mcpServers["coderag"] = map[string]interface{}{
		"command": binPath,
		"args":    []string{},
		"env": map[string]string{
			"OLLAMA_BASE_URL": "http://localhost:11434",
			"OLLAMA_MODEL":    "phi3:medium",
			"OLLAMA_EMBED":    "nomic-embed-text",
			"QDRANT_URL":      "http://localhost:6333",
		},
	}

	// Update config with mcpServers
	config["mcpServers"] = mcpServers

	// Remove top-level coderag entry if exists (to avoid duplicates)
	delete(config, "coderag")

	data, _ := json.MarshalIndent(config, "", "  ")
	if err := os.WriteFile(path, append(data, '\n'), 0644); err == nil {
		success(fmt.Sprintf("MCP Config updated for %s: %s", label, path))
	} else {
		warn(fmt.Sprintf("could not write MCP config for %s: %v", label, err))
	}
}

func runSetupOnce() {
	home := homeDir()
	installDir := filepath.Join(home, installDir)
	startScript := filepath.Join(installDir, "start.sh")
	log("Checking and starting required services...")

	// Check if services are already running
	results := healthcheck.CheckAll("http://localhost:11434", "http://localhost:6333")
	allHealthy := true
	for _, result := range results {
		if result.Status != "healthy" {
			allHealthy = false
			break
		}
	}

	if allHealthy {
		log("Services already running, starting MCP server...")
		// Start MCP server in background
		cmd := exec.Command(startScript)
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		if err := cmd.Run(); err != nil {
			warn(fmt.Sprintf("Could not start MCP server: %v", err))
		}
		warn(fmt.Sprintf("Run manually: %s", startScript))
	} else {
		log("Services not ready, running full start.sh...")
		// Run full start.sh (with services + MCP)
		cmd := exec.Command(startScript)
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		if err := cmd.Run(); err != nil {
			warn(fmt.Sprintf("Could not start services: %v", err))
		}
		warn(fmt.Sprintf("Run manually: %s", startScript))
	}
}

func main() {
	requireCommands("go", "tar", "python3")
	tmpDir, err := os.MkdirTemp("", "coderag-install-*")
	if err != nil {
		fail(fmt.Sprintf("could not create temp directory: %v", err))
	}
	defer os.RemoveAll(tmpDir)

	// Try downloading release; if fails, fall back to local build
	if err := installFromRelease(tmpDir); err != nil {
		warn(fmt.Sprintf("Release could not be downloaded: %v", err))
		warn("Falling back to local build...")
		if err := buildLocal(); err != nil {
			fail(fmt.Sprintf("local build failed: %v", err))
		}
	}
	ensurePath()
	configureMCPClient(".codeium/windsurf/mcp_config.json", "Windsurf")
	configureMCPClient(".cursor/mcp.config.json", "Cursor")
	runSetupOnce()

	home := homeDir()
	binPath := filepath.Join(home, installDir, binDir, "coderag-mcp")
	installDirPath := filepath.Join(home, installDir)
	fmt.Printf("\n%sInstallation complete!%s\n", c.green, c.reset)
	fmt.Printf("%s────────────────────────────────────────────%s\n", c.blue, c.reset)
	fmt.Printf("Binary:       %s\n", binPath)
	fmt.Printf("Start script: %s\n", filepath.Join(installDirPath, "start.sh"))
	fmt.Printf("MCP Config:   ~/.codeium/windsurf/mcp_config.json, ~/.cursor/mcp.config.json\n")
	fmt.Printf("\nTo start server manually:\n")
	fmt.Printf("  %s\n", filepath.Join(installDirPath, "start.sh"))
}

package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/codeclysm/extract/v3"
	"github.com/doITmagic/rag-code-mcp/internal/config"
	"github.com/doITmagic/rag-code-mcp/internal/healthcheck"
	"github.com/doITmagic/rag-code-mcp/internal/uninstall"
	"github.com/doITmagic/rag-code-mcp/internal/updater"
)

var (
	ollamaMode    = flag.String("ollama", "local", "Mode for Ollama: 'local' (use existing) or 'docker' (run container)")
	qdrantMode    = flag.String("qdrant", "docker", "Mode for Qdrant: 'docker' (run container) or 'remote' (use existing URL)")
	gpu           = flag.Bool("gpu", true, "Enable GPU support for Docker containers")
	upgradeFlag   = flag.Bool("upgrade", false, "Upgrade existing installation")
	uninstallFlag = flag.Bool("uninstall", false, "Uninstall the application")
	assumeYes     = flag.Bool("y", true, "Automatic yes to prompts (non-interactive)")
	idesFlag      = flag.String("ides", "auto", "Comma-separated IDE list to configure (auto, vs-code, claude, claude-cli, cursor, windsurf, antigravity, gemini-cli, zed)")
	transportFlag = flag.String("transport", "auto", "MCP transport: 'auto' (SSE if server running, else stdio), 'stdio' (binary), 'sse' (URL)")
	ssePortFlag   = flag.Int("sse-port", 3000, "Port where rag-code-mcp SSE server listens (used for --transport=sse|auto)")
)

const (
	installDirName = ".ragcode"
	binDirName     = "bin"
)

func main() {
	flag.Parse()
	printBanner()

	if *uninstallFlag {
		uninstall.RunUninstall()
		return
	}

	if *upgradeFlag {
		log("Upgrading existing installation (overwriting binaries)...")
	}

	home, err := os.UserHomeDir()
	if err != nil {
		fail(fmt.Sprintf("Error: Could not determine home directory: %v", err))
	}

	installPath := filepath.Join(home, installDirName)
	binPath := filepath.Join(installPath, binDirName)

	// 0. Stop running processes at the target location
	targetBin := filepath.Join(binPath, "rag-code-mcp")
	if runtime.GOOS == "windows" {
		targetBin += ".exe"
	}
	stopRunningProcess(targetBin)

	log("Preparing installation in: " + installPath)

	// 1. Create directory structure
	if err := os.MkdirAll(binPath, 0755); err != nil {
		fail(fmt.Sprintf("Failed to create directories: %v", err))
	}

	// 2. Determine current location strictly
	execPath, _ := os.Executable()
	sourceDir := filepath.Dir(execPath)

	// Required files: rag-code-mcp (binary) + config.yaml
	// If either is missing from sourceDir, download the latest release.
	// rag-code-install is the running binary — no need to check or copy itself.
	requiredFiles := []string{"rag-code-mcp", "config.yaml"}
	optionalResources := []string{"README.md", "llms.txt", "LICENSE"}

	// Check if required files are present
	needsDownload := false
	for _, f := range requiredFiles {
		name := f
		if runtime.GOOS == "windows" && f == "rag-code-mcp" {
			name += ".exe"
		}
		if _, err := os.Stat(filepath.Join(sourceDir, name)); os.IsNotExist(err) {
			needsDownload = true
			break
		}
	}

	// Download only if rag-code-mcp or config.yaml is missing
	tempDir := ""
	if needsDownload {
		log("Required files missing in current directory. Downloading latest release...")
		var err error
		tempDir, err = downloadAndExtractLatest()
		if err != nil {
			fail(fmt.Sprintf("Failed to download and extract release: %v", err))
		}
		defer os.RemoveAll(tempDir)
		sourceDir = tempDir
		log("Files extracted successfully from release.")
	} else {
		log("Copying files from: " + sourceDir)
	}

	// Install the main binary
	{
		srcName := "rag-code-mcp"
		if runtime.GOOS == "windows" {
			srcName += ".exe"
		}
		src := filepath.Join(sourceDir, srcName)
		dst := filepath.Join(binPath, srcName)

		if err := copyFile(src, dst); err != nil {
			fail(fmt.Sprintf("Failed to install binary rag-code-mcp: %v", err))
		}
		if err := os.Chmod(dst, 0755); err != nil {
			warn(fmt.Sprintf("Failed to set executable permissions: %v", err))
		}
		success("Installed binary: rag-code-mcp")
	}

	// Install config.yaml (preserve existing)
	{
		src := filepath.Join(sourceDir, "config.yaml")
		dst := filepath.Join(binPath, "config.yaml")
		if _, err := os.Stat(dst); err == nil {
			log("config.yaml already exists - keeping existing configuration.")
			checkConfigUpgrade(dst)
		} else if err := copyFile(src, dst); err != nil {
			warn("Could not install config.yaml: " + err.Error())
		} else {
			success("Installed resource: config.yaml")
		}
	}

	// Copy optional resources (silently skip if missing)
	for _, r := range optionalResources {
		src := filepath.Join(sourceDir, r)
		dst := filepath.Join(binPath, r)
		if err := copyFile(src, dst); err == nil {
			success("Installed resource: " + r)
		}
	}

	// 4. Configure PATH
	addToPath(binPath)

	// 5. Handle environment setup based on flags
	setupEnvironment()

	// 6. Configure IDEs automatically
	selectedIDEs := parseIDESelections(*idesFlag)
	transportMode := resolveTransport(*transportFlag, *ssePortFlag)
	configureIDEs(selectedIDEs, binPath, transportMode, *ssePortFlag)

	// 7. Health Check
	runHealthCheck(binPath)

	success("RagCode MCP installation completed successfully!")
}

func setupEnvironment() {
	// Check if docker is available if needed
	if *qdrantMode == "docker" || *ollamaMode == "docker" {
		if _, err := exec.LookPath("docker"); err != nil {
			warn("Docker is required for the requested mode but was not found in PATH.")
			return
		}
	}

	needsDelay := false

	// Auto-detect if services are missing and ask for corrective action
	if *ollamaMode == "local" && !isPortOpen(11434) {
		ollamaPath, err := exec.LookPath("ollama")
		if err == nil {
			fmt.Printf("\n\033[1;33m⚠️  Ollama binary found at %s but service is not running.\033[0m\n", ollamaPath)
			fmt.Printf("   Would you like to try starting the local Ollama service? [Y/n]: ")
			if askConfirm(true) {
				log("Starting Ollama service in background...")
				// Start ollama serve; capture early exit errors via channel
				errCh := make(chan error, 1)
				go func() {
					errCh <- exec.Command("ollama", "serve").Run()
				}()
				// Give it a few seconds to bind to the port
				log("Waiting for Ollama to bind to port 11434...")
				started := false
				for i := 0; i < 10; i++ {
					select {
					case err := <-errCh:
						if err != nil {
							fmt.Printf("⚠️  Warning: ollama serve exited early: %v\n", err)
						}
						goto ollamaDone
					default:
					}
					if isPortOpen(11434) {
						success("Ollama service started successfully")
						started = true
						goto ollamaDone
					}
					time.Sleep(1 * time.Second)
				}
			ollamaDone:
				if !started && !isPortOpen(11434) {
					fmt.Printf("⚠️  Ollama did not bind to port 11434 in time\n")
				}
			}
		}

		// If still not open after attempt, or if binary wasn't found, or if user declined
		if !isPortOpen(11434) {
			fmt.Printf("\n\033[1;33m⚠️  Ollama not accessible on port 11434.\033[0m\n")
			fmt.Printf("   Would you like to start Ollama in a Docker container instead? [Y/n]: ")
			if askConfirm(true) {
				*ollamaMode = "docker"
			}
		}
	}

	if *qdrantMode == "local" && !isPortOpen(6333) {
		// Note: qdrantMode default is "docker", but if user forced "local"
		fmt.Printf("\n\033[1;33m⚠️  Qdrant not detected on port 6333.\033[0m\n")
		fmt.Printf("   Would you like to start Qdrant in a Docker container? [Y/n]: ")
		if askConfirm(true) {
			*qdrantMode = "docker"
		}
	}

	// Re-check docker availability if modes switched to docker
	if *qdrantMode == "docker" || *ollamaMode == "docker" {
		if _, err := exec.LookPath("docker"); err != nil {
			warn("Docker is required for the requested mode but was not found in PATH.")
			return
		}
	}

	// Qdrant Setup
	if *qdrantMode == "docker" {
		log("Setting up Qdrant in Docker...")
		// Remove stale container if it exists (e.g. from previous install)
		_ = exec.Command("docker", "rm", "-f", "ragcode-qdrant").Run()
		cmd := exec.Command("docker", "run", "-d",
			"--name", "ragcode-qdrant",
			"--restart", "always",
			"-p", "6333:6333",
			"-p", "6334:6334",
			"-v", "ragcode-qdrant-data:/qdrant/storage",
			"qdrant/qdrant")
		if err := cmd.Run(); err != nil {
			warn("Could not start Qdrant container (might be already running): " + err.Error())
		} else {
			success("Qdrant container started")
			needsDelay = true
		}
	}

	// Ollama Setup
	if *ollamaMode == "docker" {
		log("Setting up Ollama in Docker...")
		args := []string{"run", "-d", "--name", "ragcode-ollama", "--restart", "always", "-p", "11434:11434", "-v", "ollama-data:/root/.ollama"}
		if *gpu {
			argsWithGPU := make([]string, 0, len(args)+2)
			argsWithGPU = append(argsWithGPU, args[:2]...)
			argsWithGPU = append(argsWithGPU, "--gpus", "all")
			argsWithGPU = append(argsWithGPU, args[2:]...)
			args = argsWithGPU
		}
		args = append(args, "ollama/ollama")
		cmd := exec.Command("docker", args...)
		if err := cmd.Run(); err != nil {
			warn("Could not start Ollama container (might be already running): " + err.Error())
		} else {
			success("Ollama container started")
			needsDelay = true
		}
	}

	if needsDelay {
		log("Waiting 5s for services to initialize...")
		time.Sleep(5 * time.Second)
	}

	// Pull Models from config
	log("Ensuring required models are available...")

	// Attempt to load config from the installed destination to see which models are required
	home, _ := os.UserHomeDir()
	binPath := filepath.Join(home, installDirName, binDirName)
	cfgPath := filepath.Join(binPath, "config.yaml")

	// Use default config as base
	cfg := config.DefaultConfig()
	if loaded, err := config.Load(cfgPath); err == nil {
		cfg = loaded
	}

	requiredModels := []string{cfg.LLM.OllamaEmbed}
	if cfg.LLM.OllamaModel != "" {
		requiredModels = append(requiredModels, cfg.LLM.OllamaModel)
	}

	for _, modelName := range requiredModels {
		if modelName == "" {
			continue
		}
		log(fmt.Sprintf("Pulling model: %s", modelName))
		err := healthcheck.PullModel("http://localhost:11434", modelName)
		if err != nil {
			warn(fmt.Sprintf("Could not pull model %s automatically: %v", modelName, err))
			warn("Please run manually: ollama pull " + modelName)
		} else {
			success("Model " + modelName + " is ready")
		}
	}
}

func checkConfigUpgrade(configPath string) {
	cfg, err := config.Load(configPath)
	if err != nil {
		return
	}

	currentModel := cfg.LLM.OllamaEmbed
	stableModel := config.StableEmbeddingModel

	if currentModel != stableModel {
		fmt.Printf("\n\033[1;33m⚠️  New stable embedding model available: %s\033[0m\n", stableModel)
		fmt.Printf("   Current model: %s\n", currentModel)
		fmt.Printf("   \033[1;31mNote: Upgrading will require re-indexing all your workspaces.\033[0m\n")
		fmt.Printf("   Do you want to upgrade to the new stable model? [Y/n]: ")

		var response string
		if *assumeYes {
			fmt.Println("y (auto-confirmed)")
			response = "y"
		} else {
			_, _ = fmt.Scanln(&response)
			response = strings.ToLower(strings.TrimSpace(response))
		}

		if response == "" || response == "y" || response == "yes" {
			cfg.LLM.OllamaEmbed = stableModel
			if err := config.Save(configPath, cfg); err != nil {
				warn("Failed to update config: " + err.Error())
			} else {
				success("Config updated to " + stableModel)
				success("Remember to run 'rag_index_workspace' with 'recreate: true' for your projects.")
				// Sleep for a moment so the user sees the message
				time.Sleep(2 * time.Second)
			}
		} else {
			log("Keeping current model: " + currentModel)
		}
	}
}

func askConfirm(defaultVal bool) bool {
	if *assumeYes {
		fmt.Println("y (auto-confirmed)")
		return true
	}

	var response string
	_, _ = fmt.Scanln(&response)
	response = strings.ToLower(strings.TrimSpace(response))

	if response == "" {
		return defaultVal
	}
	return response == "y" || response == "yes"
}

func isPortOpen(port int) bool {
	address := fmt.Sprintf("127.0.0.1:%d", port)
	conn, err := net.DialTimeout("tcp", address, 1*time.Second)
	if err != nil {
		return false
	}
	conn.Close()
	return true
}

func stopRunningProcess(binPath string) {
	if _, err := os.Stat(binPath); os.IsNotExist(err) {
		return
	}

	log("Stopping existing process gracefully: " + binPath)

	// Attempt Graceful Shutdown using TCP health endpoint
	client := &http.Client{Timeout: 2 * time.Second}
	if resp, err := client.Get("http://127.0.0.1:39000/health"); err == nil {
		defer resp.Body.Close()
		var health struct {
			PID int `json:"pid"`
		}
		if decodeErr := json.NewDecoder(resp.Body).Decode(&health); decodeErr == nil && health.PID > 0 {
			pid := health.PID
			pidStr := strconv.Itoa(pid)
			log(fmt.Sprintf("Found daemon PID: %d. Sending termination signal...", pid))

			// For Windows
			if runtime.GOOS == "windows" {
				_ = exec.Command("taskkill", "/PID", pidStr).Run()
				time.Sleep(2 * time.Second)
				_ = exec.Command("taskkill", "/F", "/PID", pidStr).Run()
				return
			}

			// For Unix
			process, err := os.FindProcess(pid)
			if err == nil {
				// Send SIGTERM
				_ = process.Signal(syscall.SIGTERM)

				// Wait up to 5 seconds for it to exit gracefully
				for i := 0; i < 50; i++ {
					if err := process.Signal(syscall.Signal(0)); err != nil {
						// Process is gone
						break
					}
					time.Sleep(100 * time.Millisecond)
				}

				// After grace period, only SIGKILL if process still appears alive
				if err := process.Signal(syscall.Signal(0)); err == nil {
					_ = process.Signal(syscall.SIGKILL)
					time.Sleep(200 * time.Millisecond)
				}
				return
			}
		}
	}

	log("PID file not found or process untrackable. Using fallback termination...")

	if runtime.GOOS == "windows" {
		_ = exec.Command("taskkill", "/IM", filepath.Base(binPath)).Run()
		time.Sleep(1 * time.Second)
		_ = exec.Command("taskkill", "/F", "/IM", filepath.Base(binPath)).Run()
		return
	}

	// 1. Soft kill (SIGTERM)
	_ = exec.Command("pkill", "-15", "-f", binPath).Run()
	time.Sleep(1 * time.Second)

	// 2. Hard kill (SIGKILL) fallback
	_ = exec.Command("pkill", "-9", "-f", binPath).Run()

	time.Sleep(500 * time.Millisecond)
}

func copyFile(src, dst string) error {
	sourceFile, err := os.Open(src)
	if err != nil {
		return err
	}
	defer sourceFile.Close()

	destFile, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer destFile.Close()

	_, err = io.Copy(destFile, sourceFile)
	return err
}

func addToPath(binDir string) {
	if runtime.GOOS == "windows" {
		warn("Please add " + binDir + " to your PATH manually.")
		return
	}

	home, _ := os.UserHomeDir()

	shellEnv := os.Getenv("SHELL")
	if shellEnv == "" {
		shellEnv = "/bin/bash" // fallback
	}
	shell := filepath.Base(shellEnv)
	config := filepath.Join(home, ".bashrc")
	if shell == "zsh" {
		config = filepath.Join(home, ".zshrc")
	}

	content, _ := os.ReadFile(config)
	if strings.Contains(string(content), binDir) {
		success("PATH already configured in " + config)
		return
	}

	f, err := os.OpenFile(config, os.O_APPEND|os.O_WRONLY|os.O_CREATE, 0644)
	if err != nil {
		warn("Could not update shell config: " + err.Error())
		return
	}
	defer f.Close()

	if _, err := f.WriteString(fmt.Sprintf("\n# RagCode MCP\nexport PATH=\"%s:$PATH\"\n", binDir)); err != nil {
		warn("Could not write to shell config: " + err.Error())
		return
	}
	success("Added to PATH in " + config + " (restart shell to apply)")
}

func runHealthCheck(binDir string) {
	bin := filepath.Join(binDir, "rag-code-mcp")
	if runtime.GOOS == "windows" {
		bin += ".exe"
	}

	log("Running basic health check...")
	cmd := exec.Command(bin, "--version")
	if err := cmd.Run(); err != nil {
		warn("Health check failed: binary could not be executed.")
	} else {
		success("Health check: Binary is operational.")
	}
}

func log(msg string)     { fmt.Printf("\033[0;34m==>\033[0m %s\n", msg) }
func success(msg string) { fmt.Printf("\033[0;32m✓\033[0m %s\n", msg) }
func warn(msg string)    { fmt.Printf("\033[1;33m!\033[0m %s\n", msg) }
func fail(msg string)    { fmt.Printf("\033[0;31m✗\033[0m %s\n", msg); os.Exit(1) }

func printBanner() {
	fmt.Println(`
    ____              ______          __   
   / __ \____ _____ _/ ____/___  ____/ /__ 
  / /_/ / __ '/ __ '/ /   / __ \/ __  / _ \
  / _, _/ /_/ / /_/ / /___/ /_/ / /_/ /  __/
 /_/ |_|\__,_/\__, /\____/\____/\__,_/\___/ 
             /____/                         
    Universal Installer (Fast Copy Mode)
	`)
}

// --- Transport resolution ---

// resolveTransport determines the effective transport mode.
// 'auto': uses SSE if server is already running on the configured port, else stdio.
// 'sse':  always uses SSE URL config.
// 'stdio': always uses binary command config.
func resolveTransport(mode string, port int) string {
	switch strings.ToLower(mode) {
	case "sse":
		return "sse"
	case "stdio":
		return "stdio"
	default: // auto
		if isPortOpen(port) {
			success(fmt.Sprintf("SSE server detected on port %d → configuring IDEs with SSE URL transport", port))
			return "sse"
		}
		log(fmt.Sprintf("No SSE server on port %d → configuring IDEs with stdio (binary) transport", port))
		return "stdio"
	}
}

// --- IDE Configuration ---

func configureIDEs(selected []string, binDir string, transport string, ssePort int) {
	log("Configuring IDEs...")
	home, err := os.UserHomeDir()
	if err != nil {
		warn(fmt.Sprintf("Could not determine user home directory for IDE config: %v", err))
		return
	}
	paths := resolveIDEPaths(home)
	if len(paths) == 0 {
		warn("No known IDE paths detected")
		return
	}

	binPath := filepath.Join(binDir, "rag-code-mcp")
	if runtime.GOOS == "windows" {
		binPath += ".exe"
	}

	if transport == "sse" {
		log(fmt.Sprintf("Transport mode: SSE URL (http://localhost:%d/sse)", ssePort))
	} else {
		log(fmt.Sprintf("Transport mode: stdio (binary: %s)", binPath))
	}

	selection := normalizeIdeSelection(selected)
	configuredCount := 0

	for key, cfg := range paths {
		shouldEnsure := selection.explicit[key]

		// If auto-detecting, only configure if the root folder for the IDE seems to exist
		if !selection.auto && !shouldEnsure {
			continue // User explicitly didn't ask for this and we aren't in auto mode
		}

		dir := filepath.Dir(cfg.path)

		if !shouldEnsure {
			// Auto mode: check if IDE directory exists before trying to configure
			if _, err := os.Stat(dir); os.IsNotExist(err) {
				continue
			}
		} else {
			// Explicit mode: Ensure directory exists
			if err := os.MkdirAll(dir, 0755); err != nil {
				warn(fmt.Sprintf("Failed to create %s: %v", dir, err))
				continue
			}
		}

		if updateMCPConfig(key, cfg.displayName, cfg.path, binPath, transport, ssePort) {
			configuredCount++
		}
	}

	if configuredCount == 0 {
		log("No IDEs were automatically configured. They may not be installed or use non-standard paths.")
	}

	// Informational: Codex CLI uses TOML format – cannot be auto-configured
	codexPath := filepath.Join(home, ".codex")
	if _, err := os.Stat(codexPath); err == nil {
		log("OpenAI Codex CLI detected at ~/.codex – requires manual config (TOML format).")
		log("  Add to ~/.codex/config.toml:")
		log(`  [mcp_servers.ragcode]`)
		log(fmt.Sprintf(`  command = "%s"`, binPath))
		log(`  args = []`)
	}

	// OpenClaw / NemoClaw Logging
	openclawPath := filepath.Join(home, ".openclaw")
	if _, err := os.Stat(openclawPath); err == nil {
		log("OpenClaw detected at ~/.openclaw – requires manual config.")
		log(fmt.Sprintf(`  Run: openclaw mcp set ragcode "%s"`, binPath))
	}

	nemoclawPath := filepath.Join(home, ".nemoclaw")
	if _, err := os.Stat(nemoclawPath); err == nil {
		log("NemoClaw sandbox detected – requires manual policy config.")
		log("  Please refer to NVIDIA OpenShell documentation to map the MCP tool.")
	}
}

type idePath struct {
	path        string
	displayName string
}

func resolveIDEPaths(home string) map[string]idePath {
	paths := map[string]idePath{
		"windsurf": {
			path:        filepath.Join(home, ".codeium", "windsurf", "mcp_config.json"),
			displayName: "Windsurf",
		},
		"cursor": determineCursorPath(home),
		"copilot": {
			path:        filepath.Join(home, ".copilot", "mcp-config.json"),
			displayName: "GitHub Copilot CLI",
		},
		"antigravity": {
			path:        filepath.Join(home, ".gemini", "antigravity", "mcp_config.json"),
			displayName: "Antigravity",
		},
		"mcp-cli": {
			path:        filepath.Join(home, ".config", "mcp-servers.json"),
			displayName: "MCP CLI / Generic",
		},
		// Claude Code CLI stores MCP servers inside ~/.claude.json under "mcpServers"
		"claude-cli": {
			path:        filepath.Join(home, ".claude.json"),
			displayName: "Claude Code CLI",
		},
		// Gemini CLI stores MCP servers inside ~/.gemini/settings.json under "mcpServers"
		"gemini-cli": {
			path:        filepath.Join(home, ".gemini", "settings.json"),
			displayName: "Gemini CLI",
		},
		"openhands": {
			path:        filepath.Join(home, ".openhands", "mcp.json"),
			displayName: "OpenHands",
		},
		"continue": {
			path:        filepath.Join(home, ".continue", "mcpServers", "ragcode.json"),
			displayName: "Continue.dev",
		},
	}

	// Claude Desktop (GUI app) – OS-specific paths
	switch runtime.GOOS {
	case "darwin":
		paths["claude"] = idePath{filepath.Join(home, "Library", "Application Support", "Claude", "claude_desktop_config.json"), "Claude Desktop"}
	case "windows":
		appData := os.Getenv("APPDATA")
		if appData != "" {
			paths["claude"] = idePath{filepath.Join(appData, "Claude", "claude_desktop_config.json"), "Claude Desktop"}
		}
	default: // Linux / others
		paths["claude"] = idePath{filepath.Join(home, ".config", "Claude", "claude_desktop_config.json"), "Claude Desktop"}
	}

	// Zed Editor – stores MCP in its main settings.json under "context_servers"
	switch runtime.GOOS {
	case "windows":
		appData := os.Getenv("APPDATA")
		if appData != "" {
			paths["zed"] = idePath{filepath.Join(appData, "Zed", "settings.json"), "Zed Editor"}
		}
	default: // Linux + macOS
		paths["zed"] = idePath{filepath.Join(home, ".config", "zed", "settings.json"), "Zed Editor"}
	}

	// VS Code (Copilot & popular extensions like Roo Code, Cline)
	if vsCodeUserDir, ok := getVSCodeUserDir(home); ok {
		paths["vs-code"] = idePath{path: filepath.Join(vsCodeUserDir, "mcp.json"), displayName: "VS Code (Copilot)"}
		paths["roo-code"] = idePath{path: filepath.Join(vsCodeUserDir, "globalStorage", "rooveterinaryinc.roo-cline", "settings", "cline_mcp_settings.json"), displayName: "Roo Code (VS Code)"}
		paths["cline"] = idePath{path: filepath.Join(vsCodeUserDir, "globalStorage", "saoudrizwan.claude-dev", "settings", "cline_mcp_settings.json"), displayName: "Cline (VS Code)"}
	}

	return paths
}

func determineCursorPath(home string) idePath {
	if runtime.GOOS == "windows" {
		appData := os.Getenv("APPDATA")
		if appData != "" {
			return idePath{path: filepath.Join(appData, "Cursor", "mcp.json"), displayName: "Cursor"}
		}
	}
	return idePath{path: filepath.Join(home, ".cursor", "mcp.json"), displayName: "Cursor"}
}

func getVSCodeUserDir(home string) (string, bool) {
	switch runtime.GOOS {
	case "windows":
		appData := os.Getenv("APPDATA")
		if appData == "" {
			return "", false
		}
		return filepath.Join(appData, "Code", "User"), true
	case "darwin":
		return filepath.Join(home, "Library", "Application Support", "Code", "User"), true
	default:
		return filepath.Join(home, ".config", "Code", "User"), true
	}
}

type ideSelection struct {
	auto     bool
	explicit map[string]bool
}

func parseIDESelections(raw string) []string {
	if raw == "" {
		return nil
	}
	parts := strings.Split(raw, ",")
	result := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.ToLower(strings.TrimSpace(part))
		if part != "" {
			result = append(result, part)
		}
	}
	return result
}

func normalizeIdeSelection(selected []string) ideSelection {
	if len(selected) == 0 {
		return ideSelection{auto: true, explicit: map[string]bool{}}
	}
	sel := ideSelection{explicit: map[string]bool{}}
	for _, item := range selected {
		if item == "auto" {
			sel.auto = true
			continue
		}
		sel.explicit[item] = true
	}
	if len(sel.explicit) == 0 {
		sel.auto = true
	}
	return sel
}

func updateMCPConfig(ideKey, displayName, path, binPath, transport string, ssePort int) bool {
	// Special case: Zed Editor uses "context_servers" inside its main settings.json
	if ideKey == "zed" {
		return updateZedConfig(displayName, path, binPath, transport, ssePort)
	}

	configMap := make(map[string]interface{})

	// Read existing
	if data, err := os.ReadFile(path); err == nil {
		if err := json.Unmarshal(data, &configMap); err != nil {
			warn(fmt.Sprintf("Failed to parse existing MCP config %s: %v", path, err))
		}
	}

	collectionKey := "mcpServers"
	if ideKey == "vs-code" || ideKey == "copilot" {
		// New GitHub copilot uses "mcpServers" normally now, but leaving "servers" as fallback check
		if _, exists := configMap["servers"]; exists {
			collectionKey = "servers"
		}
	}

	servers := make(map[string]interface{})
	if existing, ok := configMap[collectionKey].(map[string]interface{}); ok {
		servers = existing
	}

	if transport == "sse" {
		servers["ragcode"] = buildSSEServerEntry(ssePort)
	} else {
		servers["ragcode"] = buildMCPServerEntry(ideKey, binPath)
	}
	configMap[collectionKey] = servers

	data, _ := json.MarshalIndent(configMap, "", "  ")
	if err := os.MkdirAll(filepath.Dir(path), 0755); err == nil {
		if err := os.WriteFile(path, data, 0644); err == nil {
			success(fmt.Sprintf("Configured %s (%s) [%s]", displayName, path, transport))
			return true
		} else {
			warn(fmt.Sprintf("Could not write to %s: %v", path, err))
		}
	}
	return false
}

// updateZedConfig handles Zed Editor's special format where MCP servers
// live under "context_servers" in the main settings.json.
// SSE mode: Zed supports HTTP MCP servers via the "url" field inside "command".
func updateZedConfig(displayName, path, binPath, transport string, ssePort int) bool {
	configMap := make(map[string]interface{})

	if data, err := os.ReadFile(path); err == nil {
		if err := json.Unmarshal(data, &configMap); err != nil {
			warn(fmt.Sprintf("Failed to parse existing Zed config %s: %v", path, err))
		}
	}

	contextServers := make(map[string]interface{})
	if existing, ok := configMap["context_servers"].(map[string]interface{}); ok {
		contextServers = existing
	}

	var ragcodeEntry map[string]interface{}
	if transport == "sse" {
		// Zed Streamable HTTP: endpoint /mcp, fără sesiuni
		ragcodeEntry = map[string]interface{}{
			"command": map[string]interface{}{
				"url": fmt.Sprintf("http://localhost:%d/mcp", ssePort),
			},
			"settings": map[string]interface{}{},
		}
	} else {
		ragcodeEntry = map[string]interface{}{
			"command": map[string]interface{}{
				"path": binPath,
				"args": []string{},
				"env": map[string]string{
					"OLLAMA_BASE_URL": "http://localhost:11434",
					"OLLAMA_EMBED":    config.StableEmbeddingModel,
					"QDRANT_URL":      "http://localhost:6333",
				},
			},
			"settings": map[string]interface{}{},
		}
	}
	contextServers["ragcode"] = ragcodeEntry
	configMap["context_servers"] = contextServers

	data, _ := json.MarshalIndent(configMap, "", "  ")
	if err := os.MkdirAll(filepath.Dir(path), 0755); err == nil {
		if err := os.WriteFile(path, data, 0644); err == nil {
			success(fmt.Sprintf("Configured %s (%s) [%s]", displayName, path, transport))
			return true
		} else {
			warn(fmt.Sprintf("Could not write to %s: %v", path, err))
		}
	}
	return false
}

func downloadAndExtractLatest() (string, error) {
	// Pass "v0.0.0" to force a network check to get the absolute latest if we're a naked installer
	ctx := context.Background()
	info, err := updater.CheckForUpdates(ctx, "v0.0.0", true)
	if err != nil {
		return "", fmt.Errorf("failed to check for updates: %w", err)
	}
	if info == nil || info.AssetURL == "" {
		return "", fmt.Errorf("no update asset found")
	}

	tempDir, err := os.MkdirTemp("", "ragcode-install-*")
	if err != nil {
		return "", err
	}
	cleanup := true
	defer func() {
		if cleanup {
			os.RemoveAll(tempDir)
		}
	}()

	archivePath := filepath.Join(tempDir, "release-archive")
	log("Downloading " + info.AssetURL)

	// Use DownloadAndVerify for checksum-verified downloads (supply-chain security)
	if err := info.DownloadAndVerify(ctx, archivePath); err != nil {
		return "", fmt.Errorf("failed to download and verify release: %w", err)
	}

	log("Extracting archive...")
	f, err := os.Open(archivePath)
	if err != nil {
		return "", err
	}
	defer f.Close()

	extractDir := filepath.Join(tempDir, "extracted")
	if err := os.MkdirAll(extractDir, 0755); err != nil {
		return "", err
	}

	if err := extract.Archive(ctx, f, extractDir, nil); err != nil {
		return "", err
	}

	cleanup = false // success — caller is responsible for cleanup
	return extractDir, nil
}

// buildMCPServerEntry builds the stdio (binary) MCP server entry.
func buildMCPServerEntry(ideKey, binPath string) map[string]interface{} {
	entry := map[string]interface{}{
		"command": binPath,
		"args":    []string{},
		"env": map[string]string{
			"OLLAMA_BASE_URL": "http://localhost:11434",
			"OLLAMA_EMBED":    config.StableEmbeddingModel,
			"QDRANT_URL":      "http://localhost:6333",
		},
	}

	switch ideKey {
	case "windsurf":
		entry["disabled"] = false
	}

	return entry
}

// buildSSEServerEntry builds the Streamable HTTP (stateless) MCP server entry.
// Agentul trimite POST direct la /mcp — fără sesiuni, fără sessionid.
func buildSSEServerEntry(ssePort int) map[string]interface{} {
	return map[string]interface{}{
		"url": fmt.Sprintf("http://localhost:%d/mcp", ssePort),
	}
}

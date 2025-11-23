package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// Configuration Flags
var (
	ollamaMode = flag.String("ollama", "local", "Mode for Ollama: 'local' (use existing) or 'docker' (run container)")
	qdrantMode = flag.String("qdrant", "docker", "Mode for Qdrant: 'docker' (run container) or 'remote' (use existing URL)")
	modelsDir  = flag.String("models-dir", "", "Path to local Ollama models directory (for Docker mapping). Defaults to ~/.ollama")
	gpu        = flag.Bool("gpu", false, "Enable GPU support for Docker containers (requires nvidia-container-toolkit)")
	skipBuild  = flag.Bool("skip-build", false, "Skip building the binary (use existing if available)")
)

// Constants
const (
	ollamaImage     = "ollama/ollama:latest"
	qdrantImage     = "qdrant/qdrant:latest"
	ollamaContainer = "ragcode-ollama"
	qdrantContainer = "ragcode-qdrant"
	defaultModel    = "phi3:medium"
	defaultEmbed    = "nomic-embed-text"
	ollamaPort      = "11434"
	qdrantPort      = "6333"
)

// Colors for output
var (
	blue   = "\033[0;34m"
	green  = "\033[0;32m"
	yellow = "\033[1;33m"
	red    = "\033[0;31m"
	reset  = "\033[0m"
)

func init() {
	if runtime.GOOS == "windows" {
		// Disable colors on Windows to avoid garbage characters
		blue, green, yellow, red, reset = "", "", "", "", ""
	}
}

func log(msg string)     { fmt.Printf("%s==> %s%s\n", blue, msg, reset) }
func success(msg string) { fmt.Printf("%s✓ %s%s\n", green, msg, reset) }
func warn(msg string)    { fmt.Printf("%s! %s%s\n", yellow, msg, reset) }
func fail(msg string)    { fmt.Printf("%s✗ %s%s\n", red, msg, reset); os.Exit(1) }

func main() {
	flag.Parse()

	printBanner()

	// 1. Build and Install Binary
	if !*skipBuild {
		installBinary()
	}

	// 2. Setup Services (Docker or Local)
	setupServices()

	// 3. Provision Models (Auto-download)
	provisionModels()

	// 4. Configure IDEs
	configureIDEs()

	printSummary()
}

func printBanner() {
	fmt.Println(`
    ____              ______          __   
   / __ \____ _____ _/ ____/___  ____/ /__ 
  / /_/ / __ '/ __ '/ /   / __ \/ __  / _ \
 / _, _/ /_/ / /_/ / /___/ /_/ / /_/ /  __/
/_/ |_|\__,_/\__, /\____/\____/\__,_/\___/ 
            /____/                         
   Universal Installer
	`)
}

// --- Step 1: Binary Installation ---

func installBinary() {
	log("Installing RagCode binary...")

	// Determine install path
	home, _ := os.UserHomeDir()
	var binDir string
	if runtime.GOOS == "windows" {
		binDir = filepath.Join(home, "go", "bin")
	} else {
		binDir = filepath.Join(home, ".local", "bin")
	}
	if err := os.MkdirAll(binDir, 0755); err != nil {
		fail(fmt.Sprintf("Could not create bin directory: %v", err))
	}

	outputBin := filepath.Join(binDir, "rag-code-mcp")
	if runtime.GOOS == "windows" {
		outputBin += ".exe"
	}

	// Try downloading pre‑built binary first
	if downloadBinary(outputBin) {
		success("Binary downloaded successfully")
		addToPath(binDir)
		return
	}

	// Fallback: build locally if source is present
	warn("Download failed – attempting local build from source.")
	// Verify source exists
	if _, err := os.Stat("./cmd/rag-code-mcp"); err != nil {
		fail("Release not found and source code not available. Run installer from repository or create a GitHub release.")
	}
	cmd := exec.Command("go", "build", "-o", outputBin, "./cmd/rag-code-mcp")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	log(fmt.Sprintf("Compiling to %s...", outputBin))
	if err := cmd.Run(); err != nil {
		fail(fmt.Sprintf("Local build failed: %v", err))
	}
	success("Binary built successfully")
	addToPath(binDir)
}

// downloadBinary fetches the installer binary from the latest GitHub release.
func downloadBinary(dest string) bool {
	var binaryName string
	switch runtime.GOOS {
	case "linux":
		binaryName = "ragcode-installer-linux"
	case "darwin":
		binaryName = "ragcode-installer-darwin"
	case "windows":
		binaryName = "ragcode-installer-windows.exe"
	default:
		return false
	}
	url := fmt.Sprintf("https://github.com/doITmagic/rag-code-mcp/releases/latest/download/%s", binaryName)
	log(fmt.Sprintf("Downloading from %s...", url))
	resp, err := http.Get(url)
	if err != nil || resp.StatusCode != 200 {
		if resp != nil && resp.StatusCode == 404 {
			warn("Release not found (404). Skipping download.")
		} else {
			warn(fmt.Sprintf("Failed to download binary: %v (status %d)", err, resp.StatusCode))
		}
		return false
	}
	defer resp.Body.Close()
	out, err := os.Create(dest)
	if err != nil {
		warn(fmt.Sprintf("Could not create file %s: %v", dest, err))
		return false
	}
	defer out.Close()
	if _, err := io.Copy(out, resp.Body); err != nil {
		warn(fmt.Sprintf("Error writing binary: %v", err))
		return false
	}
	if err := os.Chmod(dest, 0755); err != nil {
		warn(fmt.Sprintf("Could not set executable flag: %v", err))
		return false
	}
	return true
}

func addToPath(binDir string) {
	path := os.Getenv("PATH")
	if strings.Contains(path, binDir) {
		return
	}

	log("Adding binary to PATH...")

	var shellConfig string
	home, _ := os.UserHomeDir()

	switch filepath.Base(os.Getenv("SHELL")) {
	case "zsh":
		shellConfig = filepath.Join(home, ".zshrc")
	case "bash":
		shellConfig = filepath.Join(home, ".bashrc")
	default:
		if runtime.GOOS == "windows" {
			warn("Please add " + binDir + " to your PATH manually.")
			return
		}
		shellConfig = filepath.Join(home, ".profile")
	}

	f, err := os.OpenFile(shellConfig, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		warn(fmt.Sprintf("Could not update shell config: %v", err))
		return
	}
	defer f.Close()

	if _, err := f.WriteString(fmt.Sprintf("\nexport PATH=\"%s:$PATH\"\n", binDir)); err != nil {
		warn(fmt.Sprintf("Could not write to shell config: %v", err))
	} else {
		success(fmt.Sprintf("Added to %s (restart shell to apply)", shellConfig))
	}
}

// --- Step 2: Service Orchestration ---

func setupServices() {
	log("Configuring services...")

	// Setup Qdrant
	if *qdrantMode == "docker" {
		startDockerContainer(qdrantContainer, qdrantImage, []string{"-p", "6333:6333"}, nil)
	} else {
		log("Using remote/local Qdrant (skipping Docker setup)")
	}

	// Setup Ollama
	if *ollamaMode == "docker" {
		home, _ := os.UserHomeDir()
		localModels := *modelsDir
		if localModels == "" {
			localModels = filepath.Join(home, ".ollama")
		}

		// Ensure local models dir exists
		os.MkdirAll(localModels, 0755)

		args := []string{
			"-p", "11434:11434",
			"-v", fmt.Sprintf("%s:/root/.ollama", localModels),
			"--dns", "8.8.8.8", // Fix DNS issues in some containers
		}

		if *gpu {
			args = append(args, "--gpus", "all")
		}

		startDockerContainer(ollamaContainer, ollamaImage, args, nil)
	} else {
		log("Using local Ollama service (skipping Docker setup)")
	}

	// Wait for healthchecks
	waitForService("Ollama", "http://localhost:11434")
	waitForService("Qdrant", "http://localhost:6333")
}

func startDockerContainer(name, image string, args []string, env []string) {
	// Check if running
	cmd := exec.Command("docker", "ps", "-q", "-f", "name="+name)
	out, _ := cmd.Output()
	if len(out) > 0 {
		success(fmt.Sprintf("Container %s is already running", name))
		return
	}

	// Remove if exists but stopped
	exec.Command("docker", "rm", name).Run()

	// Run
	runArgs := []string{"run", "-d", "--name", name, "--restart", "unless-stopped"}
	runArgs = append(runArgs, args...)
	for _, e := range env {
		runArgs = append(runArgs, "-e", e)
	}
	runArgs = append(runArgs, image)

	log(fmt.Sprintf("Starting container %s...", name))
	if err := exec.Command("docker", runArgs...).Run(); err != nil {
		fail(fmt.Sprintf("Failed to start %s: %v", name, err))
	}
	success(fmt.Sprintf("Started %s", name))
}

func waitForService(name, url string) {
	log(fmt.Sprintf("Waiting for %s to be ready...", name))
	for i := 0; i < 30; i++ {
		resp, err := http.Get(url)
		if err == nil && resp.StatusCode < 500 {
			success(fmt.Sprintf("%s is ready", name))
			return
		}
		time.Sleep(1 * time.Second)
		fmt.Print(".")
	}
	fmt.Println()
	fail(fmt.Sprintf("%s failed to start. Check logs.", name))
}

// --- Step 3: Model Provisioning ---

type ModelList struct {
	Models []struct {
		Name string `json:"name"`
	} `json:"models"`
}

func provisionModels() {
	log("Checking AI models...")

	required := []string{defaultModel, defaultEmbed}

	// Get installed models
	resp, err := http.Get("http://localhost:11434/api/tags")
	if err != nil {
		warn("Could not connect to Ollama API to check models. Skipping provisioning.")
		return
	}
	defer resp.Body.Close()

	var list ModelList
	if err := json.NewDecoder(resp.Body).Decode(&list); err != nil {
		warn("Failed to parse Ollama model list")
		return
	}

	installed := make(map[string]bool)
	for _, m := range list.Models {
		installed[m.Name] = true
	}

	for _, req := range required {
		// Check for exact match or match without tag if 'latest'
		found := false
		for k := range installed {
			if strings.HasPrefix(k, req) {
				found = true
				break
			}
		}

		if found {
			success(fmt.Sprintf("Model %s is present", req))
		} else {
			pullModel(req)
		}
	}
}

func pullModel(name string) {
	log(fmt.Sprintf("Downloading model %s (this may take a while)...", name))

	reqBody, _ := json.Marshal(map[string]string{"name": name})
	resp, err := http.Post("http://localhost:11434/api/pull", "application/json", bytes.NewBuffer(reqBody))
	if err != nil {
		fail(fmt.Sprintf("Failed to pull model %s: %v", name, err))
	}
	defer resp.Body.Close()

	// Read stream to keep connection open, but we won't parse every line for progress to keep it simple
	io.Copy(io.Discard, resp.Body)

	success(fmt.Sprintf("Model %s downloaded", name))
}

// --- Step 4: IDE Configuration ---

func configureIDEs() {
	log("Configuring IDEs...")

	home, _ := os.UserHomeDir()

	// Define paths for various IDEs
	configs := map[string]string{
		"VS Code":        filepath.Join(home, ".config", "Code", "User", "globalStorage", "mcp-servers.json"),
		"Claude Desktop": filepath.Join(home, ".config", "Claude", "mcp-servers.json"), // Linux path
		"Windsurf":       filepath.Join(home, ".codeium", "windsurf", "mcp_config.json"),
		"Cursor":         filepath.Join(home, ".cursor", "mcp.config.json"),
	}

	if runtime.GOOS == "darwin" {
		configs["Claude Desktop"] = filepath.Join(home, "Library", "Application Support", "Claude", "mcp-servers.json")
		configs["VS Code"] = filepath.Join(home, "Library", "Application Support", "Code", "User", "globalStorage", "mcp-servers.json")
	} else if runtime.GOOS == "windows" {
		appData := os.Getenv("APPDATA")
		configs["Claude Desktop"] = filepath.Join(appData, "Claude", "mcp-servers.json")
		configs["VS Code"] = filepath.Join(appData, "Code", "User", "globalStorage", "mcp-servers.json")
	}

	// Determine binary path
	var binPath string
	if runtime.GOOS == "windows" {
		binPath = filepath.Join(home, "go", "bin", "rag-code-mcp.exe")
	} else {
		binPath = filepath.Join(home, ".local", "bin", "rag-code-mcp")
	}

	for ide, path := range configs {
		if _, err := os.Stat(filepath.Dir(path)); err == nil {
			updateMCPConfig(ide, path, binPath)
		}
	}
}

func updateMCPConfig(ide, path, binPath string) {
	config := make(map[string]interface{})

	// Read existing
	if data, err := os.ReadFile(path); err == nil {
		json.Unmarshal(data, &config)
	}

	mcpServers := make(map[string]interface{})
	if existing, ok := config["mcpServers"].(map[string]interface{}); ok {
		mcpServers = existing
	}

	mcpServers["ragcode"] = map[string]interface{}{
		"command": binPath,
		"args":    []string{},
		"env": map[string]string{
			"OLLAMA_BASE_URL": "http://localhost:11434",
			"OLLAMA_MODEL":    defaultModel,
			"OLLAMA_EMBED":    defaultEmbed,
			"QDRANT_URL":      "http://localhost:6333",
		},
	}

	config["mcpServers"] = mcpServers

	data, _ := json.MarshalIndent(config, "", "  ")
	if err := os.MkdirAll(filepath.Dir(path), 0755); err == nil {
		if err := os.WriteFile(path, data, 0644); err == nil {
			success(fmt.Sprintf("Configured %s", ide))
		}
	}
}

func printSummary() {
	fmt.Println("\n" + green + "Installation Complete! 🚀" + reset)
	fmt.Println("────────────────────────────────────────────")
	fmt.Println("RagCode MCP Server is running and configured.")
	fmt.Println("\nTry it in your IDE:")
	fmt.Println("  - VS Code: Open Copilot Chat and type '@ragcode'")
	fmt.Println("  - Claude:  Enable MCP in settings")
	fmt.Println("  - Cursor:  Check MCP settings")
	fmt.Println("\nTo troubleshoot, run: rag-code-mcp")
}

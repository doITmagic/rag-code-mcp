package main

import (
	"bufio"
	"bytes"
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
	"strings"
	"time"
)

// Configuration Flags
var (
	ollamaMode    = flag.String("ollama", "local", "Mode for Ollama: 'local' (use existing) or 'docker' (run container)")
	qdrantMode    = flag.String("qdrant", "docker", "Mode for Qdrant: 'docker' (run container) or 'remote' (use existing URL)")
	modelsDir     = flag.String("models-dir", "", "Path to local Ollama models directory (for Docker mapping). Defaults to ~/.ollama")
	gpu           = flag.Bool("gpu", false, "Enable GPU support for Docker containers (requires nvidia-container-toolkit)")
	skipBuild     = flag.Bool("skip-build", false, "Skip building the binary (use existing if available)")
	idesFlag      = flag.String("ides", "auto", "Comma-separated IDE list to configure (auto, vs-code, claude, cursor, windsurf, antigravity)")
	upgradeFlag   = flag.Bool("upgrade", false, "Upgrade existing installation")
	uninstallFlag = flag.Bool("uninstall", false, "Uninstall the application and clean up configurations")
)

// Constants
const (
	ollamaImage     = "ollama/ollama:latest"
	qdrantImage     = "qdrant/qdrant:latest"
	ollamaContainer = "ragcode-ollama"
	qdrantContainer = "ragcode-qdrant"
	defaultModel    = "phi3:medium"
	defaultEmbed    = "mxbai-embed-large"
	installDirName  = ".local/share/ragcode"
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

func installRuntimeBinaries() {
	// Stop any running servers before attempting to replace binaries
	stopRunningServers()

	home, err := os.UserHomeDir()
	if err != nil {
		fail(fmt.Sprintf("Could not determine user home directory: %v", err))
	}
	installDir := filepath.Join(home, installDirName)
	binDir := filepath.Join(installDir, "bin")

	if err := os.MkdirAll(binDir, 0755); err != nil {
		fail(fmt.Sprintf("Could not create install dir %s: %v", binDir, err))
	}

	binaries := []struct {
		name string
		pkg  string
	}{
		{"rag-code-mcp", "./cmd/rag-code-mcp"},
		{"index-all", "./cmd/index-all"},
		{"ragcode-installer", "./cmd/install"},
	}

	log(fmt.Sprintf("Installing runtime binaries into %s...", binDir))
	for _, bin := range binaries { // Install/update all to ensure latest version
		binName := bin.name
		if runtime.GOOS == "windows" {
			binName += ".exe"
		}
		output := filepath.Join(binDir, binName)

		// Option 1: Check if pre-built binary exists in current directory
		if _, err := os.Stat(binName); err == nil {
			log(fmt.Sprintf(" - Found %s in current directory, copying...", binName))
			if err := copyFile(binName, output); err != nil {
				fail(fmt.Sprintf("Failed to copy %s: %v", binName, err))
			}
		} else if _, err := os.Stat(bin.pkg); err == nil {
			// Option 2: Build from source
			if _, err := exec.LookPath("go"); err == nil {
				log(fmt.Sprintf(" - Building %s from source...", bin.name))
				cmd := exec.Command("go", "build", "-o", output, bin.pkg)
				if err := cmd.Run(); err != nil {
					warn(fmt.Sprintf("Failed to build %s: %v", bin.name, err))
					continue
				}
			}
		}

		if _, err := os.Stat(output); err == nil {
			if err := os.Chmod(output, 0755); err != nil {
				warn(fmt.Sprintf("Could not set executable flag for %s: %v", output, err))
			}

			// CLI Tool Support: Symlink to ~/.local/bin if it exists
			localBin := filepath.Join(home, ".local", "bin")
			if _, err := os.Stat(localBin); err == nil {
				linkPath := filepath.Join(localBin, binName)
				os.Remove(linkPath) // Remove old link
				if err := os.Symlink(output, linkPath); err == nil {
					success(fmt.Sprintf("Linked %s to %s", binName, linkPath))
				}
			}
		}
	}
}

func runHealthCheck() {
	log("Running RagCode health check...")
	home, err := os.UserHomeDir()
	if err != nil {
		fail(fmt.Sprintf("Could not determine user home directory: %v", err))
	}
	installDir := filepath.Join(home, installDirName)
	binPath := filepath.Join(installDir, "bin", "rag-code-mcp")

	if runtime.GOOS == "windows" {
		binPath += ".exe"
	}

	if _, err := os.Stat(binPath); err != nil {
		warn(fmt.Sprintf("Health check skipped – binary not found at %s", binPath))
		return
	}

	cmd := exec.Command(binPath, "--health")
	cmd.Dir = installDir
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Run(); err != nil {
		warn(fmt.Sprintf("Health check reported issues. Run '%s --health' manually for details.", binPath))
	} else {
		success("Health check passed – all services are reachable")
	}
}

func log(msg string)     { fmt.Printf("%s==> %s%s\n", blue, msg, reset) }
func success(msg string) { fmt.Printf("%s✓ %s%s\n", green, msg, reset) }
func warn(msg string)    { fmt.Printf("%s! %s%s\n", yellow, msg, reset) }
func fail(msg string)    { fmt.Printf("%s✗ %s%s\n", red, msg, reset); os.Exit(1) }

func checkDockerAvailable() {
	log("Checking Docker availability...")

	// Check if docker command exists in PATH
	dockerPath, err := exec.LookPath("docker")
	if err != nil {
		if runtime.GOOS == "windows" {
			fmt.Println()
			fmt.Println("Docker CLI not found in PATH.")
			fmt.Println()
			fmt.Println("If you have Docker Desktop installed with WSL2 backend, you have two options:")
			fmt.Println()
			fmt.Println("  Option 1: Enable Docker CLI for Windows")
			fmt.Println("    1. Open Docker Desktop")
			fmt.Println("    2. Go to Settings > Resources > WSL Integration")
			fmt.Println("    3. Enable 'Use the WSL 2 based engine'")
			fmt.Println("    4. Restart Docker Desktop and try again")
			fmt.Println()
			fmt.Println("  Option 2: Run this installer inside WSL")
			fmt.Println("    1. Open WSL terminal (wsl.exe)")
			fmt.Println("    2. Download the Linux version of the installer")
			fmt.Println("    3. Run the installer from within WSL")
			fmt.Println()
			fmt.Println("  Option 3: Use local services instead of Docker")
			fmt.Println("    Run: .\\ragcode-installer.exe -ollama=local -qdrant=remote")
			fmt.Println("    (Requires Ollama and Qdrant to be installed separately)")
			fmt.Println()
			fail("Docker CLI not available. See options above.")
		} else {
			fail("Docker not found. Please install Docker: https://docs.docker.com/get-docker/")
		}
	}

	// Verify docker daemon is running
	cmd := exec.Command("docker", "info")
	if err := cmd.Run(); err != nil {
		if runtime.GOOS == "windows" {
			fmt.Println()
			fmt.Println("Docker daemon is not running or not accessible.")
			fmt.Println()
			fmt.Println("Please ensure:")
			fmt.Println("  1. Docker Desktop is running")
			fmt.Println("  2. Docker Desktop has finished starting (check system tray)")
			fmt.Println("  3. If using WSL2 backend, ensure WSL integration is enabled")
			fmt.Println()
			fail("Docker daemon not accessible. Start Docker Desktop and try again.")
		} else {
			fail("Docker daemon not running. Please start Docker and try again.")
		}
	}

	success(fmt.Sprintf("Docker available at %s", dockerPath))
}

func isPortInUse(port int) bool {
	conn, err := net.DialTimeout("tcp", fmt.Sprintf("localhost:%d", port), time.Second)
	if err != nil {
		return false
	}
	conn.Close()
	return true
}

func killProcessOnPort(port int) {
	if runtime.GOOS == "windows" {
		// Windows: find PID and kill
		cmd := exec.Command("cmd", "/c", fmt.Sprintf("for /f \"tokens=5\" %%a in ('netstat -aon ^| findstr :%d ^| findstr LISTENING') do taskkill /PID %%a /F", port))
		_ = cmd.Run() // Ignore error - best effort kill
	} else {
		// Linux/Mac: use fuser
		_ = exec.Command("fuser", "-k", fmt.Sprintf("%d/tcp", port)).Run() // Ignore error - best effort kill
	}
}

// stopRunningServers stops any running rag-code-mcp processes
func stopRunningServers() {
	log("Checking for running rag-code-mcp processes...")

	var cmd *exec.Cmd
	if runtime.GOOS == "windows" {
		cmd = exec.Command("taskkill", "/F", "/IM", "rag-code-mcp.exe")
	} else {
		cmd = exec.Command("pkill", "-f", "rag-code-mcp")
	}

	err := cmd.Run()
	if err == nil {
		success("Stopped running MCP server processes")
		time.Sleep(500 * time.Millisecond) // Give processes time to clean up
	} else {
		// Not an error if no processes found - just means nothing was running
		log("No running MCP server processes found (this is normal)")
	}
}

func freeRequiredPorts() {
	ports := map[int]string{}

	if *ollamaMode == "docker" {
		ports[11434] = "Ollama"
	}
	if *qdrantMode == "docker" {
		ports[6333] = "Qdrant"
	}

	for port, name := range ports {
		if isPortInUse(port) {
			// 1. Check if it's already our named container (shorthand check)
			containerName := ""
			if port == 11434 {
				containerName = ollamaContainer
			} else if port == 6333 {
				containerName = qdrantContainer
			}

			if containerName != "" {
				cmd := exec.Command("docker", "ps", "--filter", "name="+containerName, "--filter", "status=running", "--format", "{{.Names}}")
				output, _ := cmd.CombinedOutput()
				if strings.Contains(string(output), containerName) {
					success(fmt.Sprintf("Service %s (%s) is already running in Docker", name, containerName))
					continue
				}
			}

			// 2. Check if it's already the service we want responding on the port
			isOurs := false
			client := &http.Client{Timeout: 2 * time.Second}
			switch port {
			case 11434:
				resp, err := client.Get("http://127.0.0.1:11434/api/tags")
				if err == nil {
					resp.Body.Close()
					isOurs = true
				}
			case 6333:
				// Qdrant check
				resp, err := client.Get("http://127.0.0.1:6333/readyz")
				if err == nil {
					resp.Body.Close()
					isOurs = true
				}
			}

			if isOurs {
				success(fmt.Sprintf("Service %s is already running on port %d", name, port))
				continue
			}

			// 3. It's an unknown process, try to kill it
			log(fmt.Sprintf("Port %d (%s) is in use by an unknown process. Trying to free it...", port, name))
			killProcessOnPort(port)
			time.Sleep(1 * time.Second)

			if isPortInUse(port) {
				fail(fmt.Sprintf("Could not free port %d (%s). Please stop the process manually.", port, name))
			}
		}
	}
}

func main() {
	flag.Parse()

	if *uninstallFlag {
		runUninstall()
		return
	}

	printBanner()

	if *upgradeFlag {
		log("Upgrading RagCode MCP Server...")
	}

	// 0. Check Docker availability if needed
	if *ollamaMode == "docker" || *qdrantMode == "docker" {
		checkDockerAvailable()
		freeRequiredPorts()
	}

	// 1. Build and Install Binary
	if !*skipBuild {
		installBinary()
	} else {
		log("Skipping rag-code-mcp binary install (--skip-build)")
	}
	installRuntimeBinaries()

	// 2. Setup Services (Docker or Local)
	setupServices()

	// 3. Provision Models (Auto-download)
	provisionModels()

	// 4. Configure IDEs
	configureIDEs(parseIDESelections(*idesFlag))

	// 5. Run health validation
	runHealthCheck()

	printSummary()
}

func runUninstall() {
	log("Starting uninstallation process...")

	// 1. Stop and remove Docker containers
	if commandExists("docker") {
		log("Cleaning up Docker containers...")
		_ = exec.Command("docker", "stop", ollamaContainer, qdrantContainer).Run()
		_ = exec.Command("docker", "rm", ollamaContainer, qdrantContainer).Run()
	}

	home, _ := os.UserHomeDir()

	// 2. Remove configuration from IDEs
	log("Removing configurations from IDEs...")
	allIDEs := resolveIDEPaths(home)
	for key, ide := range allIDEs {
		removeConfigFromIDE(key, ide.path, ide.displayName)
	}

	// 3. Remove symlinks/binaries from ~/.local/bin or go/bin
	log("Removing binaries...")
	binaries := []string{"rag-code-mcp", "index-all", "ragcode-installer"}

	var binPath string
	if runtime.GOOS == "windows" {
		binPath = filepath.Join(home, "go", "bin")
	} else {
		binPath = "/usr/local/bin" // Traditional path
		if userBin, err := os.UserHomeDir(); err == nil {
			userBin = filepath.Join(userBin, ".local", "bin")
			if _, err := os.Stat(userBin); err == nil {
				binPath = userBin
			}
		}
	}

	for _, bin := range binaries {
		p := filepath.Join(binPath, bin)
		if runtime.GOOS == "windows" {
			p += ".exe"
		}
		if _, err := os.Stat(p); err == nil {
			os.Remove(p)
			log(fmt.Sprintf("Removed %s", p))
		}
	}

	// 4. Remove runtime binaries and metadata
	log("Removing application files...")
	installDir := filepath.Join(home, installDirName)
	os.RemoveAll(installDir)

	success("RagCode MCP has been uninstalled successfully.")
}

func commandExists(cmd string) bool {
	_, err := exec.LookPath(cmd)
	return err == nil
}

func removeConfigFromIDE(ideKey, path, displayName string) {
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return
	}

	var config map[string]interface{}
	if err := json.Unmarshal(data, &config); err != nil {
		return
	}

	// Determine the collection key (mcpServers or mcp-servers)
	collectionKey := "mcpServers"
	if ideKey == "vs-code" || ideKey == "copilot" || ideKey == "cursor" {
		// These IDEs use mcpServers
	} else {
		// Check if mcp-servers exists
		if _, ok := config["mcp-servers"]; ok {
			collectionKey = "mcp-servers"
		}
	}

	serversRaw, ok := config[collectionKey]
	if !ok {
		return
	}

	servers, ok := serversRaw.(map[string]interface{})
	if !ok {
		return
	}

	if _, ok := servers["ragcode"]; ok {
		delete(servers, "ragcode")
		updated, _ := json.MarshalIndent(config, "", "  ")
		if err := os.WriteFile(path, updated, 0644); err != nil {
			warn(fmt.Sprintf("Failed to write updated config to %s: %v", path, err))
		} else {
			log(fmt.Sprintf("Removed config from %s", displayName))
		}
	}
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

	// Stop any running servers before attempting to replace binary
	stopRunningServers()

	// Determine install path
	home, err := os.UserHomeDir()
	if err != nil {
		fail(fmt.Sprintf("Could not determine user home directory: %v", err))
	}
	var binDir string
	if runtime.GOOS == "windows" {
		binDir = filepath.Join(home, ".local", "share", "ragcode", "bin")
	} else {
		binDir = filepath.Join(home, ".local", "share", "ragcode", "bin")
	}
	if err := os.MkdirAll(binDir, 0755); err != nil {
		fail(fmt.Sprintf("Could not create bin directory: %v", err))
	}

	binaryName := "rag-code-mcp"
	if runtime.GOOS == "windows" {
		binaryName += ".exe"
	}
	outputBin := filepath.Join(binDir, binaryName)

	// Option 1: Check if binary exists in current directory (from extracted archive)
	if _, err := os.Stat(binaryName); err == nil {
		log(fmt.Sprintf("Found %s in current directory, copying to %s...", binaryName, binDir))
		if err := copyFile(binaryName, outputBin); err != nil {
			fail(fmt.Sprintf("Failed to copy binary: %v", err))
		}
		if err := os.Chmod(outputBin, 0755); err != nil {
			warn(fmt.Sprintf("Could not set executable flag: %v", err))
		}
		success("Binary installed successfully")
		addToPath(binDir)
		return
	}

	// Option 2: Try downloading pre-built archive
	if downloadAndExtractBinary(outputBin) {
		success("Binary downloaded and installed successfully")
		addToPath(binDir)
		return
	}

	// Option 3: Fallback to local build if source is present
	warn("Download failed – attempting local build from source.")
	if _, err := os.Stat("./cmd/rag-code-mcp"); err != nil {
		fail("Binary not found. Please download the release archive from:\nhttps://github.com/doITmagic/rag-code-mcp/releases/latest")
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

// copyFile copies a file from src to dst
func copyFile(src, dst string) error {
	sourceFile, err := os.Open(src)
	if err != nil {
		return err
	}
	defer sourceFile.Close()

	destFile, err := os.Create(dst)
	if err != nil {
		// If "text file busy", try to move the existing file aside
		if strings.Contains(err.Error(), "text file busy") {
			oldPath := dst + ".old-" + time.Now().Format("20060102-150405")
			if renameErr := os.Rename(dst, oldPath); renameErr == nil {
				// Try creating again
				destFile, err = os.Create(dst)
				if err != nil {
					return err
				}
			} else {
				return fmt.Errorf("could not move existing file %s: %v", dst, renameErr)
			}
		} else {
			return err
		}
	}
	defer destFile.Close()

	_, err = io.Copy(destFile, sourceFile)
	return err
}

// downloadAndExtractBinary fetches the release archive and extracts the binary.
func downloadAndExtractBinary(dest string) bool {
	var archiveName string
	arch := runtime.GOARCH
	switch runtime.GOOS {
	case "linux":
		archiveName = fmt.Sprintf("rag-code-mcp_linux_%s.tar.gz", arch)
	case "darwin":
		archiveName = fmt.Sprintf("rag-code-mcp_darwin_%s.tar.gz", arch)
	case "windows":
		archiveName = fmt.Sprintf("rag-code-mcp_windows_%s.zip", arch)
	default:
		return false
	}
	url := fmt.Sprintf("https://github.com/doITmagic/rag-code-mcp/releases/latest/download/%s", archiveName)
	log(fmt.Sprintf("Downloading from %s...", url))

	resp, err := http.Get(url)
	if err != nil {
		warn(fmt.Sprintf("Failed to download: %v", err))
		return false
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		if resp.StatusCode == 404 {
			warn("Release not found (404). Skipping download.")
		} else {
			warn(fmt.Sprintf("Download failed with status %d", resp.StatusCode))
		}
		return false
	}

	// Create temp file for archive
	tmpFile, err := os.CreateTemp("", "ragcode-*.tar.gz")
	if err != nil {
		warn(fmt.Sprintf("Could not create temp file: %v", err))
		return false
	}
	defer os.Remove(tmpFile.Name())
	defer tmpFile.Close()

	if _, err := io.Copy(tmpFile, resp.Body); err != nil {
		warn(fmt.Sprintf("Error downloading archive: %v", err))
		return false
	}
	tmpFile.Close()

	// Extract binary from archive
	binaryName := "rag-code-mcp"
	if runtime.GOOS == "windows" {
		binaryName += ".exe"
	}

	if runtime.GOOS == "windows" {
		// Handle zip for Windows
		warn("Windows archive extraction not yet implemented")
		return false
	}

	// Extract tar.gz
	cmd := exec.Command("tar", "-xzf", tmpFile.Name(), "-O", binaryName)
	outFile, err := os.Create(dest)
	if err != nil {
		warn(fmt.Sprintf("Could not create destination file: %v", err))
		return false
	}
	defer outFile.Close()
	cmd.Stdout = outFile

	if err := cmd.Run(); err != nil {
		warn(fmt.Sprintf("Failed to extract binary: %v", err))
		return false
	}

	if err := os.Chmod(dest, 0755); err != nil {
		warn(fmt.Sprintf("Could not set executable flag: %v", err))
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
	home, err := os.UserHomeDir()
	if err != nil {
		fail(fmt.Sprintf("Could not determine user home directory: %v", err))
	}

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

	client := &http.Client{Timeout: 2 * time.Second}

	// Setup Qdrant
	qdrantReady := false
	resp, err := client.Get("http://127.0.0.1:6333/readyz")
	if err == nil {
		resp.Body.Close()
		qdrantReady = true
		success("Qdrant detected and responding on port 6333 - using existing instance")
	}

	if !qdrantReady && *qdrantMode == "docker" {
		home, err := os.UserHomeDir()
		if err != nil {
			fail(fmt.Sprintf("Could not determine user home directory: %v", err))
		}
		dataDir := filepath.Join(home, ".local", "share", "qdrant")
		if err := os.MkdirAll(dataDir, 0755); err != nil {
			fail(fmt.Sprintf("Could not create Qdrant data dir: %v", err))
		}

		qdrantArgs := []string{
			"-p", "6333:6333",
			"-p", "6334:6334",
			"-v", fmt.Sprintf("%s:/qdrant/storage", dataDir),
		}
		startDockerContainer(qdrantContainer, qdrantImage, qdrantArgs, nil)
	} else if !qdrantReady {
		log("Using remote/local Qdrant (skipping Docker setup)")
	}

	// Setup Ollama
	ollamaReady := false
	resp, err = client.Get("http://127.0.0.1:11434/api/tags")
	if err == nil {
		resp.Body.Close()
		ollamaReady = true
		success("Ollama detected and responding on port 11434 - using existing instance")
	}

	if !ollamaReady && *ollamaMode == "docker" {
		home, err := os.UserHomeDir()
		if err != nil {
			fail(fmt.Sprintf("Could determine user home directory: %v", err))
		}
		localModels := *modelsDir
		if localModels == "" {
			localModels = filepath.Join(home, ".ollama")
		}

		// Ensure local models dir exists
		if err := os.MkdirAll(localModels, 0755); err != nil {
			fail(fmt.Sprintf("Could not create Ollama models dir: %v", err))
		}

		args := []string{
			"-p", "11434:11434",
			"-v", fmt.Sprintf("%s:/root/.ollama", localModels),
			"--dns", "8.8.8.8", // Fix DNS issues in some containers
		}

		if *gpu {
			args = append(args, "--gpus", "all")
		}

		startDockerContainer(ollamaContainer, ollamaImage, args, nil)
	} else if !ollamaReady {
		log("Using local Ollama service (skipping Docker setup)")
	}

	// Wait for healthchecks
	waitForService("Ollama", "http://localhost:11434")
	waitForService("Qdrant", "http://localhost:6333/readyz")
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
	if err := exec.Command("docker", "rm", name).Run(); err != nil {
		// ignore if container didn't exist, but log other errors
		if exitErr, ok := err.(*exec.ExitError); !ok || exitErr.ExitCode() != 1 {
			warn(fmt.Sprintf("Failed to remove existing container %s: %v", name, err))
		}
	}

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

	scanner := bufio.NewScanner(resp.Body)
	buffer := make([]byte, 0, 1024)
	scanner.Buffer(buffer, 1024*1024)
	var lastLine string

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		lastLine = line

		var chunk map[string]interface{}
		if err := json.Unmarshal([]byte(line), &chunk); err != nil {
			continue
		}

		status, _ := chunk["status"].(string)
		message := status

		if detail, ok := chunk["detail"].(map[string]interface{}); ok {
			if current, ok := detail["current"].(string); ok && current != "" {
				message = current
			}
		} else if digest, ok := chunk["digest"].(string); ok && digest != "" && status != "" {
			message = fmt.Sprintf("%s %s", status, digest)
		}

		percent := ""
		if completed, ok := chunk["completed"].(float64); ok {
			if total, ok := chunk["total"].(float64); ok && total > 0 {
				pct := (completed / total) * 100
				percent = fmt.Sprintf(" %.0f%%", pct)
			}
		}

		fmt.Printf("\r   ↳ %s%s", message, percent)

		if status == "success" {
			break
		}
	}

	if err := scanner.Err(); err != nil {
		warn(fmt.Sprintf("Model download stream ended with error: %v", err))
	}

	if lastLine != "" {
		fmt.Print("\r")
	}
	fmt.Println()
	success(fmt.Sprintf("Model %s downloaded", name))
}

// --- Step 4: IDE Configuration ---

func configureIDEs(selected []string) {
	log("Configuring IDEs...")
	home, err := os.UserHomeDir()
	if err != nil {
		fail(fmt.Sprintf("Could not determine user home directory: %v", err))
	}
	paths := resolveIDEPaths(home)
	if len(paths) == 0 {
		warn("No known IDE paths detected")
		return
	}

	var binPath string
	if runtime.GOOS == "windows" {
		binPath = filepath.Join(home, "go", "bin", "rag-code-mcp.exe")
	} else {
		binPath = filepath.Join(home, installDirName, "bin", "rag-code-mcp")
	}

	selection := normalizeIdeSelection(selected)
	for key, cfg := range paths {
		shouldEnsure := selection.explicit[key]
		if !selection.auto && !shouldEnsure {
			continue
		}
		dir := filepath.Dir(cfg.path)
		if !shouldEnsure {
			if _, err := os.Stat(dir); err != nil {
				continue
			}
		} else {
			if err := os.MkdirAll(dir, 0755); err != nil {
				warn(fmt.Sprintf("Failed to create %s: %v", dir, err))
				continue
			}
		}
		updateMCPConfig(key, cfg.displayName, cfg.path, binPath)
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
		"cursor": {
			path:        filepath.Join(home, ".cursor", "mcp.config.json"),
			displayName: "Cursor",
		},
		"copilot": {
			path:        filepath.Join(home, ".aitk", "mcp.json"),
			displayName: "GitHub Copilot",
		},
		"antigravity": {
			path:        filepath.Join(home, ".gemini", "antigravity", "mcp_config.json"),
			displayName: "Antigravity",
		},
		"mcp-cli": {
			path:        filepath.Join(home, ".config", "mcp-servers.json"),
			displayName: "MCP CLI / Generic",
		},
	}

	switch runtime.GOOS {
	case "darwin":
		paths["claude"] = idePath{filepath.Join(home, "Library", "Application Support", "Claude", "mcp-servers.json"), "Claude Desktop"}
	case "windows":
		appData := os.Getenv("APPDATA")
		if appData != "" {
			paths["claude"] = idePath{filepath.Join(appData, "Claude", "mcp-servers.json"), "Claude Desktop"}
		}
	default: // Linux / others
		paths["claude"] = idePath{filepath.Join(home, ".config", "Claude", "mcp-servers.json"), "Claude Desktop"}
	}

	if vsPath, ok := determineVSCodePath(home); ok {
		paths["vs-code"] = vsPath
	}

	return paths
}

type ideSelection struct {
	auto     bool
	explicit map[string]bool
}

func determineVSCodePath(home string) (idePath, bool) {
	var userDir string
	switch runtime.GOOS {
	case "windows":
		appData := os.Getenv("APPDATA")
		if appData == "" {
			return idePath{}, false
		}
		userDir = filepath.Join(appData, "Code", "User")
	case "darwin":
		userDir = filepath.Join(home, "Library", "Application Support", "Code", "User")
	default:
		userDir = filepath.Join(home, ".config", "Code", "User")
	}

	newPath := filepath.Join(userDir, "mcp.json")
	legacyPath := filepath.Join(userDir, "globalStorage", "mcp-servers.json")
	chosen := newPath
	if _, err := os.Stat(newPath); os.IsNotExist(err) {
		if _, legacyErr := os.Stat(legacyPath); legacyErr == nil {
			chosen = legacyPath
		}
	}

	return idePath{path: chosen, displayName: "VS Code"}, true
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

func updateMCPConfig(ideKey, displayName, path, binPath string) {
	config := make(map[string]interface{})

	// Read existing
	if data, err := os.ReadFile(path); err == nil {
		if err := json.Unmarshal(data, &config); err != nil {
			warn(fmt.Sprintf("Failed to parse existing MCP config %s: %v", path, err))
		}
	}

	collectionKey := "mcpServers"
	if ideKey == "vs-code" || ideKey == "copilot" {
		collectionKey = "servers"
	}

	servers := make(map[string]interface{})
	if existing, ok := config[collectionKey].(map[string]interface{}); ok {
		servers = existing
	}

	// Migration: Clean up legacy server keys
	legacyKeys := []string{"coderag", "do-ai", "ragcond"}
	for _, lk := range legacyKeys {
		if _, exists := servers[lk]; exists {
			delete(servers, lk)
			log(fmt.Sprintf("Migrating legacy config: removed %s", lk))
		}
	}

	servers["ragcode"] = buildMCPServerEntry(ideKey, binPath)
	config[collectionKey] = servers

	data, _ := json.MarshalIndent(config, "", "  ")
	if err := os.MkdirAll(filepath.Dir(path), 0755); err == nil {
		if err := os.WriteFile(path, data, 0644); err == nil {
			success(fmt.Sprintf("Configured %s", displayName))
		}
	}
}

func buildMCPServerEntry(ideKey, binPath string) map[string]interface{} {
	// default json for ide's cursor , antigravity , claude
	entry := map[string]interface{}{
		"command": binPath,
		"args":    []string{},
		"env": map[string]string{
			"OLLAMA_BASE_URL": "http://localhost:11434",
			"OLLAMA_MODEL":    defaultModel,
			"OLLAMA_EMBED":    defaultEmbed,
			"QDRANT_URL":      "http://localhost:6333",
		},
	}

	// add specific fields for each ide
	switch ideKey {
	case "vs-code", "copilot":
		entry["alwaysAllow"] = []string{
			"search_code",
			"search_local_index",
			"get_function_details",
			"find_type_definition",
			"get_code_context",
			"list_package_exports",
			"find_implementations",
			"search_docs",
			"hybrid_search",
			"index_workspace",
		}
	case "windsurf":
		entry["disabled"] = false
	default:
		// Other IDEs currently don't need extra fields
	}

	return entry
}

func printSummary() {
	fmt.Println("\n" + green + "Installation Complete! 🚀" + reset)
	fmt.Println("────────────────────────────────────────────")
	fmt.Println("RagCode MCP Server is running and configured.")
	fmt.Println("\nTry it in your IDE:")
	fmt.Println("  - VS Code: Open Copilot Chat and type '@ragcode'")
	fmt.Println("  - Claude:  Enable MCP in settings")
	fmt.Println("  - Cursor:  Check MCP settings")
	fmt.Println("\n💡 First Time Setup - Index Your Workspace:")
	fmt.Println("   After opening your IDE, ask the AI to index your project:")
	fmt.Println("")
	fmt.Println("   Suggested AI Prompt:")
	fmt.Println("   Please use the RagCode MCP tool 'index_workspace' to index this project for semantic code search.")
	fmt.Println("   Provide the file_path parameter pointing to any file in this workspace. Once indexing completes, I'll be")
	fmt.Println("   able to use search_code, get_function_details, and other tools to help you navigate and understand the codebase.")
	fmt.Println("")
	fmt.Println("   Note: Indexing runs in the background and may take a few minutes depending on project size.")
	fmt.Println("   You can start using search immediately - results will improve as indexing progresses.")
	fmt.Println("")
	fmt.Println("   Repeat this for each project you want to work with.")
	fmt.Println("\nTo troubleshoot, run: rag-code-mcp")
}

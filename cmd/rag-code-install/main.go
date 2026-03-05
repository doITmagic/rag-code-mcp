package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/doITmagic/rag-code-mcp/internal/config"
	"github.com/doITmagic/rag-code-mcp/internal/healthcheck"
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
		runUninstall()
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

	// 2. Determine current location
	execPath, _ := os.Executable()
	sourceDir := filepath.Dir(execPath)
	resourceDir := sourceDir

	// If we are running from bin/, resources might be in the parent dir
	if _, err := os.Stat(filepath.Join(resourceDir, "README.md")); os.IsNotExist(err) {
		parentDir := filepath.Dir(resourceDir)
		if _, err := os.Stat(filepath.Join(parentDir, "README.md")); err == nil {
			resourceDir = parentDir
		}
	}

	// 3. Define files to install
	binaries := []string{"rag-code-mcp", "rag-code-install"}
	resources := []string{"README.md", "llms.txt", "LICENSE", "config.yaml"}

	log("Copying files from: " + sourceDir)

	// Copy Binaries to bin/
	for _, b := range binaries {
		srcName := b
		if runtime.GOOS == "windows" {
			srcName += ".exe"
		}
		src := filepath.Join(sourceDir, srcName)
		dst := filepath.Join(binPath, srcName)

		if err := copyFile(src, dst); err != nil {
			warn(fmt.Sprintf("Skipping: %s not found in source directory", b))
			continue
		}
		if err := os.Chmod(dst, 0755); err != nil {
			warn(fmt.Sprintf("Failed to set executable permissions for %s: %v", b, err))
		}
		success("Installed binary: " + b)
	}

	// Copy Resources to bin/
	for _, r := range resources {
		src := filepath.Join(resourceDir, r)
		dst := filepath.Join(binPath, r)

		if r == "config.yaml" {
			if _, err := os.Stat(dst); err == nil {
				log("config.yaml already exists - keeping existing configuration.")
				checkConfigUpgrade(dst)
				continue
			}
		}

		if err := copyFile(src, dst); err != nil {
			warn(fmt.Sprintf("Skipping: %s not found in source directory", r))
		} else {
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

	log("Stopping existing process running: " + binPath)

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

	// Give it a moment to release file handles
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

func runUninstall() {
	home, _ := os.UserHomeDir()
	installPath := filepath.Join(home, installDirName)
	binPath := filepath.Join(installPath, binDirName)
	targetBin := filepath.Join(binPath, "rag-code-mcp")
	if runtime.GOOS == "windows" {
		targetBin += ".exe"
	}

	log("Uninstalling RagCode MCP...")
	fmt.Println()

	// 1. Stop running processes
	log("Step 1/7: Stopping running processes...")
	stopRunningProcess(targetBin)
	success("Processes stopped")

	// 2. Clean per-workspace .ragcode/ directories (read registry BEFORE deleting ~/.ragcode)
	log("Step 2/7: Cleaning per-workspace data...")
	cleanWorkspaceData(home)

	// 3. Remove installation directory
	log("Step 3/7: Removing installation directory: " + installPath)
	if _, err := os.Stat(installPath); err == nil {
		if err := os.RemoveAll(installPath); err != nil {
			warn(fmt.Sprintf("Failed to remove %s: %v", installPath, err))
		} else {
			success("Removed " + installPath)
		}
	} else {
		log("Directory not found, skipping: " + installPath)
	}

	// Also clean up legacy paths if they exist
	legacyPaths := []string{
		filepath.Join(home, ".local", "share", "ragcode"),
		filepath.Join(home, ".local", "state", "ragcode"),
	}
	for _, lp := range legacyPaths {
		if _, err := os.Stat(lp); err == nil {
			if err := os.RemoveAll(lp); err != nil {
				warn(fmt.Sprintf("Failed to remove legacy path %s: %v", lp, err))
			} else {
				success("Removed legacy path: " + lp)
			}
		}
	}

	// 4. Clean PATH from shell configs (both current and legacy paths)
	log("Step 4/7: Cleaning PATH from shell configuration...")
	removeFromShellConfig(home, binPath)
	// Also clean legacy PATH entries
	removeFromShellConfig(home, filepath.Join(home, ".local", "share", "ragcode", "bin"))

	// 5. Remove ragcode entries from IDE MCP configs
	log("Step 5/7: Removing ragcode from IDE configurations...")
	removeFromIDEConfigs(home)

	// 6. Stop and remove Docker containers
	log("Step 6/7: Cleaning up Docker containers...")
	removeDockerResources()

	// 7. Clean Qdrant collections (if Qdrant is still reachable)
	log("Step 7/7: Cleaning Qdrant collections...")
	cleanQdrantCollections()

	fmt.Println()
	success("RagCode MCP fully uninstalled!")
}

// removeFromShellConfig removes the RagCode PATH export from .bashrc/.zshrc.
func removeFromShellConfig(home, binDir string) {
	shellConfigs := []string{
		filepath.Join(home, ".bashrc"),
		filepath.Join(home, ".zshrc"),
	}

	for _, cfg := range shellConfigs {
		content, err := os.ReadFile(cfg)
		if err != nil {
			continue
		}

		original := string(content)
		if !strings.Contains(original, "RagCode") && !strings.Contains(original, binDir) {
			continue
		}

		// Remove the RagCode block (comment + export line + surrounding blank lines)
		lines := strings.Split(original, "\n")
		var cleaned []string
		skip := false
		for _, line := range lines {
			trimmed := strings.TrimSpace(line)
			if trimmed == "# RagCode MCP" {
				skip = true
				continue
			}
			if skip && strings.Contains(line, binDir) {
				skip = false
				continue
			}
			skip = false
			cleaned = append(cleaned, line)
		}

		newContent := strings.Join(cleaned, "\n")
		if newContent != original {
			if err := os.WriteFile(cfg, []byte(newContent), 0644); err != nil {
				warn("Failed to clean " + cfg + ": " + err.Error())
			} else {
				success("Cleaned PATH from " + cfg)
			}
		}
	}
}

// removeFromIDEConfigs removes the "ragcode" entry from all known IDE MCP config files.
func removeFromIDEConfigs(home string) {
	paths := resolveIDEPaths(home)
	for key, ide := range paths {
		if key == "zed" {
			removeZedRagcodeEntry(ide.displayName, ide.path)
			continue
		}
		removeRagcodeFromJSON(ide.displayName, ide.path)
	}
}

// removeRagcodeFromJSON removes the "ragcode" key from mcpServers/servers in a JSON config file.
func removeRagcodeFromJSON(displayName, path string) {
	data, err := os.ReadFile(path)
	if err != nil {
		return // File doesn't exist
	}

	var configMap map[string]interface{}
	if err := json.Unmarshal(data, &configMap); err != nil {
		return
	}

	modified := false
	for _, key := range []string{"mcpServers", "servers"} {
		servers, ok := configMap[key].(map[string]interface{})
		if !ok {
			continue
		}
		if _, exists := servers["ragcode"]; exists {
			delete(servers, "ragcode")
			configMap[key] = servers
			modified = true
		}
	}

	if modified {
		newData, _ := json.MarshalIndent(configMap, "", "  ")
		if err := os.WriteFile(path, newData, 0644); err != nil {
			warn("Failed to update " + displayName + ": " + err.Error())
		} else {
			success("Removed ragcode from " + displayName + " (" + path + ")")
		}
	}
}

// removeZedRagcodeEntry removes ragcode from Zed's context_servers in settings.json.
func removeZedRagcodeEntry(displayName, path string) {
	data, err := os.ReadFile(path)
	if err != nil {
		return
	}

	var configMap map[string]interface{}
	if err := json.Unmarshal(data, &configMap); err != nil {
		return
	}

	servers, ok := configMap["context_servers"].(map[string]interface{})
	if !ok {
		return
	}
	if _, exists := servers["ragcode"]; !exists {
		return
	}

	delete(servers, "ragcode")
	configMap["context_servers"] = servers

	newData, _ := json.MarshalIndent(configMap, "", "  ")
	if err := os.WriteFile(path, newData, 0644); err != nil {
		warn("Failed to update " + displayName + ": " + err.Error())
	} else {
		success("Removed ragcode from " + displayName + " (" + path + ")")
	}
}

// removeDockerResources stops and removes ragcode Docker containers and volumes.
func removeDockerResources() {
	if _, err := exec.LookPath("docker"); err != nil {
		log("Docker not found, skipping container cleanup")
		return
	}

	containers := []string{"ragcode-qdrant", "ragcode-ollama"}
	for _, name := range containers {
		// Check if container exists
		check := exec.Command("docker", "inspect", name)
		if err := check.Run(); err != nil {
			continue // Doesn't exist
		}

		// Stop
		_ = exec.Command("docker", "stop", name).Run()
		// Remove
		if err := exec.Command("docker", "rm", "-f", name).Run(); err != nil {
			warn("Failed to remove container " + name + ": " + err.Error())
		} else {
			success("Removed Docker container: " + name)
		}
	}

	// Remove volumes
	volumes := []string{"ragcode-qdrant-data"}
	for _, vol := range volumes {
		check := exec.Command("docker", "volume", "inspect", vol)
		if err := check.Run(); err != nil {
			continue
		}
		if err := exec.Command("docker", "volume", "rm", vol).Run(); err != nil {
			warn("Failed to remove volume " + vol + ": " + err.Error())
		} else {
			success("Removed Docker volume: " + vol)
		}
	}
}

// cleanWorkspaceData reads the registry to find indexed workspaces and removes their .ragcode/ dirs.
func cleanWorkspaceData(home string) {
	registryPath := filepath.Join(home, installDirName, "registry.json")

	data, err := os.ReadFile(registryPath)
	if err != nil {
		// Registry doesn't exist or unreadable — try common project directories
		log("Registry not found, scanning common project directories...")
		scanAndCleanRagcodeDirs(home)
		return
	}

	// Registry is a JSON object with workspace paths as keys
	var registry map[string]interface{}
	if err := json.Unmarshal(data, &registry); err != nil {
		warn("Could not parse registry: " + err.Error())
		scanAndCleanRagcodeDirs(home)
		return
	}

	cleaned := 0
	for wsPath := range registry {
		ragDir := filepath.Join(wsPath, ".ragcode")
		if _, err := os.Stat(ragDir); err == nil {
			if err := os.RemoveAll(ragDir); err != nil {
				warn(fmt.Sprintf("Failed to remove %s: %v", ragDir, err))
			} else {
				success("Removed workspace data: " + ragDir)
				cleaned++
			}
		}
	}

	if cleaned == 0 {
		log("No per-workspace .ragcode/ directories found")
	}
}

// scanAndCleanRagcodeDirs scans common project directories for .ragcode/ folders.
func scanAndCleanRagcodeDirs(home string) {
	// Common project root directories
	searchRoots := []string{
		filepath.Join(home, "Projects"),
		filepath.Join(home, "projects"),
		filepath.Join(home, "go", "src"),
		filepath.Join(home, "Code"),
		filepath.Join(home, "code"),
		filepath.Join(home, "dev"),
		filepath.Join(home, "workspace"),
	}

	cleaned := 0
	for _, root := range searchRoots {
		if _, err := os.Stat(root); os.IsNotExist(err) {
			continue
		}
		// Walk up to 4 levels deep looking for .ragcode directories
		_ = filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
			if err != nil {
				return nil
			}
			// Limit depth
			rel, _ := filepath.Rel(root, path)
			if strings.Count(rel, string(os.PathSeparator)) > 4 {
				return filepath.SkipDir
			}
			if info.IsDir() && info.Name() == ".ragcode" {
				if err := os.RemoveAll(path); err != nil {
					warn(fmt.Sprintf("Failed to remove %s: %v", path, err))
				} else {
					success("Removed workspace data: " + path)
					cleaned++
				}
				return filepath.SkipDir
			}
			return nil
		})
	}

	if cleaned == 0 {
		log("No per-workspace .ragcode/ directories found")
	}
}

// cleanQdrantCollections connects to Qdrant and deletes all ragcode-* collections.
func cleanQdrantCollections() {
	// Check if Qdrant is reachable
	conn, err := net.DialTimeout("tcp", "127.0.0.1:6333", 2*time.Second)
	if err != nil {
		log("Qdrant not reachable on port 6333, skipping collection cleanup")
		return
	}
	conn.Close()

	// List all collections via Qdrant REST API
	cmd := exec.Command("curl", "-s", "http://localhost:6333/collections")
	output, err := cmd.Output()
	if err != nil {
		warn("Could not list Qdrant collections: " + err.Error())
		return
	}

	var resp struct {
		Result struct {
			Collections []struct {
				Name string `json:"name"`
			} `json:"collections"`
		} `json:"result"`
	}
	if err := json.Unmarshal(output, &resp); err != nil {
		warn("Could not parse Qdrant response: " + err.Error())
		return
	}

	deleted := 0
	for _, col := range resp.Result.Collections {
		if strings.HasPrefix(col.Name, "ragcode-") {
			delCmd := exec.Command("curl", "-s", "-X", "DELETE", "http://localhost:6333/collections/"+col.Name)
			if err := delCmd.Run(); err != nil {
				warn("Failed to delete collection " + col.Name + ": " + err.Error())
			} else {
				success("Deleted Qdrant collection: " + col.Name)
				deleted++
			}
		}
	}

	if deleted == 0 {
		log("No ragcode collections found in Qdrant")
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

	// VS Code (modern copilot mcp.json)
	if vsPath, ok := determineVSCodePath(home); ok {
		paths["vs-code"] = vsPath
	}

	return paths
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

	newPath := filepath.Join(userDir, "mcp.json") // modern copilot mcp standard
	return idePath{path: newPath, displayName: "VS Code"}, true
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

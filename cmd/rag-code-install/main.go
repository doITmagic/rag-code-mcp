package main

import (
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
)

const (
	installDirName = ".local/share/ragcode"
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

	// Copy Resources to root/
	for _, r := range resources {
		src := filepath.Join(sourceDir, r)
		dst := filepath.Join(installPath, r)

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

	// 6. Health Check
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
				// Start ollama serve in a way that doesn't block the installer
				go func() {
					if err := exec.Command("ollama", "serve").Run(); err != nil {
						fmt.Printf("⚠️  Warning: Failed to serve ollama: %v\n", err)
					}
				}()
				// Give it a few seconds to bind to the port
				log("Waiting for Ollama to bind to port 11434...")
				for i := 0; i < 10; i++ {
					if isPortOpen(11434) {
						success("Ollama service started successfully")
						break
					}
					time.Sleep(1 * time.Second)
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

	// Attempt to load config to see which models are required
	execPath, _ := os.Executable()
	sourceDir := filepath.Dir(execPath)
	cfgPath := filepath.Join(sourceDir, "config.yaml")

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
		exec.Command("taskkill", "/F", "/IM", filepath.Base(binPath)).Run()
		time.Sleep(500 * time.Millisecond)
		return
	}

	// 1. Precise kill using full path
	exec.Command("pkill", "-f", binPath).Run()

	// 2. Fallback using lsof to find PIDs mapping this binary
	if _, err := exec.LookPath("lsof"); err == nil {
		cmd := exec.Command("lsof", "-t", binPath)
		if output, err := cmd.Output(); err == nil {
			pids := strings.Fields(string(output))
			for _, pid := range pids {
				exec.Command("kill", "-9", pid).Run()
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
	log("Uninstalling RagCode from: " + installPath)
	if err := os.RemoveAll(installPath); err != nil {
		fail(fmt.Sprintf("Failed to uninstall: %v", err))
	}
	success("Uninstallation complete.")
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

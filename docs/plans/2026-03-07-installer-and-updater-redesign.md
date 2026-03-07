# RagCode MCP Installer and Updater Redesign

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Refactor the installer and updater so that the installer behaves as a smart, standalone process that manages dependencies directly from its directory (or downloads the latest release if missing), and the updater completely defers installation to the installer by downloading the archive, extracting it, and spawning the new installer.

**Architecture:** 
1. `rag-code-install` will no longer use a fallback directory. It will check if required files exist alongside it. If not, it will download the latest release archive, extract it to a temporary directory, and copy them to `~/.ragcode/bin`.
2. `internal/updater/updater.go` will be stripped of its manual file extraction, replacement logic, and process killing logic. Instead, `ApplyUpdate` will download the new archive, extract it fully to a temporary directory, spawn `rag-code-install --upgrade -y` from there, and immediately `os.Exit(0)`.
3. Process stopping logic will be centralized and improved in `rag-code-install` (no more brutal `pkill -f` from the updater).

**Tech Stack:** Go standard library, HTTP clients, existing file moving/copying patterns.

---

### Task 1: Refactor `rag-code-install` File Resolution

**Files:**
- Modify: `cmd/rag-code-install/main.go`

**Step 1: Write the minimal implementation**

Remove the directory fallback logic and implement strict relative file seeking. If files are missing, introduce a download-on-demand mechanism for the archive.

```go
// In main() of cmd/rag-code-install/main.go, replace the file discovery section:
	// ... (after StopRunningProcess)
	// 1. Create directory structure
	if err := os.MkdirAll(binPath, 0755); err != nil {
		fail(fmt.Sprintf("Failed to create directories: %v", err))
	}

	// 2. Determine current location strictly
	execPath, _ := os.Executable()
	sourceDir := filepath.Dir(execPath)

	binaries := []string{"rag-code-mcp", "rag-code-install"}
	resources := []string{"README.md", "llms.txt", "LICENSE", "config.yaml"}

	// Fast Check: Are files present?
	missingFiles := false
	for _, f := range append(binaries, resources...) {
		name := f
		if runtime.GOOS == "windows" && (f == "rag-code-mcp" || f == "rag-code-install") {
			name += ".exe"
		}
		if _, err := os.Stat(filepath.Join(sourceDir, name)); os.IsNotExist(err) {
			missingFiles = true
			break
		}
	}

	// If files are missing, we must download the latest release and use its contents as source
	tempDir := ""
	if missingFiles {
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

	// ... continue with loop copying files exactly from sourceDir
```

**Step 2: Add Download and Extract Helpers**

We need `downloadAndExtractLatest()` implemented in `cmd/rag-code-install/main.go`. We also need to add `github.com/doITmagic/rag-code-mcp/internal/updater` dependency to find the latest version, or simply re-use the net fetching logic. 

```go
// Add near the bottom of main.go

import (
    "context"
    // ...other imports...
	"github.com/doITmagic/rag-code-mcp/internal/updater"
    "github.com/codeclysm/extract/v3"
)

func downloadAndExtractLatest() (string, error) {
    // We pass "v0.0.0" to force a network check to get the absolute latest if we're a naked installer
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

    archivePath := filepath.Join(tempDir, "release-archive")
    log("Downloading " + info.AssetURL)
    
    // Simple download
    req, err := http.NewRequestWithContext(ctx, "GET", info.AssetURL, nil)
	if err != nil {
		return "", err
	}
	client := &http.Client{Timeout: 5 * time.Minute}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("failed to download: status %d", resp.StatusCode)
	}

	out, err := os.Create(archivePath)
	if err != nil {
		return "", err
	}
	_, copyErr := io.Copy(out, resp.Body)
	out.Close()
	if copyErr != nil {
		return "", copyErr
	}

    log("Extracting archive...")
	f, err := os.Open(archivePath)
	if err != nil {
		return "", err
	}
	defer f.Close()
	
    extractDir := filepath.Join(tempDir, "extracted")
    os.MkdirAll(extractDir, 0755)
    
	if err := extract.Archive(ctx, f, extractDir, nil); err != nil {
        return "", err
    }

    return extractDir, nil
}
```

### Task 2: Simplify `internal/updater/updater.go`

**Files:**
- Modify: `internal/updater/updater.go`

**Step 1: Simplify `ApplyUpdate`**

```go
// Replace ApplyUpdate function completely
func ApplyUpdate(archivePath string) error {
	self, err := os.Executable()
	if err != nil {
		return fmt.Errorf("could not determine executable path: %w", err)
	}
	self, err = filepath.EvalSymlinks(self)
	if err != nil {
		return fmt.Errorf("could not resolve symlinks for %s: %w", self, err)
	}

	tempDir, err := os.MkdirTemp("", "ragcode-update-*")
	if err != nil {
		return fmt.Errorf("could not create temp dir: %w", err)
	}

	// Extract the archive completely
	if err := extractArchive(archivePath, tempDir); err != nil {
        os.RemoveAll(tempDir)
		return fmt.Errorf("failed to extract archive: %w", err)
	}

    // Find the new installer
    installerName := "rag-code-install"
    if runtime.GOOS == "windows" {
        installerName += ".exe"
    }
    installerPath := filepath.Join(tempDir, installerName)
    
    if _, err := os.Stat(installerPath); os.IsNotExist(err) {
        return fmt.Errorf("updater archive is missing the installer tool (%s)", installerName)
    }

    if err := os.Chmod(installerPath, 0755); err != nil {
        fmt.Fprintf(os.Stderr, "[WARN] Failed to chmod installer: %v\n", err)
    }

    // Spawn the installer
    cmd := exec.Command(installerPath, "--upgrade", "-y")
    cmd.Stdout = os.Stdout
    cmd.Stderr = os.Stderr
    cmd.Dir = tempDir // Essential: the installer will see the extracted files!

    // Ensure it runs independently
    // (Note: cross-platform detach mechanisms can be complex, but spawning and exiting usually works well enough for quick CLI apps)
    if err := cmd.Start(); err != nil {
        return fmt.Errorf("failed to start new installer: %w", err)
    }

    // Exit gracefully and let the installer take over
    fmt.Printf("Update handed off to new installer (PID %d). Shutting down...\n", cmd.Process.Pid)
    os.Exit(0)
    
    return nil
}

// Ensure you remove the duplicated `StopRunningProcess` function entirely from updater.go 
// as it will be exclusively handled by the installer.
```

### Task 3: Refine Process Stopping in `rag-code-install`

**Files:**
- Modify: `cmd/rag-code-install/main.go`

**Step 1: Refine `StopRunningProcess`**

Target the PID specifically by reading the `daemon.pid` file created by the server, instead of using global `pkill`.

```go
func stopRunningProcess(binPath string) {
	if _, err := os.Stat(binPath); os.IsNotExist(err) {
		return
	}

	log("Stopping existing process gracefully: " + binPath)
    
    // Attempt Graceful Shutdown using PID file
    home, err := os.UserHomeDir()
    if err == nil {
        pidPath := filepath.Join(home, ".ragcode", "daemon.pid")
        if data, err := os.ReadFile(pidPath); err == nil {
            pidStr := strings.TrimSpace(string(data))
            if pid, err := strconv.Atoi(pidStr); err == nil {
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
                    
                    // Send SIGKILL just in case it's still alive
                    _ = process.Signal(syscall.SIGKILL)
                    time.Sleep(200 * time.Millisecond)
                    return
                }
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
```

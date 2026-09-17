package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"time"
)

// Let Codex update its own TOML, preserving unrelated settings.
func updateCodexConfig(path, binPath, transport string, port int) bool {
	cli, err := exec.LookPath("codex")
	if err != nil {
		// The IDE extension bundles the CLI even when it is not on PATH.
		home, _ := os.UserHomeDir()
		binary := "codex"
		arch := runtime.GOARCH
		if arch == "amd64" {
			arch = "x86_64"
		}
		if arch == "arm64" {
			arch = "aarch64"
		}
		if runtime.GOOS == "windows" {
			binary += ".exe"
		}
		for _, editor := range []string{".vscode", ".vscode-insiders", ".cursor", ".antigravity"} {
			matches, _ := filepath.Glob(filepath.Join(home, editor, "extensions", "openai.chatgpt-*", "bin", runtime.GOOS+"-"+arch, binary))
			if len(matches) > 0 {
				cli = matches[len(matches)-1]
				break
			}
		}
	}
	if cli == "" {
		warn("Cannot configure Codex: its CLI or IDE extension executable was not found.")
		return false
	}
	args := []string{"mcp", "add", "ragcode"}
	if transport == "sse" {
		args = append(args, "--url", fmt.Sprintf("http://localhost:%d/mcp", port))
	} else {
		args = append(args, "--", binPath, "--config", filepath.Join(filepath.Dir(binPath), "config.yaml"))
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, cli, args...)
	// Set only the child process environment; do not change the user's CODEX_HOME.
	cmd.Env = append(os.Environ(), "CODEX_HOME="+filepath.Dir(path))
	if output, err := cmd.CombinedOutput(); err != nil {
		warn(fmt.Sprintf("Could not configure Codex: %v (%s)", err, output))
		return false
	}
	success(fmt.Sprintf("Configured OpenAI Codex (%s)", path))
	return true
}

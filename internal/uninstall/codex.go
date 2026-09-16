package uninstall

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"time"
)

func removeCodexEntry(home, configPath string) {
	cli := findCodexCLI(home)
	if cli == "" {
		warnMsg("Could not remove RagCode from Codex: Codex CLI was not found")
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, cli, "mcp", "remove", "ragcode")
	cmd.Env = append(os.Environ(), "CODEX_HOME="+filepath.Dir(configPath))
	if output, err := cmd.CombinedOutput(); err != nil {
		warnMsg(fmt.Sprintf("Could not remove RagCode from Codex: %v (%s)", err, output))
		return
	}
	successMsg("Removed ragcode from OpenAI Codex (" + configPath + ")")
}

func findCodexCLI(home string) string {
	if cli, err := exec.LookPath("codex"); err == nil {
		return cli
	}
	binary := "codex"
	if runtime.GOOS == "windows" {
		binary += ".exe"
	}
	arch := runtime.GOARCH
	if arch == "amd64" {
		arch = "x86_64"
	} else if arch == "arm64" {
		arch = "aarch64"
	}
	for _, editor := range []string{".vscode", ".vscode-insiders", ".cursor", ".antigravity"} {
		matches, _ := filepath.Glob(filepath.Join(home, editor, "extensions", "openai.chatgpt-*", "bin", runtime.GOOS+"-"+arch, binary))
		if len(matches) > 0 {
			return matches[len(matches)-1]
		}
	}
	return ""
}

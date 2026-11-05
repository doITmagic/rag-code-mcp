package install

import (
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/doITmagic/rag-code-mcp/v3/internal/idepaths"
)

// removeV1 deletes a RagCode v1 install (~/.local/share/ragcode): what v1's
// own uninstaller removes, plus the PATH line and Qdrant data it leaves.
// Nothing from v1 is reusable by v3. Failures are warned and skipped.
func removeV1(ctx context.Context, home string, out io.Writer) {
	share := filepath.Join(home, ".local", "share")
	installDir := filepath.Join(share, "ragcode")
	if _, err := os.Stat(installDir); err != nil {
		return
	}
	step(out, "Removing RagCode v1 (its indexes are not compatible with v3; projects are indexed again)")
	_ = exec.CommandContext(ctx, "docker", "rm", "-f", "ragcode-qdrant", "ragcode-ollama").Run()

	cfg := filepath.Join(home, ".config")
	if runtime.GOOS == "darwin" {
		cfg = filepath.Join(home, "Library", "Application Support")
	}
	mcp, servers := []string{"mcpServers"}, []string{"servers"}
	for _, t := range []idepaths.Target{
		{Path: filepath.Join(home, ".codeium", "windsurf", "mcp_config.json"), Keys: mcp},
		{Path: filepath.Join(home, ".cursor", "mcp.config.json"), Keys: mcp},
		{Path: filepath.Join(home, ".gemini", "antigravity", "mcp_config.json"), Keys: mcp},
		{Path: filepath.Join(home, ".aitk", "mcp.json"), Keys: servers},
		{Path: filepath.Join(home, ".config", "mcp-servers.json"), Keys: mcp},
		{Path: filepath.Join(cfg, "Claude", "mcp-servers.json"), Keys: mcp},
		{Path: filepath.Join(cfg, "Code", "User", "mcp.json"), Keys: servers},
		{Path: filepath.Join(cfg, "Code", "User", "globalStorage", "mcp-servers.json"), Keys: servers},
	} {
		if err := t.Remove(); err != nil {
			warn(out, err.Error())
		}
	}

	if err := os.RemoveAll(installDir); err != nil {
		warn(out, err.Error())
	}
	export := `export PATH="` + filepath.Join(installDir, "bin") + `:$PATH"`
	for _, rc := range []string{".bashrc", ".zshrc", ".profile"} {
		p := filepath.Join(home, rc)
		data, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		lines := strings.Split(string(data), "\n")
		kept := lines[:0:0]
		for _, l := range lines {
			if strings.TrimSpace(l) != export {
				kept = append(kept, l)
			}
		}
		if len(kept) != len(lines) {
			if err := os.WriteFile(p, []byte(strings.Join(kept, "\n")), 0o644); err != nil {
				warn(out, err.Error())
			}
		}
	}

	// The v1 Qdrant container wrote its data as root, so a plain delete fails.
	data := filepath.Join(share, "qdrant")
	if os.RemoveAll(data) != nil {
		if exec.CommandContext(ctx, "docker", "run", "--rm", "-v", share+":/p", "--entrypoint", "rm", qdrantImage, "-rf", "/p/qdrant").Run() != nil {
			warn(out, "could not delete "+data+"; run: sudo rm -rf "+data)
		}
	}
	ok(out, "RagCode v1 removed")
}

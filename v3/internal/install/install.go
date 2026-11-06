// Package install sets RagCode up on this machine: the server binary under
// ~/.ragcode/bin, config.yaml, the Ollama container, the embedding model,
// IDE configs and PATH.
package install

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/doITmagic/rag-code-mcp/v3/internal/config"
	"github.com/doITmagic/rag-code-mcp/v3/internal/idepaths"
	"github.com/doITmagic/rag-code-mcp/v3/internal/uninstall"
)

// Options controls Run.
type Options struct {
	Ollama    string // docker | local | skip
	GPU       bool   // try --gpus all for the Ollama container first
	Yes       bool   // replace an existing installation without asking
	ModelsDir string // host directory mounted at /root/.ollama; "" uses a volume
	SkipIDE   bool
	In        io.Reader
	Out       io.Writer

	srcDir string // where rag-code-mcp is copied from; tests set it
}

const (
	ollamaImage = "ollama/ollama"
	readyWait   = 60 * time.Second
)

// Run performs the installation. Ollama, model and IDE failures are reported
// and skipped; a missing binary fails Run at once.
func Run(ctx context.Context, o Options) error {
	if o.Out == nil {
		o.Out = os.Stdout
	}
	if o.In == nil {
		o.In = os.Stdin
	}
	if o.srcDir == "" {
		exe, err := os.Executable()
		if err != nil {
			return fmt.Errorf("install: locate installer: %w", err)
		}
		o.srcDir = filepath.Dir(exe)
	}
	switch o.Ollama {
	case "docker", "local", "skip":
	default:
		return fmt.Errorf("install: -ollama must be docker, local or skip, got %q", o.Ollama)
	}

	home := config.HomeDir()
	binary := uninstall.Binary(home)
	src := filepath.Join(o.srcDir, filepath.Base(binary))
	if _, err := os.Stat(src); err != nil {
		return fmt.Errorf("install: %s must sit next to the installer: %w", filepath.Base(binary), err)
	}
	if _, err := os.Stat(binary); err == nil && !o.Yes {
		fmt.Fprintf(o.Out, "%s is already installed. Replace it? [Y/n]: ", binary)
		line, _ := bufio.NewReader(o.In).ReadString('\n')
		if a := strings.ToLower(strings.TrimSpace(line)); a != "" && a != "y" && a != "yes" {
			return errors.New("install: cancelled")
		}
	}
	step(o.Out, "Installing binary to "+binary)
	uninstall.StopProcess(ctx, binary)
	if err := copyFile(src, binary); err != nil {
		return fmt.Errorf("install: copy binary: %w", err)
	}
	ok(o.Out, "Installed "+binary)

	cfgPath := config.DefaultPath()
	_, existed := os.Stat(cfgPath)
	cfg, err := config.Load(cfgPath) // writes the default when missing
	if err != nil {
		return err
	}
	if existed == nil {
		ok(o.Out, "Kept existing "+cfgPath)
	} else {
		ok(o.Out, "Wrote "+cfgPath)
	}
	// v1 goes only once v3 is in place, and before the ragcode-ollama
	// container (same name in v1) is started again.
	if userHome, err := os.UserHomeDir(); err == nil {
		removeV1(ctx, userHome, o.Out)
	}

	if o.Ollama == "docker" {
		if _, err := exec.LookPath("docker"); err != nil {
			warn(o.Out, "docker not found in PATH; container not started")
			o.Ollama = "local" // a native Ollama may still answer
		}
	}
	ollamaFailed := false
	if o.Ollama == "docker" {
		step(o.Out, "Starting Ollama container")
		mount := "ragcode-ollama:/root/.ollama"
		if o.ModelsDir != "" {
			if err := os.MkdirAll(o.ModelsDir, 0o755); err != nil {
				return fmt.Errorf("install: models dir: %w", err)
			}
			mount = o.ModelsDir + ":/root/.ollama"
		}
		args := []string{"-p", "127.0.0.1:11434:11434", "-v", mount}
		var err error
		if o.GPU {
			err = runContainer(ctx, o.Out, "ragcode-ollama", ollamaImage, append([]string{"--gpus", "all"}, args...)...)
			if err != nil {
				warn(o.Out, "GPU start failed, retrying on CPU: "+err.Error())
			}
		}
		if !o.GPU || err != nil {
			err = runContainer(ctx, o.Out, "ragcode-ollama", ollamaImage, args...)
		}
		if err != nil {
			warn(o.Out, err.Error()) // fall through: a native Ollama may hold the port
			ollamaFailed = true
		}
	}
	if o.Ollama != "skip" {
		step(o.Out, "Waiting for Ollama at "+cfg.OllamaBaseURL)
		if err := waitHTTP(ctx, cfg.OllamaBaseURL+"/api/tags", readyWait); err != nil {
			warn(o.Out, "Ollama: "+err.Error()+"; run manually: ollama pull "+cfg.OllamaEmbed)
		} else {
			if ollamaFailed {
				ok(o.Out, "Using the Ollama already running at "+cfg.OllamaBaseURL)
			}
			step(o.Out, "Pulling model "+cfg.OllamaEmbed)
			if err := pullModel(ctx, o.Out, cfg.OllamaBaseURL, cfg.OllamaEmbed); err != nil {
				warn(o.Out, err.Error()+"; run manually: ollama pull "+cfg.OllamaEmbed)
			} else {
				ok(o.Out, "Model "+cfg.OllamaEmbed+" ready")
			}
		}
	}

	if !o.SkipIDE {
		step(o.Out, "Configuring IDEs")
		for _, t := range idepaths.Targets() {
			switch err := t.Write(binary, cfgPath); {
			case errors.Is(err, idepaths.ErrNotInstalled):
			case err != nil:
				warn(o.Out, t.Name+": "+err.Error())
			default:
				ok(o.Out, "Configured "+t.Name+" ("+t.Path+")")
			}
		}
	}

	binDir := filepath.Dir(binary)
	if runtime.GOOS == "windows" {
		warn(o.Out, "Add "+binDir+" to your PATH: setx PATH \"%PATH%;"+binDir+"\"")
	} else if rc, added, err := addPath(binDir); err != nil {
		warn(o.Out, "PATH: "+err.Error())
	} else if added {
		ok(o.Out, "Added "+binDir+" to PATH in "+rc+" (restart your shell)")
	}

	snippet, _ := json.MarshalIndent(map[string]any{"mcpServers": map[string]any{
		"ragcode": map[string]any{"command": binary, "args": []string{"--config", cfgPath}},
	}}, "", "  ")
	fmt.Fprintf(o.Out, "\nBinary:  %s\nConfig:  %s\nOllama:  %s (model %s)\nIndex:   %s\n\nmcp.json entry for any other client:\n%s\n",
		binary, cfgPath, cfg.OllamaBaseURL, cfg.OllamaEmbed, filepath.Join(home, "index"), snippet)
	ok(o.Out, "RagCode MCP installed")
	return nil
}

// copyFile writes next to dst and renames over it, so a binary an IDE still
// runs is replaced instead of failing with "text file busy".
func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(dst), "."+filepath.Base(dst)+"-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := io.Copy(tmp, in); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Chmod(0o755); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), dst)
}

// runContainer replaces any container with that name and starts a new one.
// Docker's own output (image pull progress) goes to out.
func runContainer(ctx context.Context, out io.Writer, name, image string, args ...string) error {
	_ = exec.CommandContext(ctx, "docker", "rm", "-f", name).Run()
	full := append([]string{"run", "-d", "--name", name, "--restart", "always"}, args...)
	cmd := exec.CommandContext(ctx, "docker", append(full, image)...)
	cmd.Stdout, cmd.Stderr = out, out
	if err := cmd.Run(); err != nil {
		_ = exec.CommandContext(ctx, "docker", "rm", "-f", name).Run()
		return fmt.Errorf("docker run %s: %w", name, err)
	}
	return nil
}

// waitHTTP polls url every 500 ms until it answers 200 or max elapses.
func waitHTTP(ctx context.Context, url string, max time.Duration) error {
	client := &http.Client{Timeout: 3 * time.Second}
	deadline := time.Now().Add(max)
	for {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			return err
		}
		resp, err := client.Do(req)
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return nil
			}
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("%s not ready after %s", url, max)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(500 * time.Millisecond):
		}
	}
}

// pullModel streams POST /api/pull and prints progress on one line.
func pullModel(ctx context.Context, out io.Writer, baseURL, model string) error {
	body, _ := json.Marshal(map[string]any{"name": model, "stream": true})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, baseURL+"/api/pull", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("pull %s: %w", model, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		if resp.StatusCode == http.StatusPreconditionFailed { // old Ollama refusing a newer model
			return fmt.Errorf("pull %s: status 412: this model needs a newer Ollama, 0.40 or later: upgrade Ollama", model)
		}
		return fmt.Errorf("pull %s: status %d", model, resp.StatusCode)
	}
	sc := bufio.NewScanner(resp.Body)
	sc.Buffer(nil, 1<<20)
	last := ""
	for sc.Scan() {
		var ev struct {
			Status, Error    string
			Completed, Total int64
		}
		if json.Unmarshal(sc.Bytes(), &ev) != nil {
			continue
		}
		if ev.Error != "" {
			return fmt.Errorf("pull %s: %s", model, ev.Error)
		}
		if ev.Status == "success" {
			fmt.Fprintln(out)
			return nil
		}
		line := ev.Status
		if ev.Total > 0 {
			line = fmt.Sprintf("%s %d%%", ev.Status, ev.Completed*100/ev.Total)
		}
		if line != last {
			fmt.Fprintf(out, "\r  %-60s", line)
			last = line
		}
	}
	if err := sc.Err(); err != nil {
		return fmt.Errorf("pull %s: %w", model, err)
	}
	return fmt.Errorf("pull %s: stream ended without success", model)
}

// addPath appends the PATH export to the shell rc when binDir is not in it.
func addPath(binDir string) (rc string, added bool, err error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", false, err
	}
	rc = filepath.Join(home, ".bashrc")
	if filepath.Base(os.Getenv("SHELL")) == "zsh" {
		rc = filepath.Join(home, ".zshrc")
	}
	data, err := os.ReadFile(rc)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return rc, false, err
	}
	if bytes.Contains(data, []byte(binDir)) {
		return rc, false, nil
	}
	f, err := os.OpenFile(rc, os.O_APPEND|os.O_WRONLY|os.O_CREATE, 0o644)
	if err != nil {
		return rc, false, err
	}
	defer f.Close()
	_, err = fmt.Fprintf(f, "\n# RagCode MCP\nexport PATH=\"%s:$PATH\"\n", binDir)
	return rc, err == nil, err
}

func step(w io.Writer, s string) { fmt.Fprintf(w, "\033[0;34m==>\033[0m %s\n", s) }
func ok(w io.Writer, s string)   { fmt.Fprintf(w, "\033[0;32m✓\033[0m %s\n", s) }
func warn(w io.Writer, s string) { fmt.Fprintf(w, "\033[1;33m!\033[0m %s\n", s) }

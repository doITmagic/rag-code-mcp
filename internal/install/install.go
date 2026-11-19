// Package install sets RagCode up on this machine: the server binary under
// ~/.ragcode/bin, config.yaml, the embedding runtime (llama.cpp) and model,
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
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/doITmagic/rag-code-mcp/v2/internal/config"
	"github.com/doITmagic/rag-code-mcp/v2/internal/idepaths"
	"github.com/doITmagic/rag-code-mcp/v2/internal/llama"
	"github.com/doITmagic/rag-code-mcp/v2/internal/uninstall"
	"github.com/doITmagic/rag-code-mcp/v2/internal/update"
)

// Options controls Run.
type Options struct {
	Yes bool // replace an existing installation without asking
	// SkipDownload leaves the embedding runtime and model for the server,
	// which fetches them on first use.
	SkipDownload bool
	SkipIDE      bool
	In           io.Reader
	Out          io.Writer

	srcDir string // where rag-code-mcp is copied from; tests set it
	// ensure fetches the runtime and the model; tests replace the download.
	ensure func(context.Context, llama.Options) error
}

// Run performs the installation. Download and IDE failures are reported and
// skipped (the server fetches the runtime on first use); a missing binary
// fails Run at once.
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
	if o.ensure == nil {
		o.ensure = func(ctx context.Context, rt llama.Options) error {
			_, _, err := rt.Ensure(ctx)
			return err
		}
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
	if userHome, err := os.UserHomeDir(); err == nil {
		removeV1(ctx, userHome, o.Out) // only once v2 is in place
	}
	removeOllama(ctx, o.Out)

	rt := llama.FromConfig(cfg, func(s string) { fmt.Fprintln(o.Out, "  "+s) })
	for _, p := range llama.ServerPaths(home) { // not a llama_server of the user's own
		uninstall.StopProcess(ctx, p) // a server whose RagCode process was killed
	}
	switch {
	case o.SkipDownload:
		warn(o.Out, "Embedding runtime not downloaded (-skip-download): the server fetches it on first use")
	case rt.Installed():
		ok(o.Out, "Embedding runtime "+rt.RuntimeName()+" and model "+rt.ModelLabel()+" already present")
	default:
		step(o.Out, "Installing the embedding runtime "+rt.RuntimeName()+" and model "+rt.ModelLabel())
		if err := o.ensure(ctx, rt); err != nil {
			warn(o.Out, err.Error()+"; the server tries again on first use")
		} else {
			ok(o.Out, "Embedding runtime and model ready")
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
		warn(o.Out, "Add "+binDir+" to your PATH (cmd or PowerShell, then open a new terminal): "+windowsPathHint(binDir))
	} else if rc, added, err := addPath(binDir); err != nil {
		warn(o.Out, "PATH: "+err.Error())
	} else if added {
		ok(o.Out, "Added "+binDir+" to PATH in "+rc+" (restart your shell)")
	}

	snippet, _ := json.MarshalIndent(map[string]any{"mcpServers": map[string]any{
		"ragcode": map[string]any{"command": binary, "args": []string{"--config", cfgPath}},
	}}, "", "  ")
	fmt.Fprintf(o.Out, "\nBinary:   %s\nConfig:   %s\nEmbedder: %s (model %s)\nIndex:    %s\n\nmcp.json entry for any other client:\n%s\n",
		binary, cfgPath, rt.RuntimeName(), rt.ModelLabel(), filepath.Join(home, "index"), snippet)
	ok(o.Out, "RagCode MCP installed")
	return nil
}

// windowsPathHint is a command that appends binDir to the user Path, pasted
// in cmd or PowerShell alike (no $ for an outer PowerShell to expand). It
// reads the raw registry value and writes it back as REG_EXPAND_SZ, so
// entries such as %USERPROFILE%\AppData\Local\Microsoft\WindowsApps stay
// expandable; setting and clearing a throwaway variable broadcasts the change
// to new windows. setx PATH "%PATH%;..." would copy the machine PATH into it
// and truncate it at 1024 characters in cmd, and store a literal %PATH% in
// PowerShell; [Environment]::SetEnvironmentVariable would freeze every %VAR%.
func windowsPathHint(binDir string) string {
	return `powershell -NoProfile -Command "[Microsoft.Win32.Registry]::CurrentUser.CreateSubKey('Environment').SetValue('Path', [Microsoft.Win32.Registry]::CurrentUser.OpenSubKey('Environment').GetValue('Path', '', 'DoNotExpandEnvironmentNames') + ';` +
		strings.ReplaceAll(binDir, "'", "''") + `', 'ExpandString'); [Environment]::SetEnvironmentVariable('RAGCODE_PATH_REFRESH', '1', 'User'); [Environment]::SetEnvironmentVariable('RAGCODE_PATH_REFRESH', '', 'User')"`
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
	return update.Replace(tmp.Name(), dst) // on Windows a binary a client still runs is moved aside first
}

// removeOllama removes the ragcode-ollama container and volume an earlier
// v2 installer created: the server runs the embedding model itself now, and
// the container would keep its memory forever (--restart always).
// The server does the same on its first start (an auto-update replaces only
// the binary), through the same uninstall.RemoveOllama.
func removeOllama(ctx context.Context, out io.Writer) {
	switch removed, err := uninstall.RemoveOllama(ctx); {
	case errors.Is(err, uninstall.ErrNoDocker):
	case err != nil:
		warn(out, "the ragcode-ollama container is no longer used; remove it with: docker rm -f ragcode-ollama")
	case removed:
		ok(out, "Removed the ragcode-ollama container: RagCode no longer uses Ollama")
	}
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

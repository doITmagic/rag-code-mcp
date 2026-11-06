// Package uninstall removes RagCode from the machine: running processes, IDE
// entries, per-root .ragcode directories, Qdrant collections, ~/.ragcode and
// the PATH line. The server binary runs it for --uninstall.
package uninstall

import (
	"bufio"
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
	"strconv"
	"strings"
	"time"

	"github.com/doITmagic/rag-code-mcp/v3/engine"
	"github.com/doITmagic/rag-code-mcp/v3/internal/config"
	"github.com/doITmagic/rag-code-mcp/v3/internal/idepaths"
	"github.com/doITmagic/rag-code-mcp/v3/store"
)

// Options controls Run.
type Options struct {
	Yes    bool // remove every <root>/.ragcode without asking
	Docker bool // also remove the ragcode-qdrant and ragcode-ollama containers
	In     io.Reader
	Out    io.Writer
}

// Binary is the installed server path for the global dir.
func Binary(home string) string {
	p := filepath.Join(home, "bin", "rag-code-mcp")
	if runtime.GOOS == "windows" {
		p += ".exe"
	}
	return p
}

// Run uninstalls everything. Steps that cannot be done (service down, IDE
// absent) are reported and skipped; only reading stdin can fail Run.
func Run(ctx context.Context, o Options) error {
	if o.Out == nil {
		o.Out = os.Stdout
	}
	if o.In == nil {
		o.In = os.Stdin
	}
	home := config.HomeDir()
	step(o.Out, "Stopping rag-code-mcp processes")
	StopProcess(ctx, Binary(home))

	step(o.Out, "Removing IDE entries")
	for _, t := range idepaths.Targets() {
		if err := t.Remove(); err != nil {
			warn(o.Out, t.Name+": "+err.Error())
		}
	}

	step(o.Out, "Removing per-workspace data")
	in := bufio.NewReader(o.In)
	rs := roots(filepath.Join(home, "roots.json"))
	for _, root := range rs {
		for _, p := range engine.PointerFiles(root) {
			if err := removePointer(p); err != nil {
				warn(o.Out, err.Error())
			}
		}
		dir := filepath.Join(root, ".ragcode")
		if _, err := os.Stat(dir); err != nil {
			continue
		}
		if !o.Yes {
			fmt.Fprintf(o.Out, "Remove %s? [y/N]: ", dir)
			line, err := in.ReadString('\n')
			if err != nil && line == "" {
				return fmt.Errorf("uninstall: read answer: %w", err)
			}
			if a := strings.ToLower(strings.TrimSpace(line)); a != "y" && a != "yes" {
				continue
			}
		}
		if err := os.RemoveAll(dir); err != nil {
			warn(o.Out, err.Error())
		} else {
			ok(o.Out, "Removed "+dir)
		}
	}

	step(o.Out, "Deleting Qdrant collections")
	cfg := config.Default()
	if _, err := os.Stat(config.DefaultPath()); err == nil {
		if c, err := config.Load(config.DefaultPath()); err == nil {
			cfg = c
		}
	}
	if err := deleteCollections(ctx, cfg.QdrantURL, cfg.QdrantAPIKey, rs, o.Out); err != nil {
		warn(o.Out, "Qdrant: "+err.Error())
	}

	userHome, _ := os.UserHomeDir()
	step(o.Out, "Removing "+home)
	// RAGCODE_HOME is user input: never delete the home dir or a filesystem root.
	resolve := func(p string) string {
		if r, err := filepath.EvalSymlinks(p); err == nil {
			return r
		}
		return filepath.Clean(p)
	}
	if abs, err := filepath.Abs(home); err != nil || resolve(abs) == resolve(userHome) || filepath.Dir(abs) == abs {
		warn(o.Out, "not removing "+home+": it is the home directory or a filesystem root; check RAGCODE_HOME")
	} else if err := os.RemoveAll(home); err != nil {
		warn(o.Out, err.Error())
	}

	step(o.Out, "Removing PATH line")
	for _, rc := range []string{".bashrc", ".zshrc"} {
		if err := stripPath(filepath.Join(userHome, rc), filepath.Join(home, "bin")); err != nil {
			warn(o.Out, err.Error())
		}
	}

	if o.Docker {
		step(o.Out, "Removing Docker containers")
		for _, c := range []string{"ragcode-qdrant", "ragcode-ollama"} {
			if out, err := exec.CommandContext(ctx, "docker", "rm", "-f", c).CombinedOutput(); err != nil {
				warn(o.Out, c+": "+strings.TrimSpace(string(out)))
			}
			_ = exec.CommandContext(ctx, "docker", "volume", "rm", c).Run()
		}
	}
	ok(o.Out, "RagCode MCP uninstalled")
	return nil
}

// roots reads ~/.ragcode/roots.json, a plain []string.
func roots(path string) []string {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var rs []string
	_ = json.Unmarshal(data, &rs)
	return rs
}

// StopProcess terminates every running instance of binary except this
// process, best effort: SIGTERM, up to 5 s of polling, then SIGKILL.
func StopProcess(ctx context.Context, binary string) {
	if _, err := os.Stat(binary); err != nil {
		return
	}
	self := strconv.Itoa(os.Getpid())
	if runtime.GOOS == "windows" {
		out, _ := exec.CommandContext(ctx, "tasklist", "/FI", "IMAGENAME eq "+filepath.Base(binary), "/NH", "/FO", "CSV").Output()
		for _, line := range strings.Split(string(out), "\n") {
			if f := strings.Split(line, "\",\""); len(f) >= 2 && strings.Trim(f[1], "\"") != self {
				_ = exec.CommandContext(ctx, "taskkill", "/F", "/PID", strings.Trim(f[1], "\"")).Run()
			}
		}
		return
	}
	alive := func() []string {
		out, _ := exec.CommandContext(ctx, "pgrep", "-f", binary).Output()
		var pids []string
		for _, p := range strings.Fields(string(out)) {
			if p != self {
				pids = append(pids, p)
			}
		}
		return pids
	}
	pids := alive()
	if len(pids) == 0 {
		return
	}
	_ = exec.CommandContext(ctx, "kill", append([]string{"-TERM"}, pids...)...).Run()
	deadline := time.Now().Add(5 * time.Second)
	for len(alive()) > 0 && time.Now().Before(deadline) {
		select {
		case <-ctx.Done():
			return
		case <-time.After(100 * time.Millisecond):
		}
	}
	if pids = alive(); len(pids) > 0 {
		_ = exec.CommandContext(ctx, "kill", append([]string{"-KILL"}, pids...)...).Run()
	}
}

// deleteCollections drops the collections of roots through Qdrant's REST API.
// Only this machine's roots: the server may hold other machines' collections.
func deleteCollections(ctx context.Context, baseURL, apiKey string, roots []string, out io.Writer) error {
	own := map[string]bool{}
	for _, r := range roots {
		own[store.CollectionName(r)] = true
	}
	client := &http.Client{Timeout: 10 * time.Second}
	do := func(method, path string) (*http.Response, error) {
		req, err := http.NewRequestWithContext(ctx, method, strings.TrimRight(baseURL, "/")+path, nil)
		if err != nil {
			return nil, err
		}
		if apiKey != "" {
			req.Header.Set("api-key", apiKey)
		}
		resp, err := client.Do(req)
		if err != nil {
			return nil, err
		}
		if resp.StatusCode/100 != 2 {
			resp.Body.Close()
			return nil, fmt.Errorf("%s %s: status %d", method, path, resp.StatusCode)
		}
		return resp, nil
	}
	resp, err := do(http.MethodGet, "/collections")
	if err != nil {
		return fmt.Errorf("not reachable, collections kept (%w)", err)
	}
	var list struct {
		Result struct {
			Collections []struct{ Name string } `json:"collections"`
		} `json:"result"`
	}
	err = json.NewDecoder(resp.Body).Decode(&list)
	resp.Body.Close()
	if err != nil {
		return err
	}
	for _, c := range list.Result.Collections {
		if !own[c.Name] {
			continue
		}
		if r, err := do(http.MethodDelete, "/collections/"+c.Name); err != nil {
			warn(out, err.Error())
		} else {
			r.Body.Close()
			ok(out, "Deleted collection "+c.Name)
		}
	}
	return nil
}

// removePointer drops the engine.IDEPointer line WriteIDERules added to path,
// and the file itself when only the ragcode.mdc header is left. Like the
// writer, it never follows a symlink.
func removePointer(path string) error {
	if info, err := os.Lstat(path); err != nil || info.Mode()&os.ModeSymlink != 0 {
		return nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	lines := strings.Split(string(data), "\n")
	kept := lines[:0:0]
	for _, l := range lines {
		if strings.TrimSpace(l) != engine.IDEPointer {
			kept = append(kept, l)
		}
	}
	if len(kept) == len(lines) {
		return nil
	}
	s := strings.Join(kept, "\n")
	if filepath.Base(path) == "ragcode.mdc" && s == "---\nalwaysApply: true\n---\n" {
		return os.Remove(path)
	}
	return os.WriteFile(path, []byte(s), 0o644)
}

// stripPath removes the "# RagCode MCP" + export pair the installer wrote.
func stripPath(rc, binDir string) error {
	data, err := os.ReadFile(rc)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	export := `export PATH="` + binDir + `:$PATH"`
	lines := strings.Split(string(data), "\n")
	kept := lines[:0:0]
	for i := 0; i < len(lines); i++ {
		if strings.TrimSpace(lines[i]) == "# RagCode MCP" && i+1 < len(lines) && strings.TrimSpace(lines[i+1]) == export {
			i++
			continue
		}
		kept = append(kept, lines[i])
	}
	if len(kept) == len(lines) {
		return nil
	}
	return os.WriteFile(rc, []byte(strings.Join(kept, "\n")), 0o644)
}

func step(w io.Writer, s string) { fmt.Fprintf(w, "\033[0;34m==>\033[0m %s\n", s) }
func ok(w io.Writer, s string)   { fmt.Fprintf(w, "\033[0;32m✓\033[0m %s\n", s) }
func warn(w io.Writer, s string) { fmt.Fprintf(w, "\033[1;33m!\033[0m %s\n", s) }

// Package uninstall removes RagCode from the machine: running processes, IDE
// entries, per-root .ragcode directories, ~/.ragcode (with every index) and
// the PATH line. The server binary runs it for --uninstall.
package uninstall

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/doITmagic/rag-code-mcp/v2/engine"
	"github.com/doITmagic/rag-code-mcp/v2/internal/config"
	"github.com/doITmagic/rag-code-mcp/v2/internal/idepaths"
	"github.com/doITmagic/rag-code-mcp/v2/internal/llama"
)

// Options controls Run.
type Options struct {
	Yes    bool // remove every <root>/.ragcode without asking
	Docker bool // also remove the ragcode-ollama and ragcode-qdrant containers of earlier versions
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
	for _, p := range llama.ServerPaths(home) {
		StopProcess(ctx, p) // an embedding server whose RagCode process was killed
	}

	step(o.Out, "Removing IDE entries")
	targets := idepaths.Targets()
	if appData := os.Getenv("APPDATA"); appData != "" {
		// Earlier Windows installs wrote Cursor's entry here; Cursor never read it.
		targets = append(targets, idepaths.Target{Name: "Cursor (old path)", Path: filepath.Join(appData, "Cursor", "mcp.json"), Keys: []string{"mcpServers"}})
	}
	for _, t := range targets {
		if err := t.Remove(); err != nil {
			warn(o.Out, t.Name+": "+err.Error())
		}
	}

	step(o.Out, "Removing per-workspace data")
	in := bufio.NewReader(o.In)
	rs, err := engine.ReadRoots()
	if err != nil {
		warn(o.Out, err.Error()+": per-workspace .ragcode folders are not removed")
	}
	for _, root := range rs {
		for _, p := range engine.PointerFiles(root) {
			if err := removePointer(root, p); err != nil {
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

	userHome, _ := os.UserHomeDir()
	step(o.Out, "Removing "+home)
	// RAGCODE_HOME is user input: never delete the home dir or a filesystem root.
	resolve := func(p string) string {
		if r, err := filepath.EvalSymlinks(p); err == nil {
			return r
		}
		return filepath.Clean(p)
	}
	if abs, err := filepath.Abs(home); err != nil || resolve(abs) == resolve(userHome) || filepath.Dir(abs) == abs ||
		strings.HasPrefix(resolve(userHome), resolve(abs)+string(filepath.Separator)) {
		warn(o.Out, "not removing "+home+": it is the home directory, one of its parents or a filesystem root; check RAGCODE_HOME")
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
		if out, err := exec.CommandContext(ctx, "docker", "rm", "-f", "ragcode-qdrant").CombinedOutput(); err != nil {
			warn(o.Out, "ragcode-qdrant: "+strings.TrimSpace(string(out)))
		}
		_ = exec.CommandContext(ctx, "docker", "volume", "rm", "ragcode-qdrant").Run()
		// The Ollama container goes only when it is RagCode's; its volume goes by
		// name, because this flag is the explicit request to remove it.
		switch out, err := RemoveOllama(ctx); {
		case errors.Is(err, ErrNoDocker):
			warn(o.Out, err.Error())
		case err != nil:
			warn(o.Out, ollamaName+": "+err.Error())
		case out == Foreign:
			warn(o.Out, "container "+ollamaName+" is not an Ollama of RagCode, left alone")
		}
		if err := RemoveOllamaVolume(ctx); err != nil && !errors.Is(err, ErrNoDocker) {
			warn(o.Out, err.Error())
		}
	}
	ok(o.Out, "RagCode MCP uninstalled")
	return nil
}

// StopProcess terminates every running instance of binary except this
// process, best effort: SIGTERM, up to 5 s of polling, then SIGKILL.
func StopProcess(ctx context.Context, binary string) {
	if _, err := os.Stat(binary); err != nil {
		return
	}
	if runtime.GOOS == "windows" {
		if abs, err := filepath.Abs(binary); err == nil {
			binary = abs
		}
		stopWindows(binary, os.Getpid())
		return
	}
	self := strconv.Itoa(os.Getpid())
	alive := func() []string {
		// Anchored: argv[0] must be the binary, so a shell, tail -f or a
		// launcher wrapper that only mentions the path is left alone.
		out, _ := exec.CommandContext(ctx, "pgrep", "-f", "^"+regexp.QuoteMeta(binary)+"( |$)").Output()
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

// sameExe reports whether the image path of a running process is binary.
// Windows only: the comparison ignores case, as NTFS does.
func sameExe(path, binary string) bool {
	if path == "" {
		return false
	}
	if strings.EqualFold(filepath.Clean(path), filepath.Clean(binary)) {
		return true
	}
	a, err := os.Stat(path)
	b, berr := os.Stat(binary)
	return err == nil && berr == nil && os.SameFile(a, b) // 8.3 short names
}

// removePointer drops the engine.IDEPointer line WriteIDERules added to path,
// and the file itself when only the ragcode.mdc header is left. Like the
// writer, it never follows a symlink, of the file or of its directory out of
// the workspace root.
func removePointer(root, path string) error {
	real, err := filepath.EvalSymlinks(root)
	dir, derr := filepath.EvalSymlinks(filepath.Dir(path))
	if err != nil || derr != nil || dir != real && !strings.HasPrefix(dir, real+string(filepath.Separator)) {
		return nil
	}
	if info, err := os.Lstat(path); err != nil || !info.Mode().IsRegular() {
		return nil // a symlink, or a FIFO that would block the read forever
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

// ErrNoDocker says RemoveOllama could not ask Docker: it is not on PATH or
// the daemon did not answer.
var ErrNoDocker = errors.New("docker is not available")

// Outcome is what is known about the container named ragcode-ollama.
type Outcome int

const (
	Absent  Outcome = iota // no such container
	Ours                   // an Ollama of RagCode, still there
	Foreign                // something else under that name: never touched
	Removed                // an Ollama of RagCode, removed
)

const (
	ollamaName = "ragcode-ollama"
	ollamaDir  = "/root/.ollama" // where every RagCode version mounted the models
)

// FindOllama says whether the container ragcode-ollama exists and is the one
// RagCode v1 and v2 created: its image is ollama/ollama (any tag or digest,
// any registry prefix) and it mounts ollamaDir. RagCode set no label, so
// nothing else proves it. volume is the named volume ragcode-ollama that
// holds the models, only when that is what the container mounts at ollamaDir;
// a host directory (-models-dir, v1) is the user's and gives no volume.
// The error is ErrNoDocker when Docker could not be asked, otherwise why
// the container could not be read; nothing is removed here.
func FindOllama(ctx context.Context) (c Outcome, volume string, err error) {
	if _, err := exec.LookPath("docker"); err != nil {
		return Absent, "", ErrNoDocker
	}
	listed, err := ollamaListed(ctx)
	if err != nil {
		return Absent, "", fmt.Errorf("%w: %w", ErrNoDocker, err)
	}
	if !listed {
		return Absent, "", nil
	}
	out, err := exec.CommandContext(ctx, "docker", "container", "inspect", "--format",
		"{{.Config.Image}}|{{range .Mounts}}{{.Type}}={{.Name}}={{.Destination}};{{end}}", ollamaName).CombinedOutput()
	if err != nil {
		if strings.Contains(strings.ToLower(string(out)), "no such container") {
			return Absent, "", nil // removed between ps and inspect
		}
		return Absent, "", fmt.Errorf("docker container inspect %s: %w: %s", ollamaName, err, strings.TrimSpace(string(out)))
	}
	image, mounts, _ := strings.Cut(strings.TrimSpace(string(out)), "|")
	if !isOllamaImage(image) {
		return Foreign, "", nil
	}
	models := false
	for _, m := range strings.Split(mounts, ";") {
		parts := strings.SplitN(m, "=", 3) // type, name, destination
		if len(parts) == 3 && parts[2] == ollamaDir {
			models = true
			if parts[0] == "volume" && parts[1] == ollamaName {
				volume = ollamaName
			}
		}
	}
	if !models {
		return Foreign, "", nil
	}
	return Ours, volume, nil
}

// isOllamaImage reports whether ref is the ollama/ollama repository, without
// its tag or digest, with or without a registry in front.
func isOllamaImage(ref string) bool {
	ref, _, _ = strings.Cut(ref, "@")
	if i := strings.LastIndex(ref, ":"); i > strings.LastIndex(ref, "/") {
		ref = ref[:i]
	}
	return ref == "ollama/ollama" || strings.HasSuffix(ref, "/ollama/ollama")
}

// ollamaListed asks ps whether a container of exactly that name exists.
func ollamaListed(ctx context.Context) (bool, error) {
	names, err := exec.CommandContext(ctx, "docker", "ps", "-a", "--filter", "name=^"+ollamaName+"$", "--format", "{{.Names}}").Output()
	return strings.TrimSpace(string(names)) == ollamaName, err
}

// DropOllamaContainer removes the container with docker rm -f. A refused
// removal counts as done when the container is gone by then.
func DropOllamaContainer(ctx context.Context) error {
	out, err := exec.CommandContext(ctx, "docker", "rm", "-f", ollamaName).CombinedOutput()
	if err == nil {
		return nil
	}
	if listed, lerr := ollamaListed(ctx); lerr == nil && !listed {
		return nil
	}
	return fmt.Errorf("docker rm -f %s: %w: %s", ollamaName, err, strings.TrimSpace(string(out)))
}

// RemoveOllamaVolume removes the volume ragcode-ollama. A refused removal
// counts as done when the volume is no longer listed (it was gone already);
// otherwise the error carries Docker's answer.
func RemoveOllamaVolume(ctx context.Context) error {
	out, err := exec.CommandContext(ctx, "docker", "volume", "rm", ollamaName).CombinedOutput()
	if err == nil {
		return nil
	}
	if names, lerr := exec.CommandContext(ctx, "docker", "volume", "ls", "-q", "--filter", "name=^"+ollamaName+"$").Output(); lerr == nil && strings.TrimSpace(string(names)) == "" {
		return nil
	}
	return fmt.Errorf("volume %s not removed: %w: %s", ollamaName, err, strings.TrimSpace(string(out)))
}

// RemoveOllama removes the ragcode-ollama container and volume that RagCode
// v2 created before it ran the embedding model itself: the container
// restarts forever (--restart always) and would keep its memory. Only a
// container FindOllama recognises is removed, and only the volume that it
// mounted; a Foreign container and a volume without a container are left.
// With a nil error the outcome is final. When the volume could not be removed
// the outcome is Removed together with the error; when the container could
// not, it is Ours. The error is ErrNoDocker when Docker could not be asked.
func RemoveOllama(ctx context.Context) (Outcome, error) {
	c, volume, err := FindOllama(ctx)
	if c != Ours || err != nil {
		return c, err
	}
	if err := DropOllamaContainer(ctx); err != nil {
		return Ours, err
	}
	if volume != "" {
		if err := RemoveOllamaVolume(ctx); err != nil {
			return Removed, err
		}
	}
	return Removed, nil
}

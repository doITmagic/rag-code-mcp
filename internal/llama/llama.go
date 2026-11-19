// Package llama keeps the embedding runtime on disk: llama.cpp's llama-server
// and the GGUF embedding model. Both are downloaded once into ~/.ragcode from
// pinned URLs and checked against pinned SHA-256 sums before they are used.
package llama

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/doITmagic/rag-code-mcp/v2/index"
	"github.com/doITmagic/rag-code-mcp/v2/internal/config"
)

// Tag is the pinned llama.cpp release: the binaries of llama.cpp v1.0.0.
const Tag = "b12359"

// ModelName is the built-in embedding model, as status output and the index name it.
const ModelName = "embeddinggemma-2-Q8_0"

// The built-in model: Google's EmbeddingGemma 2 (271M parameters), 8-bit
// weights, at a fixed revision of its official GGUF repository.
const (
	modelFile = ModelName + ".gguf"
	modelSHA  = "2188ac1deca4b77dffefd603c2776a9d76d9d74ec01841392982ebb840b09135"
	modelSize = 309855456
)

// Download sources; tests point them at a local server.
var (
	runtimeBase = "https://github.com/ggml-org/llama.cpp/releases/download/" + Tag + "/"
	modelURL    = "https://huggingface.co/ggml-org/embeddinggemma-2-GGUF/resolve/6ea3e3a309d12a4779cfda98b8c19800d2d5da71/" + modelFile
	client      = &http.Client{} // no clock: a slow line still finishes; the context given to Ensure ends it
)

type asset struct{ name, sha string }

// Variant names of the runtime build. Auto is Vulkan on a machine with an
// NVIDIA driver and a Vulkan loader, CPU elsewhere.
const (
	Auto   = "auto"
	CPU    = "cpu"
	Vulkan = "vulkan"
)

// runtimes holds the release archives per variant and GOOS/GOARCH. The macOS
// build carries Metal, so it has no separate GPU variant.
var runtimes = map[string]map[string]asset{
	CPU: {
		"linux/amd64":   {"llama-" + Tag + "-bin-ubuntu-x64.tar.gz", "0bc1f091e8916102b6d8f2cf944233a620e90a0f856c72d0ce41150c65c2e72e"},
		"linux/arm64":   {"llama-" + Tag + "-bin-ubuntu-arm64.tar.gz", "dc7aa17a67884619b7223870aca9fd6addbc01717713b04a714add1c31137359"},
		"darwin/arm64":  {"llama-" + Tag + "-bin-macos-arm64.tar.gz", "82b00abc740160e1b9980e6cc79c117345fa34af058c80e6c0a69aa8ddccde75"},
		"darwin/amd64":  {"llama-" + Tag + "-bin-macos-x64.tar.gz", "25320d529077ef5a3d16e7b5da26438f221d88c8c0930a32b0c79457f5eede56"},
		"windows/amd64": {"llama-" + Tag + "-bin-win-cpu-x64.zip", "837c1655e87df3b66989f4be33a792a1399c785bf259038dc75c900096683e69"},
		"windows/arm64": {"llama-" + Tag + "-bin-win-cpu-arm64.zip", "d27564c754478d2832789dfcebfc5ace018e36a224c41d810b7cbc1ba8243ec0"},
	},
	Vulkan: {
		"linux/amd64":   {"llama-" + Tag + "-bin-ubuntu-vulkan-x64.tar.gz", "19b60f00395a3f4954e46a5037695bc1d843404fc9d3124f82a612ecd8fbe9f0"},
		"linux/arm64":   {"llama-" + Tag + "-bin-ubuntu-vulkan-arm64.tar.gz", "3f8ae812a58ac92e5a596ca9fbc988f52854d147c5a7fe1264dddc90c1d085b4"},
		"windows/amd64": {"llama-" + Tag + "-bin-win-vulkan-x64.zip", "e8d18c6e4210cca87e7307c2ea78363ac8d158e4e1397358ec96ab70b53a0d5e"},
		"windows/arm64": {"llama-" + Tag + "-bin-win-vulkan-arm64.zip", "62cd50089cffdd825524a91c5a8819041eb75d8e2beacd6eca68194dba50574f"},
	},
}

// Options says what to provide. The zero value is the pinned build for this
// machine and the built-in model under Home.
type Options struct {
	Home    string // the global RagCode directory (config.HomeDir())
	Variant string // Auto (default), CPU or Vulkan
	// Server and Model, when set, are files the user provides: nothing is
	// downloaded for that part.
	Server string
	Model  string
	// Progress receives one short line per step and per few percent of a
	// download; nil is silent.
	Progress func(string)
}

// FromConfig is the runtime a configuration asks for.
func FromConfig(cfg config.Config, progress func(string)) Options {
	return Options{Home: config.HomeDir(), Variant: cfg.EmbedRuntime, Server: cfg.LlamaServer, Model: cfg.EmbedModel, Progress: progress}
}

// ServerPaths lists where a downloaded llama-server may be under home, one
// per variant, for stopping left-over processes.
func ServerPaths(home string) []string {
	return []string{Options{Home: home, Variant: CPU}.ServerPath(), Options{Home: home, Variant: Vulkan}.ServerPath()}
}

func (o Options) variant() string {
	want := o.Variant == Vulkan || (o.Variant == "" || o.Variant == Auto) && nvidiaVulkan()
	if _, ok := runtimes[Vulkan][runtime.GOOS+"/"+runtime.GOARCH]; ok && want {
		return Vulkan
	}
	return CPU
}

// nvidiaVulkan reports an NVIDIA driver together with a Vulkan loader: the
// one GPU setup the Vulkan build is picked for without being asked. Other
// GPUs work with embed_runtime: vulkan.
var nvidiaVulkan = sync.OnceValue(func() bool {
	any := func(paths ...string) bool {
		for _, p := range paths {
			if _, err := os.Stat(p); err == nil {
				return true
			}
		}
		return false
	}
	switch runtime.GOOS {
	case "linux":
		libs := []string{"/usr/lib/x86_64-linux-gnu", "/usr/lib/aarch64-linux-gnu", "/usr/lib64", "/usr/lib", "/lib64"}
		for i, d := range libs {
			libs[i] = filepath.Join(d, "libvulkan.so.1")
		}
		return any("/proc/driver/nvidia/version") && any(libs...)
	case "windows":
		sys := filepath.Join(os.Getenv("SystemRoot"), "System32")
		return any(filepath.Join(sys, "nvapi64.dll")) && any(filepath.Join(sys, "vulkan-1.dll"))
	}
	return false
})

// RuntimeName names the runtime for status output: "llama.cpp b12359 (cpu)",
// or the path of a user-provided server.
func (o Options) RuntimeName() string {
	if o.Server != "" {
		return o.Server
	}
	return "llama.cpp " + Tag + " (" + o.variant() + ")"
}

// ModelLabel is the model name the index stores: the built-in name (one file,
// pinned by modelSHA: a new built-in model needs a new ModelName), or the
// file name of a user-provided model without ".gguf", followed by "@" and a
// fingerprint of the file. Vectors of two models are not comparable even at
// the same size, so another file under the same name must count as another
// model; without the fingerprint the index would keep and mix both.
func (o Options) ModelLabel() string {
	if o.Model == "" {
		return ModelName
	}
	name := strings.TrimSuffix(filepath.Base(o.Model), ".gguf")
	if fp := fingerprint(o.Model); fp != "" {
		return name + "@" + fp
	}
	return name // unreadable: Ensure reports it
}

// fingerprint identifies a model file by its size and its first and last
// MiB (the header with the model's metadata, and weights): reading all of a
// file of hundreds of MB at every start would be slow. "" when unreadable.
func fingerprint(path string) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil || !fi.Mode().IsRegular() {
		return ""
	}
	const part = 1 << 20
	h := sha256.New()
	fmt.Fprintf(h, "%d\n", fi.Size())
	if _, err := io.CopyN(h, f, part); err != nil && err != io.EOF {
		return ""
	}
	if fi.Size() > 2*part {
		if _, err := f.Seek(-part, io.SeekEnd); err != nil {
			return ""
		}
	}
	if _, err := io.Copy(h, f); err != nil {
		return ""
	}
	return hex.EncodeToString(h.Sum(nil))[:12]
}

// ServerPath is where llama-server is, or will be after Ensure.
func (o Options) ServerPath() string {
	if o.Server != "" {
		return o.Server
	}
	name := "llama-server"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	return filepath.Join(o.Home, "llama", Tag+"-"+o.variant(), name)
}

// ModelPath is where the model file is, or will be after Ensure.
func (o Options) ModelPath() string {
	if o.Model != "" {
		return o.Model
	}
	return filepath.Join(o.Home, "models", modelFile)
}

// Installed reports whether both files are on disk.
func (o Options) Installed() bool {
	return isFile(o.ServerPath()) && isFile(o.ModelPath())
}

func isFile(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && fi.Mode().IsRegular()
}

// Ensure returns the paths of llama-server and of the model, downloading
// what is missing. Several processes may call it at once: one downloads, the
// others wait for it. A download that fails or does not match its checksum
// leaves nothing behind.
func (o Options) Ensure(ctx context.Context) (server, model string, err error) {
	server, model = o.ServerPath(), o.ModelPath()
	if o.Installed() {
		return server, model, nil
	}
	if o.Server != "" && !isFile(server) {
		return "", "", fmt.Errorf("llama: llama_server %s is not a file", server)
	}
	if o.Model != "" && !isFile(model) {
		return "", "", fmt.Errorf("llama: embed_model %s is not a file", model)
	}
	lockPath := filepath.Join(o.Home, "cache", "llama.lock")
	if err := os.MkdirAll(filepath.Dir(lockPath), 0o755); err != nil {
		return "", "", fmt.Errorf("llama: %w", err)
	}
	var unlock func()
	for said := false; ; {
		unlock, err = index.LockFile(lockPath)
		if err == nil {
			break
		}
		if !errors.Is(err, index.ErrLocked) {
			return "", "", fmt.Errorf("llama: %w", err)
		}
		if !said {
			o.say("another RagCode process is downloading the embedding runtime; waiting for it")
			said = true
		}
		select {
		case <-ctx.Done():
			return "", "", ctx.Err()
		case <-time.After(500 * time.Millisecond):
		}
	}
	defer unlock()
	// What a killed download or unpack left behind, in the directories
	// RagCode owns; the lock says no one is writing them.
	var stale []string
	if o.Model == "" {
		m, _ := filepath.Glob(filepath.Join(o.Home, "models", ".download-*"))
		stale = append(stale, m...)
	}
	if o.Server == "" {
		m, _ := filepath.Glob(filepath.Join(o.Home, "llama", ".*-*"))
		stale = append(stale, m...)
	}
	for _, p := range stale {
		_ = os.RemoveAll(p)
	}

	if !isFile(server) {
		if err := o.fetchRuntime(ctx, filepath.Dir(server)); err != nil {
			return "", "", err
		}
	}
	if !isFile(model) {
		if err := os.MkdirAll(filepath.Dir(model), 0o755); err != nil {
			return "", "", fmt.Errorf("llama: %w", err)
		}
		tmp, err := o.download(ctx, modelURL, modelSHA, modelSize, filepath.Dir(model), "embedding model "+ModelName)
		if err != nil {
			return "", "", err
		}
		if err := os.Rename(tmp, model); err != nil {
			os.Remove(tmp)
			return "", "", fmt.Errorf("llama: %w", err)
		}
	}
	return server, model, nil
}

func (o Options) say(s string) {
	if o.Progress != nil {
		o.Progress(s)
	}
}

// fetchRuntime downloads the release archive of this platform and unpacks
// llama-server with its libraries into dir. dir appears only when complete.
func (o Options) fetchRuntime(ctx context.Context, dir string) error {
	a, ok := runtimes[o.variant()][runtime.GOOS+"/"+runtime.GOARCH]
	if !ok {
		return fmt.Errorf("llama: no llama.cpp build for %s/%s: set llama_server in config.yaml to a llama-server binary", runtime.GOOS, runtime.GOARCH)
	}
	parent := filepath.Dir(dir)
	if err := os.MkdirAll(parent, 0o755); err != nil {
		return fmt.Errorf("llama: %w", err)
	}
	archive, err := o.download(ctx, runtimeBase+a.name, a.sha, 0, parent, o.RuntimeName())
	if err != nil {
		return err
	}
	defer os.Remove(archive)
	tmp, err := os.MkdirTemp(parent, "."+filepath.Base(dir)+"-*")
	if err != nil {
		return fmt.Errorf("llama: %w", err)
	}
	defer os.RemoveAll(tmp) // gone already after the rename
	if strings.HasSuffix(a.name, ".zip") {
		err = unzip(archive, tmp)
	} else {
		err = untar(archive, tmp)
	}
	if err != nil {
		return fmt.Errorf("llama: unpack %s: %w", a.name, err)
	}
	if !isFile(filepath.Join(tmp, filepath.Base(o.ServerPath()))) {
		return fmt.Errorf("llama: %s holds no %s", a.name, filepath.Base(o.ServerPath()))
	}
	_ = os.RemoveAll(dir) // a half-removed earlier copy
	if err := os.Rename(tmp, dir); err != nil {
		return fmt.Errorf("llama: %w", err)
	}
	return nil
}

// download saves url to a temporary file in dir and returns its path once the
// SHA-256 matches. size, when known, is checked too and gives the percent.
func (o Options) download(ctx context.Context, url, sha string, size int64, dir, what string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", fmt.Errorf("llama: %w", err)
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("llama: download %s: %w", what, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("llama: download %s: %s returned %s", what, url, resp.Status)
	}
	if size == 0 {
		size = resp.ContentLength
	}
	f, err := os.CreateTemp(dir, ".download-*")
	if err != nil {
		return "", fmt.Errorf("llama: %w", err)
	}
	fail := func(err error) (string, error) {
		f.Close()
		os.Remove(f.Name())
		return "", err
	}
	o.say(fmt.Sprintf("downloading %s (%d MB)", what, size>>20))
	h := sha256.New()
	var done int64
	lastPct := 0
	buf := make([]byte, 1<<20)
	for {
		n, rerr := resp.Body.Read(buf)
		if n > 0 {
			if _, err := f.Write(buf[:n]); err != nil {
				return fail(fmt.Errorf("llama: %w", err))
			}
			h.Write(buf[:n])
			done += int64(n)
			if size > 0 {
				if pct := int(done * 100 / size); pct >= lastPct+10 && pct < 100 {
					lastPct = pct - pct%10
					o.say(fmt.Sprintf("downloading %s: %d%%", what, lastPct))
				}
			}
		}
		if rerr == io.EOF {
			break
		}
		if rerr != nil {
			return fail(fmt.Errorf("llama: download %s: %w", what, rerr))
		}
	}
	if got := hex.EncodeToString(h.Sum(nil)); got != sha {
		return fail(fmt.Errorf("llama: %s does not match its pinned checksum (got %s, %d bytes): not used", what, got, done))
	}
	if err := f.Close(); err != nil {
		os.Remove(f.Name())
		return "", fmt.Errorf("llama: %w", err)
	}
	return f.Name(), nil
}

// wanted reports whether an archive entry is part of the server: the binary,
// its shared libraries and the licence. The other tools of the release stay out.
func wanted(base string) bool {
	switch {
	case base == "llama-server", base == "llama-server.exe":
		return true
	case strings.HasPrefix(base, "LICENSE"):
		return true
	case strings.HasSuffix(base, ".dll"), strings.HasSuffix(base, ".dylib"):
		return true
	case strings.HasPrefix(base, "lib") && strings.Contains(base, ".so"):
		return true
	}
	return false
}

const maxFile = 1 << 30 // no file of a release comes near it

// untar unpacks the wanted regular files and symlinks of a .tar.gz flat into
// dir: entry names lose their directory, and a link may only name a sibling.
func untar(archive, dir string) error {
	f, err := os.Open(archive)
	if err != nil {
		return err
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return err
	}
	tr := tar.NewReader(gz)
	for {
		h, err := tr.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		base := path.Base(h.Name)
		if !wanted(base) {
			continue
		}
		dst := filepath.Join(dir, base)
		switch h.Typeflag {
		case tar.TypeReg:
			if err := writeFile(dst, tr, h.FileInfo().Mode().Perm()|0o600); err != nil {
				return err
			}
		case tar.TypeSymlink:
			if h.Linkname != path.Base(h.Linkname) {
				return fmt.Errorf("link %s points outside the archive directory", h.Name)
			}
			if err := os.Symlink(h.Linkname, dst); err != nil {
				return err
			}
		}
	}
}

func unzip(archive, dir string) error {
	zr, err := zip.OpenReader(archive)
	if err != nil {
		return err
	}
	defer zr.Close()
	for _, zf := range zr.File {
		base := path.Base(strings.ReplaceAll(zf.Name, `\`, "/"))
		if zf.FileInfo().IsDir() || !zf.Mode().IsRegular() || !wanted(base) {
			continue
		}
		rc, err := zf.Open()
		if err != nil {
			return err
		}
		err = writeFile(filepath.Join(dir, base), rc, 0o755)
		rc.Close()
		if err != nil {
			return err
		}
	}
	return nil
}

func writeFile(dst string, r io.Reader, mode os.FileMode) error {
	f, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
	if err != nil {
		return err
	}
	n, err := io.Copy(f, io.LimitReader(r, maxFile+1))
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err == nil && n > maxFile {
		err = fmt.Errorf("%s is larger than %d bytes", filepath.Base(dst), maxFile)
	}
	return err
}

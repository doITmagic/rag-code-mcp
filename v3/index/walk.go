package index

import (
	"bytes"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/doITmagic/rag-code-mcp/v3/parser"
)

const (
	// Files over 256 KiB are never indexed: tree-sitter needs up to ~500x a
	// file's size while it parses (1 GB for a 3.6 MB JS bundle), and files
	// that big are bundles or generated code.
	maxFileSize = 256 << 10
	parseBudget = maxFileSize // source bytes parsed at once, whatever the CPU count
	// Two workers: slower on many cores, but a parser keeps the buffers of
	// the largest file it parsed, and a laptop needs its CPU for Ollama.
	parseWorkers = 2
	headSize     = 8 << 10 // bytes inspected for the binary (NUL) check
)

// batchBytes is the source size parsed, embedded and upserted per batch.
var batchBytes int64 = 4 << 20

var lockfiles = map[string]bool{
	"package-lock.json": true, "yarn.lock": true, "pnpm-lock.yaml": true,
	"composer.lock": true, "go.sum": true, "cargo.lock": true, "poetry.lock": true,
}

// Walk lists the indexable files under root, sorted lexically. Hidden
// directories, directories whose name is in exclude and linked git worktrees
// are pruned at any depth; files go through Skip.
func Walk(root string, exclude []string) ([]string, error) {
	ex := set(exclude)
	var out []string
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			if path == root {
				return err
			}
			return nil // unreadable entry: skip it, keep walking
		}
		if d.IsDir() {
			if path != root && (strings.HasPrefix(d.Name(), ".") || ex[d.Name()] || IsLinkedWorktree(path)) {
				return fs.SkipDir
			}
			return nil
		}
		if parser.ForFile(path) == nil {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return nil
		}
		var head []byte
		if info.Mode().IsRegular() { // never open a fifo or device
			head = readHead(path)
		}
		if Skip(path, info, head) == "" {
			out = append(out, path)
		}
		return nil
	})
	return out, err
}

// IsLinkedWorktree reports whether dir is a linked git worktree: its .git is
// a file naming a gitdir that has a commondir (a submodule's has none).
func IsLinkedWorktree(dir string) bool {
	// The repo controls .git: only a small regular file (one pointing at
	// /dev/zero or a FIFO would exhaust memory or hang).
	p := filepath.Join(dir, ".git")
	if info, err := os.Stat(p); err != nil || !info.Mode().IsRegular() || info.Size() > 4<<10 {
		return false
	}
	data, err := os.ReadFile(p)
	if err != nil {
		return false
	}
	gitdir, ok := strings.CutPrefix(strings.TrimSpace(string(data)), "gitdir:")
	if !ok {
		return false
	}
	if gitdir = strings.TrimSpace(gitdir); !filepath.IsAbs(gitdir) {
		gitdir = filepath.Join(dir, gitdir)
	}
	_, err = os.Stat(filepath.Join(gitdir, "commondir"))
	return err == nil
}

func set(names []string) map[string]bool {
	m := make(map[string]bool, len(names))
	for _, n := range names {
		m[n] = true
	}
	return m
}

// Skip reports why a file is left out of the index, or "" when it is
// indexed. head is the first bytes of the file (up to headSize), nil when the
// caller has none.
func Skip(path string, info fs.FileInfo, head []byte) string {
	if !info.Mode().IsRegular() {
		return "not a regular file"
	}
	if info.Size() > maxFileSize {
		return "over 256 KiB"
	}
	base := strings.ToLower(filepath.Base(path))
	switch {
	case strings.HasSuffix(base, ".min.js"), strings.HasSuffix(base, ".min.css"), strings.HasSuffix(base, ".map"):
		return "minified"
	case lockfiles[base]:
		return "lockfile"
	case Secret(path):
		return "secret"
	}
	if parser.ForFile(path) == nil {
		return "no analyzer"
	}
	if bytes.IndexByte(head, 0) >= 0 {
		return "binary"
	}
	return ""
}

// Secret reports a file that holds credentials by its name. A secret inside
// a file with an ordinary name (config.toml) is not caught.
func Secret(path string) bool {
	base := strings.ToLower(filepath.Base(path))
	ext := filepath.Ext(base)
	switch {
	case base == ".env", strings.HasPrefix(base, ".env."), strings.HasPrefix(base, "id_rsa"),
		base == ".npmrc", base == ".pypirc", base == ".netrc":
		return true
	case ext == ".pem", ext == ".key", ext == ".p12", ext == ".pfx":
		return true
	}
	// credentials.json, secrets.yaml: data files only, credentials.go is code.
	switch strings.TrimSuffix(base, ext) {
	case "credentials", "secrets":
		switch ext {
		case "", ".json", ".yaml", ".yml", ".toml", ".ini", ".xml", ".properties", ".conf", ".cfg", ".txt":
			return true
		}
	}
	return false
}

func readHead(path string) []byte {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()
	buf := make([]byte, headSize)
	n, _ := io.ReadFull(f, buf)
	return buf[:n]
}

package index

import (
	"bytes"
	"errors"
	"io"
	"io/fs"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/doITmagic/rag-code-mcp/v2/parser"
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
// directories, directories whose name is in exclude, linked git worktrees and
// RagCode's own home (OwnHome) are pruned at any depth; files go through Skip.
func Walk(root string, exclude []string) ([]string, error) {
	return walk(root, exclude, nil)
}

// walk is Walk; a file known reports (the state has it with this size and
// modification time) was text on the run that indexed it and is not opened
// for the binary check again.
func walk(root string, exclude []string, known func(path string, info fs.FileInfo) bool) ([]string, error) {
	ex := set(exclude)
	text := map[string]bool{}
	home := OwnHome()
	var out []string
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			if path == root {
				return err
			}
			// A directory deleted mid-walk is not unreadable: its files leave the index silently.
			if d != nil && d.IsDir() && !errors.Is(err, fs.ErrNotExist) {
				// Its files leave the index on this run; say so, the tools cannot.
				slog.Warn("index: unreadable directory skipped, its files are not indexed", "dir", path, "err", err)
			}
			return nil // unreadable entry: skip it, keep walking
		}
		if d.IsDir() {
			if path != root && (strings.HasPrefix(d.Name(), ".") || ex[d.Name()] || IsLinkedWorktree(path) || InOwnHome(path, home)) {
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
		if Skip(path, info, nil) == "" { // regular files only: a fifo or device is never opened below
			out = append(out, path)
			if known != nil && known(path, info) {
				text[path] = true
			}
		}
		return nil
	})
	if ign := GitIgnored(root, out); len(ign) > 0 {
		out = slices.DeleteFunc(out, func(f string) bool { return ign[f] })
	}
	// The binary check opens the file, which on Windows costs an antivirus
	// scan (about 10 ms a file not opened since boot): only files git keeps
	// are opened, never those of ignored directories or those known as text.
	out = slices.DeleteFunc(out, func(f string) bool { return !text[f] && bytes.IndexByte(readHead(f), 0) >= 0 })
	return out, err
}

// GitIgnored returns the files among files that git ignores, by every
// .gitignore and the user's excludes; tracked files are never ignored. A
// directory with its own .git is a separate repository and follows its own
// rules, not its parent's, unless the repository around it ignores that
// directory (a legacy app kept as a nested repo under an ignored folder):
// then every file in it is ignored. Files outside any git work tree are
// never ignored.
func GitIgnored(root string, files []string) map[string]bool {
	repoOf := map[string]string{}
	var repo func(dir string) string
	repo = func(dir string) string {
		if r, ok := repoOf[dir]; ok {
			return r
		}
		r := root
		if len(dir) > len(root) {
			if _, err := os.Lstat(filepath.Join(dir, ".git")); err == nil {
				r = dir
			} else {
				r = repo(filepath.Dir(dir))
			}
		}
		repoOf[dir] = r
		return r
	}
	groups := map[string][]string{}
	for _, f := range files {
		r := repo(filepath.Dir(f))
		groups[r] = append(groups[r], f)
	}
	// dropped: the nested repository r lies in a directory an enclosing one ignores.
	drop := map[string]bool{}
	var dropped func(r string) bool
	dropped = func(r string) bool {
		if r == root {
			return false
		}
		d, ok := drop[r]
		if !ok {
			parent := repo(filepath.Dir(r))
			d = dropped(parent) || exec.Command("git", "-C", parent, "check-ignore", "-q", r).Run() == nil
			drop[r] = d
		}
		return d
	}
	var ign map[string]bool
	for r, fs := range groups {
		if dropped(r) {
			if ign == nil {
				ign = map[string]bool{}
			}
			for _, f := range fs {
				ign[f] = true
			}
			continue
		}
		var in bytes.Buffer
		for _, f := range fs {
			in.WriteString(f)
			in.WriteByte(0)
		}
		cmd := exec.Command("git", "-C", r, "check-ignore", "--stdin", "-z")
		cmd.Stdin = &in
		out, _ := cmd.Output() // exit 1: nothing ignored; 128: not a repo or no git
		for _, p := range bytes.Split(out, []byte{0}) {
			if len(p) > 0 {
				if ign == nil {
					ign = map[string]bool{}
				}
				ign[string(p)] = true
			}
		}
	}
	return ign
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

// LinkedWorktreeOf returns the linked git worktree below root that contains
// path, or "". known caches IsLinkedWorktree per directory; nil caches nothing.
func LinkedWorktreeOf(root, path string, known map[string]bool) string {
	rel, err := filepath.Rel(root, filepath.Dir(path))
	if err != nil || rel == "." || strings.HasPrefix(rel, "..") {
		return ""
	}
	dir := root
	for _, part := range strings.Split(rel, string(filepath.Separator)) {
		dir = filepath.Join(dir, part)
		is, ok := known[dir]
		if !ok {
			is = IsLinkedWorktree(dir)
			if known != nil {
				known[dir] = is
			}
		}
		if is {
			return dir
		}
	}
	return ""
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
	if !utf8.ValidString(path) {
		return "path is not valid UTF-8" // the store keeps paths as JSON text, which would alter it
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

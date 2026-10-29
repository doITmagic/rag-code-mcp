package indexer

import (
	"bytes"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/doITmagic/rag-code-mcp/internal/generatedfile"
	"github.com/doITmagic/rag-code-mcp/pkg/parser"
)

// maxAvgLineLen is the average-line-length threshold above which a file
// is considered minified.  Normal hand-written source rarely exceeds
// 120 characters per line; minified bundles routinely exceed 1 000.
const maxAvgLineLen = 500

// sampleBytes caps the amount of data we read for the density heuristic.
const sampleBytes = 256 << 10 // 256 KiB

// minifiedSuffixes are well-known filename patterns that unambiguously
// mark a file as a minified/bundled asset.
var minifiedSuffixes = []string{
	".min.js", ".min.css", ".min.mjs",
	".bundle.js", ".bundle.css", ".bundle.mjs",
	".packed.js", ".chunk.js", ".chunk.css",
	"-min.js", "-min.css",
}

// ShouldSkipFile reports whether indexing leaves out path although an analyzer
// handles it: a special, oversized, secret, generated or minified file.
func ShouldSkipFile(path string) bool {
	return parser.SkipFile(path) || isLowValueConfigOrKeyMaterial(path) || isIgnoredEnvFile(path) ||
		isGeneratedLockfile(path) || isGeneratedIDERule(path) || isMinifiedOrVendored(path)
}

// isIgnoredEnvFile reports whether path is an env file (.env, .env.*, *.env)
// Git ignores. A committed .env.example documents settings; an ignored .env
// or prod.env holds the real secrets and must not become searchable.
func isIgnoredEnvFile(path string) bool {
	base := strings.ToLower(filepath.Base(path))
	if base != ".env" && !strings.HasPrefix(base, ".env.") && filepath.Ext(base) != ".env" {
		return false
	}
	cmd := exec.Command("git", "check-ignore", "-q", "--", filepath.Base(path))
	cmd.Dir = filepath.Dir(path)
	err := cmd.Run()
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) && exitErr.ExitCode() == 1 {
		return false
	}
	// Exit 0 means ignored. Any other failure (git not on PATH, a repo that
	// trips safe.directory) leaves the status unknown: fail closed inside a
	// Git repo, while outside one git exits 128 and the file is kept.
	return err == nil || insideGitRepo(cmd.Dir)
}

func insideGitRepo(dir string) bool {
	for {
		if _, err := os.Lstat(filepath.Join(dir, ".git")); err == nil {
			return true
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return false
		}
		dir = parent
	}
}

func isGeneratedIDERule(path string) bool {
	slashPath := filepath.ToSlash(path)
	known := filepath.Base(path) == "CLAUDE.md" || strings.HasSuffix(slashPath, "/.cursor/rules/ragcode.mdc") ||
		strings.HasSuffix(slashPath, "/.windsurf/rules/ragcode.md") || strings.HasSuffix(slashPath, "/.clinerules/ragcode.md") ||
		strings.HasSuffix(slashPath, "/.roo/rules/ragcode.md")
	if !known {
		return false
	}
	data, err := os.ReadFile(path)
	return err == nil && generatedfile.Valid(string(data))
}

func isGeneratedLockfile(path string) bool {
	switch strings.ToLower(filepath.Base(path)) {
	case "package-lock.json", "npm-shrinkwrap.json", "yarn.lock", "pnpm-lock.yaml",
		"go.sum", "composer.lock", "poetry.lock", "pipfile.lock", "cargo.lock":
		return true
	}
	return false
}

func isLowValueConfigOrKeyMaterial(path string) bool {
	base := strings.ToLower(filepath.Base(path))
	switch base {
	case ".npmrc", ".pypirc", "id_rsa", "id_dsa", "id_ecdsa", "id_ed25519":
		return true
	}
	switch filepath.Ext(base) {
	case ".key", ".pem", ".p12", ".pfx":
		return true
	}
	return false
}

// isMinifiedOrVendored reports whether path points to machine-generated,
// bundled, or minified code that should be skipped before tree-sitter parsing.
//
// Directory-level exclusions (vendor/, node_modules/, etc.) are already
// handled by the WalkDir filter in IndexWorkspace, so this function only
// covers two remaining cases:
//  1. Filename suffix  (.min.js, .bundle.css, …) — no I/O.
//  2. Content density  — reads ≤256 KiB, counts newlines.
//
// Godot scenes (.tscn) skip the density check: embedded meshes and textures
// are serialized as very long lines, but the analyzer parses only section
// headers and script lines, so the node tree is still cheap to index.
func isMinifiedOrVendored(path string) bool {
	if matchesMinifiedName(path) {
		return true
	}
	if strings.EqualFold(filepath.Ext(path), ".tscn") {
		return false
	}
	return exceedsLineDensity(path)
}

// matchesMinifiedName checks well-known minified filename patterns.
func matchesMinifiedName(path string) bool {
	base := strings.ToLower(filepath.Base(path))
	for _, s := range minifiedSuffixes {
		if strings.HasSuffix(base, s) {
			return true
		}
	}
	return false
}

// exceedsLineDensity reads the first sampleBytes of path and returns
// true when the average line length exceeds maxAvgLineLen.
// Structured data files (.json, .yaml, .yml) are only considered minified when
// a single line exceeds 4 KiB, so small compact JSON configs (e.g. 600B on one line)
// are not skipped.
func exceedsLineDensity(path string) bool {
	ext := strings.ToLower(filepath.Ext(path))
	isDataFile := ext == ".json" || ext == ".yaml" || ext == ".yml"

	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()

	buf := make([]byte, sampleBytes)
	n, err := io.ReadFull(f, buf)
	if n == 0 && err != nil {
		return false
	}

	if isDataFile && n < 4096 {
		return false
	}

	lines := bytes.Count(buf[:n], []byte{'\n'})
	if lines == 0 {
		lines = 1
	}
	return n/lines > maxAvgLineLen
}

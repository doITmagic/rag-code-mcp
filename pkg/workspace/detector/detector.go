package detector

import (
	context "context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/doITmagic/rag-code-mcp/pkg/workspace/contract"
)

// Options configures the detector behavior.
type Options struct {
	Markers          []string
	AllowedRoots     []string
	ExcludePatterns  []string
	MaxDepth         int
	DisableUpward    bool
	MetadataFileName string
}

// DefaultOptions provides sane defaults aligned with TASKS.md requirements.
func DefaultOptions() Options {
	return Options{
		Markers: []string{
			".git",
			"go.mod",
			"package.json",
			"Cargo.toml",
			"pyproject.toml",
			"setup.py",
			"requirements.txt",
			"composer.json",
			"pom.xml",
			"build.gradle",
			"Gemfile",
			"Package.swift",
			"tsconfig.json",
			"tailwind.config.js",
			"tailwind.config.ts",
			"vite.config.js",
			"vite.config.ts",
			"next.config.js",
			"deno.json",
			"Dockerfile",
			"docker-compose.yml",
			"mix.exs",
			"artisan",
			".ragcode",
			".agent",
			".idea",
			".vscode",
			".vs",
			".cursor",
			".windsurf",
			"AGENTS.md",
			"CLAUDE.md",
		},
		MaxDepth:         10,
		MetadataFileName: filepath.Join(".ragcode", "root"),
	}
}

// Detector performs marker-based root detection with security validation.
type Detector struct {
	opts Options
}

// New creates a new detector with the supplied options.
func New(opts Options) *Detector {
	defaults := DefaultOptions()
	if len(opts.Markers) == 0 {
		opts.Markers = defaults.Markers
	}
	if opts.MaxDepth == 0 {
		opts.MaxDepth = defaults.MaxDepth
	}
	if opts.MetadataFileName == "" {
		opts.MetadataFileName = defaults.MetadataFileName
	}
	return &Detector{opts: opts}
}

// DetectFromFilePath implements resolver.Detector.
func (d *Detector) DetectFromFilePath(ctx context.Context, filePath string) (*contract.WorkspaceCandidate, *contract.ResolveWorkspaceError) {
	path := strings.TrimSpace(filePath)
	if path == "" {
		return nil, &contract.ResolveWorkspaceError{
			Code:    contract.ErrorInvalidPath,
			Message: "file_path is empty",
			Reason:  contract.ReasonInvalidPath,
		}
	}

	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, wrapPathErr("resolve absolute path", err)
	}
	if resolved, err := filepath.EvalSymlinks(abs); err == nil {
		abs = resolved
	}

	startDir := abs
	info, err := os.Stat(abs)
	if err != nil {
		// File does not exist — try the parent directory as fallback.
		// This handles the common case where an AI agent passes a non-existent
		// file path (e.g. "main.go") but the project directory is valid.
		parentDir := filepath.Dir(abs)
		if _, parentErr := os.Stat(parentDir); parentErr != nil {
			return nil, &contract.ResolveWorkspaceError{
				Code:    contract.ErrorInvalidPath,
				Message: fmt.Sprintf("file_path %q does not exist and parent directory %q is also invalid. The file_path parameter is only used for workspace detection — pass any existing file from the target project, or omit file_path entirely for auto-discovery", abs, parentDir),
				Reason:  contract.ReasonInvalidPath,
			}
		}
		startDir = parentDir
	} else if !info.IsDir() {
		startDir = filepath.Dir(abs)
	}

	if err := d.ensureAllowed(startDir); err != nil {
		return nil, err
	}
	if d.shouldExclude(startDir) {
		return nil, &contract.ResolveWorkspaceError{
			Code:    contract.ErrorInvalidPath,
			Message: fmt.Sprintf("path %s is excluded from detection", startDir),
			Reason:  contract.ReasonInvalidPath,
		}
	}

	if candidate, err := d.detectFromMetadata(startDir); candidate != nil || err != nil {
		return candidate, err
	}

	return d.walkUp(startDir)
}

func (d *Detector) walkUp(start string) (*contract.WorkspaceCandidate, *contract.ResolveWorkspaceError) {
	dir := start
	depth := 0
	for {
		if d.opts.MaxDepth > 0 && depth >= d.opts.MaxDepth {
			break
		}
		depth++

		if !d.shouldExclude(dir) {
			candidate, err := d.inspectDir(dir)
			if err != nil {
				return nil, err
			}
			if candidate != nil {
				return candidate, nil
			}
		}

		parent := filepath.Dir(dir)
		if parent == dir || d.opts.DisableUpward {
			break
		}
		dir = parent
	}

	return nil, &contract.ResolveWorkspaceError{
		Code:    contract.ErrorInvalidPath,
		Message: fmt.Sprintf("no workspace markers found starting from %s", start),
		Reason:  contract.ReasonInvalidPath,
	}
}

func (d *Detector) inspectDir(dir string) (*contract.WorkspaceCandidate, *contract.ResolveWorkspaceError) {
	markers := make([]string, 0)
	for _, marker := range d.opts.Markers {
		candidate := filepath.Join(dir, marker)
		if exists(candidate) {
			markers = append(markers, marker)
		}
	}
	if len(markers) == 0 {
		return nil, nil
	}
	if err := d.ensureAllowed(dir); err != nil {
		return nil, err
	}
	return &contract.WorkspaceCandidate{
		Root:       dir,
		Markers:    markers,
		Reason:     contract.ReasonFilePath,
		Source:     "file_path",
		Confidence: 0.95,
	}, nil
}

func (d *Detector) detectFromMetadata(start string) (*contract.WorkspaceCandidate, *contract.ResolveWorkspaceError) {
	if d.opts.MetadataFileName == "" {
		return nil, nil
	}
	metadataPath := filepath.Join(start, d.opts.MetadataFileName)
	data, err := os.ReadFile(metadataPath)
	if err != nil {
		return nil, nil
	}
	root := strings.TrimSpace(string(data))
	if root == "" {
		return nil, nil
	}
	if !filepath.IsAbs(root) {
		root = filepath.Join(start, root)
	}
	if resolved, err := filepath.EvalSymlinks(root); err == nil {
		root = resolved
	}
	if err := d.ensureAllowed(root); err != nil {
		return nil, err
	}
	candidate, errReason := d.inspectDir(root)
	if errReason != nil {
		return nil, errReason
	}
	if candidate == nil {
		return nil, &contract.ResolveWorkspaceError{
			Code:    contract.ErrorInvalidPath,
			Message: fmt.Sprintf("metadata referenced root %s but no markers found", root),
			Reason:  contract.ReasonInvalidPath,
		}
	}
	candidate.Reason = contract.ReasonRootsList
	candidate.Source = "metadata"
	candidate.Confidence = 0.85
	return candidate, nil
}

func (d *Detector) ensureAllowed(path string) *contract.ResolveWorkspaceError {
	if len(d.opts.AllowedRoots) == 0 {
		return nil
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return &contract.ResolveWorkspaceError{
			Code:    contract.ErrorInvalidPath,
			Message: fmt.Sprintf("failed to resolve path %s: %v", path, err),
			Reason:  contract.ReasonInvalidPath,
		}
	}
	if resolved, err := filepath.EvalSymlinks(abs); err == nil {
		abs = resolved
	}
	for _, allowed := range d.opts.AllowedRoots {
		al, err := filepath.Abs(allowed)
		if err != nil {
			continue
		}
		if strings.HasPrefix(abs, filepath.Clean(al)+string(os.PathSeparator)) || abs == filepath.Clean(al) {
			return nil
		}
	}
	return &contract.ResolveWorkspaceError{
		Code:    contract.ErrorOutsideAllowedRoots,
		Message: fmt.Sprintf("path %s is outside allowed workspace roots", abs),
		Reason:  contract.ReasonOutsideAllowedRoots,
	}
}

func (d *Detector) shouldExclude(path string) bool {
	for _, pattern := range d.opts.ExcludePatterns {
		if pattern == "" {
			continue
		}
		if strings.Contains(path, pattern) {
			return true
		}
	}
	return false
}

func wrapPathErr(action string, err error) *contract.ResolveWorkspaceError {
	return &contract.ResolveWorkspaceError{
		Code:    contract.ErrorInvalidPath,
		Message: fmt.Sprintf("%s: %v", action, err),
		Reason:  contract.ReasonInvalidPath,
	}
}

func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// Utility exported for tests.
var ErrEmptyMetadata = errors.New("metadata root missing markers")

package engine

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/doITmagic/rag-code-mcp/v2/index"
)

const ideRules = `# RagCode MCP

This workspace is indexed by RagCode MCP. For anything about the code in it,
query the index instead of guessing or grepping blindly:

- rag_search: semantic + exact search by name or qualified name. Start here. Add language, path_prefix
  or kinds to keep other languages or folders out of the results.
- rag_find_usages: every reference to a symbol.
- rag_call_hierarchy: callers and callees of a function.
- rag_list_package_exports: the public surface of a package.
- rag_read_file_context: a file with its symbols and relations.
- rag_index_workspace: (re)index when a search comes back empty or stale, and
  after editing files when a response's index block has "watch": such a
  root is not watched, so edits are not picked up automatically.

Always pass workspace_root: the absolute path of this project's top directory,
the same on every call: the folder opened in the editor, or the nearest parent
that holds .git (the repository root, also in a monorepo) or, without git, a
project file (go.mod, package.json, composer.json, pyproject.toml,
project.godot); with neither, the smallest directory that holds the files
being worked on. Never a subdirectory of it, never a parent of other projects.
`

// IDEPointer is the line WriteIDERules adds to the PointerFiles.
const IDEPointer = "RagCode MCP: read .ragcode/RAGCODE.md and always pass workspace_root to the rag_* tools."

// WriteIDERules writes <root>/.ragcode/RAGCODE.md plus a .gitignore of "*"
// there, and adds a one-line pointer to .cursor/rules/ragcode.mdc (when
// .cursor/rules exists) and to each instructionFiles entry that exists, once,
// skipping the files git tracks. Called by the index tool, not the engine.
// A cloned repo controls these paths, so no write follows a symlink; a refused
// file is reported but does not stop the others.
func WriteIDERules(root string) error {
	real, err := filepath.EvalSymlinks(root)
	if err != nil {
		return err
	}
	var errs []error
	dir := filepath.Join(root, ".ragcode")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		errs = append(errs, err)
	} else if err := within(real, dir); err != nil {
		errs = append(errs, err)
	} else if err := index.WriteAtomic(filepath.Join(dir, "RAGCODE.md"), []byte(ideRules)); err != nil {
		errs = append(errs, err)
	} else if err := index.WriteAtomic(filepath.Join(dir, ".gitignore"), []byte("*\n")); err != nil {
		// Keeps .ragcode out of git without touching the repository's own ignores.
		errs = append(errs, err)
	}
	if info, err := os.Stat(filepath.Join(root, ".cursor", "rules")); err == nil && info.IsDir() {
		if err := appendPointer(real, filepath.Join(root, ".cursor", "rules", "ragcode.mdc"), "---\nalwaysApply: true\n---\n"); err != nil {
			errs = append(errs, err)
		}
	}
	for _, rel := range instructionFiles {
		path := filepath.Join(root, filepath.FromSlash(rel))
		if _, err := os.Stat(path); err != nil {
			continue // only files the project already has
		}
		if err := appendPointer(real, path, ""); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// PointerFiles is every path under root WriteIDERules may add IDEPointer to,
// whether or not it exists: what an uninstall has to strip.
func PointerFiles(root string) []string {
	paths := []string{filepath.Join(root, ".cursor", "rules", "ragcode.mdc")}
	for _, rel := range instructionFiles {
		paths = append(paths, filepath.Join(root, filepath.FromSlash(rel)))
	}
	return paths
}

// instructionFiles are the agent instruction files the pointer is appended
// to when they already exist: Claude Code, Codex/ChatGPT and most others
// (AGENTS.md), Gemini CLI, GitHub Copilot, Windsurf, Cline.
var instructionFiles = []string{"CLAUDE.md", "AGENTS.md", "GEMINI.md", ".github/copilot-instructions.md", ".windsurfrules", ".clinerules"}

// tracked reports whether git tracks path: a pointer there would leave the
// working tree dirty and can block a checkout or pull. Without git, false.
func tracked(path string) bool {
	return exec.Command("git", "-C", filepath.Dir(path), "ls-files", "--error-unmatch", "--", filepath.Base(path)).Run() == nil
}

// within refuses dir when, symlinks resolved, it is outside the real root.
func within(real, dir string) error {
	d, err := filepath.EvalSymlinks(dir)
	if err != nil {
		return err
	}
	if d != real && !strings.HasPrefix(d, real+string(filepath.Separator)) {
		return fmt.Errorf("%s resolves outside the workspace (%s), not writing there", dir, d)
	}
	return nil
}

// appendPointer adds IDEPointer to path unless it is already there; header
// starts a file that does not exist yet.
func appendPointer(real, path, header string) error {
	if err := within(real, filepath.Dir(path)); err != nil {
		return err
	}
	if info, err := os.Lstat(path); err == nil && info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("%s is a symlink, not appending to it", path)
	}
	if tracked(path) {
		return nil
	}
	data, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	if strings.Contains(string(data), IDEPointer) {
		return nil
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	line := IDEPointer + "\n"
	switch {
	case len(data) == 0:
		line = header + line
	case data[len(data)-1] != '\n':
		line = "\n" + line
	}
	_, err = f.WriteString(line)
	return err
}

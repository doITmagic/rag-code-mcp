package iderules

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/doITmagic/rag-code-mcp/internal/generatedfile"
)

func TestWriteCreatesCurrentLocations(t *testing.T) {
	root := t.TempDir()
	Write(root)

	for _, want := range []string{
		".cursor/rules/ragcode.mdc",
		".windsurf/rules/ragcode.md",
		".clinerules/ragcode.md",
		".roo/rules/ragcode.md",
		"CLAUDE.md",
	} {
		if _, err := os.Stat(filepath.Join(root, want)); err != nil {
			t.Errorf("missing %s: %v", want, err)
		}
	}
}

func TestGeneratedRulesLocallyIgnoredAndUserEditsPreserved(t *testing.T) {
	root := t.TempDir()
	if out, err := exec.Command("git", "-C", root, "init").CombinedOutput(); err != nil {
		t.Fatalf("git init: %v %s", err, out)
	}
	Write(root)
	path := filepath.Join(root, "CLAUDE.md")
	if !IsGenerated(path) {
		t.Fatal("generated rule not recognized")
	}
	marked, err := os.ReadFile(path)
	if err != nil || !strings.Contains(string(marked), "ragcode:generated sha256=") {
		t.Fatalf("generated marker missing: %v", err)
	}
	if out, err := exec.Command("git", "-C", root, "status", "--porcelain").CombinedOutput(); err != nil || len(out) != 0 {
		t.Fatalf("generated files pollute status: %v %s", err, out)
	}
	edited := filepath.Join(root, ".cursor", "rules", "ragcode.mdc")
	editedData, err := os.ReadFile(edited)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(edited, append(editedData, []byte("\nmy instructions")...), 0600); err != nil {
		t.Fatal(err)
	}
	old := filepath.Join(root, ".roo", "rules", "ragcode.md")
	oldBody := "old generated rule"
	if err := os.WriteFile(old, []byte(generatedfile.Marker(oldBody)+oldBody), 0600); err != nil {
		t.Fatal(err)
	}
	Write(root)
	if IsGenerated(edited) {
		t.Fatal("user edits overwritten")
	}
	if data, err := os.ReadFile(old); err != nil || strings.Contains(string(data), oldBody) || !IsGenerated(old) {
		t.Fatalf("old generated rule not upgraded: %v", err)
	}
	Remove(root)
	if data, err := os.ReadFile(edited); err != nil || !strings.HasSuffix(string(data), "my instructions") {
		t.Fatalf("user edits lost: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(root, ".git", "info", "exclude"))
	if err != nil || strings.Contains(string(data), "BEGIN RagCode") {
		t.Fatalf("local exclusions not removed: %v", err)
	}
}

func TestRemoveKeepsCommittedGeneratedRules(t *testing.T) {
	root := t.TempDir()
	if out, err := exec.Command("git", "-C", root, "init").CombinedOutput(); err != nil {
		t.Fatalf("git init: %v %s", err, out)
	}
	Write(root)
	if out, err := exec.Command("git", "-C", root, "add", "-f", "CLAUDE.md").CombinedOutput(); err != nil {
		t.Fatalf("git add: %v %s", err, out)
	}

	Remove(root)

	if !IsGenerated(filepath.Join(root, "CLAUDE.md")) {
		t.Fatal("git-tracked CLAUDE.md was deleted")
	}
	if _, err := os.Stat(filepath.Join(root, ".roo", "rules", "ragcode.md")); !os.IsNotExist(err) {
		t.Fatal("untracked generated rule remains")
	}
}

func TestWriteNeverClobbersExistingClaudeMd(t *testing.T) {
	root := t.TempDir()
	claude := filepath.Join(root, "CLAUDE.md")
	if err := os.WriteFile(claude, []byte("my own notes"), 0o644); err != nil {
		t.Fatal(err)
	}

	Write(root)

	got, err := os.ReadFile(claude)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "my own notes" {
		t.Fatalf("CLAUDE.md was overwritten: %q", got)
	}
}

// v1Rules is the legacy rule file RagCode v1 wrote, byte for byte.
const v1Rules = `# RagCode MCP - Semantic Search Rules
# This file is automatically managed by RagCode MCP.

## ⚖️ The Golden Rule
**For any information about the code (location, structure, logic, or usage), you MUST use RagCode MCP tools. Never guess code details from memory; always search the local index first.**

## Available Tools
- search_code: Primary entry point for semantic search.
- get_function_details: Get full implementation of a function.
- find_type_definition: Get struct/interface definitions.
- list_package_exports: See what a module offers.
- search_docs: Find project documentation.

## Usage Guidelines
- Always provide 'file_path' to tools to ensure they detect the correct project context.
- Use 'hybrid_search' if looking for exact variable names or error messages.
- If the tool says "workspace not indexed", use 'index_workspace' once.
`

func TestRemoveLegacyOnlyTouchesRagCodeFiles(t *testing.T) {
	root := t.TempDir()
	ours := filepath.Join(root, ".cursorrules")
	theirs := filepath.Join(root, ".roomodes")
	if err := os.WriteFile(ours, []byte(strings.ReplaceAll(v1Rules, "\n", "\r\n")), 0o644); err != nil {
		t.Fatal(err)
	}
	// RagCode's file that the user extended now holds their rules too.
	extended := filepath.Join(root, ".windsurfrules")
	if err := os.WriteFile(extended, []byte(v1Rules+"\n## My own team rules\nAlways write tests.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// A hand-written file that merely names the server is still the user's.
	if err := os.WriteFile(theirs, []byte(`{"customModes":[{"roleDefinition":"Uses RagCode MCP tools"}]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	cline := filepath.Join(root, ".clinerules")
	if err := os.WriteFile(cline, []byte("Always write tests.\nWe also use the RagCode MCP server.\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	RemoveLegacy(root)
	Write(root)

	if _, err := os.Stat(ours); !os.IsNotExist(err) {
		t.Error(".cursorrules written by RagCode should have been removed")
	}
	for _, keep := range []string{theirs, cline, extended} {
		if info, err := os.Stat(keep); err != nil || info.IsDir() {
			t.Errorf("hand-written %s must be left alone: %v", filepath.Base(keep), err)
		}
	}
}

func TestWriteIsIdempotent(t *testing.T) {
	root := t.TempDir()
	Write(root)

	path := filepath.Join(root, ".cursor", "rules", "ragcode.mdc")
	before, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}

	Write(root)

	after, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if !before.ModTime().Equal(after.ModTime()) {
		t.Error("unchanged content should not be rewritten")
	}
}

func TestRemoveOnlyDeletesGeneratedRules(t *testing.T) {
	root := t.TempDir()
	Write(root)
	custom := filepath.Join(root, ".windsurf", "rules", "general.md")
	if err := os.WriteFile(custom, []byte("keep"), 0o644); err != nil {
		t.Fatal(err)
	}
	edited := filepath.Join(root, ".roo", "rules", "ragcode.md")
	if err := os.WriteFile(edited, []byte("user edited"), 0o644); err != nil {
		t.Fatal(err)
	}

	Remove(root)

	for _, keep := range []string{custom, edited} {
		if _, err := os.Stat(keep); err != nil {
			t.Fatalf("user file removed: %s", keep)
		}
	}
	for _, gone := range []string{
		filepath.Join(root, ".cursor", "rules", "ragcode.mdc"),
		filepath.Join(root, ".clinerules", "ragcode.md"),
		filepath.Join(root, "CLAUDE.md"),
	} {
		if _, err := os.Stat(gone); !os.IsNotExist(err) {
			t.Fatalf("generated file remains: %s", gone)
		}
	}
}

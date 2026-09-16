package iderules

import (
	"os"
	"path/filepath"
	"testing"
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

func TestRemoveLegacyOnlyTouchesRagCodeFiles(t *testing.T) {
	root := t.TempDir()
	ours := filepath.Join(root, ".cursorrules")
	theirs := filepath.Join(root, ".roomodes")
	if err := os.WriteFile(ours, []byte("# RagCode MCP rules"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(theirs, []byte("hand-written modes"), 0o644); err != nil {
		t.Fatal(err)
	}

	RemoveLegacy(root)

	if _, err := os.Stat(ours); !os.IsNotExist(err) {
		t.Error(".cursorrules written by RagCode should have been removed")
	}
	if _, err := os.Stat(theirs); err != nil {
		t.Error("hand-written .roomodes must be left alone")
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

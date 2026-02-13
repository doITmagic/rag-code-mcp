package workspace

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestRegistry_RegisterAndFind(t *testing.T) {
	tmp := t.TempDir()
	regPath := filepath.Join(tmp, "workspaces.json")
	reg := NewRegistry(regPath)

	info := &Info{
		Root:      "/home/user/project-a",
		ID:        "ws-a",
		Languages: []string{"go"},
	}
	if err := reg.Register(info); err != nil {
		t.Fatalf("Register failed: %v", err)
	}

	if reg.Len() != 1 {
		t.Fatalf("expected 1 workspace, got %d", reg.Len())
	}

	entry := reg.FindByName("project-a")
	if entry == nil {
		t.Fatal("FindByName returned nil")
	}
	if entry.Root != "/home/user/project-a" {
		t.Fatalf("expected root /home/user/project-a, got %q", entry.Root)
	}

	entry = reg.FindByRoot("/home/user/project-a")
	if entry == nil {
		t.Fatal("FindByRoot returned nil")
	}
}

func TestRegistry_Single(t *testing.T) {
	tmp := t.TempDir()
	reg := NewRegistry(filepath.Join(tmp, "workspaces.json"))

	if reg.Single() != nil {
		t.Fatal("expected nil for empty registry")
	}

	reg.Register(&Info{Root: "/home/user/project-a", ID: "ws-a"})
	if s := reg.Single(); s == nil {
		t.Fatal("expected single workspace")
	}

	reg.Register(&Info{Root: "/home/user/project-b", ID: "ws-b"})
	if reg.Single() != nil {
		t.Fatal("expected nil for multiple workspaces")
	}
}

func TestRegistry_Remove(t *testing.T) {
	tmp := t.TempDir()
	reg := NewRegistry(filepath.Join(tmp, "workspaces.json"))

	reg.Register(&Info{Root: "/home/user/project-a", ID: "ws-a"})
	reg.Register(&Info{Root: "/home/user/project-b", ID: "ws-b"})

	if err := reg.Remove("project-a"); err != nil {
		t.Fatalf("Remove failed: %v", err)
	}
	if reg.Len() != 1 {
		t.Fatalf("expected 1 workspace after remove, got %d", reg.Len())
	}
}

func TestRegistry_Persistence(t *testing.T) {
	tmp := t.TempDir()
	regPath := filepath.Join(tmp, "workspaces.json")

	reg1 := NewRegistry(regPath)
	reg1.Register(&Info{Root: "/home/user/project-a", ID: "ws-a", Languages: []string{"go"}})

	// Load from disk
	reg2 := NewRegistry(regPath)
	if reg2.Len() != 1 {
		t.Fatalf("expected 1 workspace after reload, got %d", reg2.Len())
	}
	entry := reg2.FindByName("project-a")
	if entry == nil || entry.Root != "/home/user/project-a" {
		t.Fatal("persisted workspace not found after reload")
	}
}

func TestRegistry_UpdateExisting(t *testing.T) {
	tmp := t.TempDir()
	reg := NewRegistry(filepath.Join(tmp, "workspaces.json"))

	reg.Register(&Info{Root: "/home/user/project-a", ID: "ws-a", Languages: []string{"go"}})
	reg.Register(&Info{Root: "/home/user/project-a", ID: "ws-a", Languages: []string{"go", "python"}})

	if reg.Len() != 1 {
		t.Fatalf("expected 1 workspace (updated), got %d", reg.Len())
	}
	entry := reg.FindByRoot("/home/user/project-a")
	if len(entry.Languages) != 2 {
		t.Fatalf("expected 2 languages after update, got %d", len(entry.Languages))
	}
}

func TestRegistry_FormatList(t *testing.T) {
	tmp := t.TempDir()
	reg := NewRegistry(filepath.Join(tmp, "workspaces.json"))

	msg := reg.FormatList()
	if msg == "" {
		t.Fatal("expected non-empty message for empty registry")
	}

	reg.Register(&Info{Root: "/home/user/project-a", ID: "ws-a"})
	reg.Register(&Info{Root: "/home/user/project-b", ID: "ws-b"})

	msg = reg.FormatList()
	if msg == "" {
		t.Fatal("expected non-empty formatted list")
	}
}

func TestRegistry_FileCreation(t *testing.T) {
	tmp := t.TempDir()
	subDir := filepath.Join(tmp, "nested", "dir")
	regPath := filepath.Join(subDir, "workspaces.json")

	reg := NewRegistry(regPath)
	reg.Register(&Info{Root: "/home/user/project-a", ID: "ws-a"})

	if _, err := os.Stat(regPath); os.IsNotExist(err) {
		t.Fatal("registry file was not created")
	}
}

// Keep time import used for IndexedAt checks
var _ = time.Now

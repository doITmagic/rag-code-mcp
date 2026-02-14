package registry

import (
	"path/filepath"
	"testing"
	"time"
)

func TestRegistryUpsertAndLookup(t *testing.T) {
	tmp := filepath.Join(t.TempDir(), "registry.json")
	r, err := New(tmp)
	if err != nil {
		t.Fatalf("new registry: %v", err)
	}

	entry, err := r.Upsert("/root/project", "proj", "windsurf")
	if err != nil {
		t.Fatalf("upsert: %v", err)
	}
	if entry.ID == "" {
		t.Fatalf("expected entry id")
	}

	retrieved, ok := r.LookupByID(entry.ID)
	if !ok || retrieved.Root != "/root/project" {
		t.Fatalf("lookup by id failed")
	}

	retrieved, ok = r.LookupByRoot("/root/project")
	if !ok || retrieved.Name != "proj" {
		t.Fatalf("lookup by root failed")
	}
}

func TestRegistryPersistence(t *testing.T) {
	tmp := filepath.Join(t.TempDir(), "registry.json")
	r, err := New(tmp)
	if err != nil {
		t.Fatalf("new registry: %v", err)
	}

	if _, err := r.Upsert("/one", "one", "clientA"); err != nil {
		t.Fatalf("upsert: %v", err)
	}

	r2, err := New(tmp)
	if err != nil {
		t.Fatalf("reload registry: %v", err)
	}
	if _, ok := r2.LookupByRoot("/one"); !ok {
		t.Fatalf("expected entry after reload")
	}
}

func TestRegistryCleanup(t *testing.T) {
	tmp := filepath.Join(t.TempDir(), "registry.json")
	r, err := New(tmp)
	if err != nil {
		t.Fatalf("new registry: %v", err)
	}

	r.clock = func() time.Time { return time.Unix(0, 0) }
	if _, err := r.Upsert("/old", "old", "client"); err != nil {
		t.Fatalf("upsert old: %v", err)
	}
	r.clock = func() time.Time { return time.Unix(10, 0) }
	if _, err := r.Upsert("/new", "new", "client"); err != nil {
		t.Fatalf("upsert new: %v", err)
	}

	cutoff := time.Unix(5, 0)
	if err := r.Cleanup(cutoff); err != nil {
		t.Fatalf("cleanup: %v", err)
	}

	if _, ok := r.LookupByRoot("/old"); ok {
		t.Fatalf("old entry should be removed")
	}
	if _, ok := r.LookupByRoot("/new"); !ok {
		t.Fatalf("new entry should remain")
	}
}

package workspace

import (
	"testing"
	"time"
)

func TestGetSingleWorkspaceRoot_EmptyCache(t *testing.T) {
	cache := NewCache(5 * time.Minute)
	m := &Manager{cache: cache}

	root := m.GetSingleWorkspaceRoot()
	if root != "" {
		t.Fatalf("expected empty string for empty cache, got %q", root)
	}
}

func TestGetSingleWorkspaceRoot_SingleWorkspace(t *testing.T) {
	cache := NewCache(5 * time.Minute)
	cache.Set("/home/user/project-a/main.go", &Info{Root: "/home/user/project-a"})

	m := &Manager{cache: cache}

	root := m.GetSingleWorkspaceRoot()
	if root != "/home/user/project-a" {
		t.Fatalf("expected /home/user/project-a, got %q", root)
	}
}

func TestGetSingleWorkspaceRoot_SingleWorkspaceMultipleCacheKeys(t *testing.T) {
	cache := NewCache(5 * time.Minute)
	// Same workspace root cached under different file paths
	cache.Set("/home/user/project-a/main.go", &Info{Root: "/home/user/project-a"})
	cache.Set("/home/user/project-a/internal/pkg/foo.go", &Info{Root: "/home/user/project-a"})

	m := &Manager{cache: cache}

	root := m.GetSingleWorkspaceRoot()
	if root != "/home/user/project-a" {
		t.Fatalf("expected /home/user/project-a (deduplicated), got %q", root)
	}
}

func TestGetSingleWorkspaceRoot_MultipleWorkspaces(t *testing.T) {
	cache := NewCache(5 * time.Minute)
	cache.Set("/home/user/project-a/main.go", &Info{Root: "/home/user/project-a"})
	cache.Set("/home/user/project-b/main.go", &Info{Root: "/home/user/project-b"})

	m := &Manager{cache: cache}

	root := m.GetSingleWorkspaceRoot()
	if root != "" {
		t.Fatalf("expected empty string for multiple workspaces, got %q", root)
	}
}

func TestGetSingleWorkspaceRoot_ExpiredEntries(t *testing.T) {
	cache := NewCache(1 * time.Millisecond)
	cache.Set("/home/user/project-a/main.go", &Info{Root: "/home/user/project-a"})

	// Wait for cache to expire
	time.Sleep(5 * time.Millisecond)

	m := &Manager{cache: cache}

	root := m.GetSingleWorkspaceRoot()
	if root != "" {
		t.Fatalf("expected empty string for expired cache, got %q", root)
	}
}

func TestGetSingleWorkspaceRoot_MixedExpiredAndValid(t *testing.T) {
	cache := NewCache(1 * time.Millisecond)
	cache.Set("/home/user/project-old/main.go", &Info{Root: "/home/user/project-old"})

	// Wait for first entry to expire
	time.Sleep(5 * time.Millisecond)

	// Add a fresh entry with a new cache (longer TTL)
	// We need to directly manipulate the cache since NewCache sets TTL globally
	cache.ttl = 5 * time.Minute
	cache.Set("/home/user/project-new/main.go", &Info{Root: "/home/user/project-new"})

	m := &Manager{cache: cache}

	root := m.GetSingleWorkspaceRoot()
	if root != "/home/user/project-new" {
		t.Fatalf("expected /home/user/project-new (only valid entry), got %q", root)
	}
}

package registry

import (
	"testing"
	"time"
)

// newTestRegistry creates a Registry with entries populated in-memory only (no disk I/O).
func newTestRegistry(entries ...*Entry) *Registry {
	r := &Registry{
		entries:    make(map[string]*Entry),
		candidates: make(map[string]*CandidateEntry),
		indexRoot:  make(map[string]string),
		indexName:  make(map[string][]string),
		clock:      time.Now,
		audit:      noopAuditSink{},
	}
	for _, e := range entries {
		r.addEntry(e)
	}
	return r
}

func TestFindParentWorkspace(t *testing.T) {
	r := newTestRegistry(&Entry{
		ID:   hashRoot("/home/user/projects/big-project"),
		Root: "/home/user/projects/big-project",
		Name: "big-project",
	})

	tests := []struct {
		name      string
		path      string
		wantRoot  string
		wantFound bool
	}{
		{
			name:      "child directory matches parent",
			path:      "/home/user/projects/big-project/submodule/graph",
			wantRoot:  "/home/user/projects/big-project",
			wantFound: true,
		},
		{
			name:      "deep nested child matches",
			path:      "/home/user/projects/big-project/a/b/c/d",
			wantRoot:  "/home/user/projects/big-project",
			wantFound: true,
		},
		{
			name:      "same path does not match (not strictly inside)",
			path:      "/home/user/projects/big-project",
			wantRoot:  "",
			wantFound: false,
		},
		{
			name:      "sibling directory does not match",
			path:      "/home/user/projects/other-project",
			wantRoot:  "",
			wantFound: false,
		},
		{
			name:      "parent directory does not match",
			path:      "/home/user/projects",
			wantRoot:  "",
			wantFound: false,
		},
		{
			name:      "similar prefix but different dir does not match",
			path:      "/home/user/projects/big-project-v2/src",
			wantRoot:  "",
			wantFound: false,
		},
		{
			name:      "empty path returns not found",
			path:      "",
			wantRoot:  "",
			wantFound: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root, found := r.FindParentWorkspace(tt.path)
			if found != tt.wantFound {
				t.Errorf("FindParentWorkspace(%q) found=%v, want %v", tt.path, found, tt.wantFound)
			}
			if root != tt.wantRoot {
				t.Errorf("FindParentWorkspace(%q) root=%q, want %q", tt.path, root, tt.wantRoot)
			}
		})
	}
}

func TestFindParentWorkspacePicksDeepest(t *testing.T) {
	r := newTestRegistry(
		&Entry{
			ID:   hashRoot("/home/user/projects"),
			Root: "/home/user/projects",
			Name: "projects",
		},
		&Entry{
			ID:   hashRoot("/home/user/projects/monorepo"),
			Root: "/home/user/projects/monorepo",
			Name: "monorepo",
		},
	)

	// A path inside monorepo should match monorepo (the deepest parent), not projects
	root, found := r.FindParentWorkspace("/home/user/projects/monorepo/packages/core")
	if !found {
		t.Fatal("expected to find parent workspace")
	}
	if root != "/home/user/projects/monorepo" {
		t.Fatalf("expected deepest parent /home/user/projects/monorepo, got %s", root)
	}

	// A path inside projects but outside monorepo should match projects
	root, found = r.FindParentWorkspace("/home/user/projects/other-app/src")
	if !found {
		t.Fatal("expected to find parent workspace")
	}
	if root != "/home/user/projects" {
		t.Fatalf("expected parent /home/user/projects, got %s", root)
	}
}

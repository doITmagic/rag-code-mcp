package tests

import (
	context "context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/doITmagic/rag-code-mcp/v2/internal/branchstate"
	"github.com/doITmagic/rag-code-mcp/v2/internal/contract"
	"github.com/doITmagic/rag-code-mcp/v2/internal/resolver"
)

type scenario struct {
	name                string
	req                 contract.ResolveWorkspaceRequest
	deps                resolver.Dependencies
	expectErr           contract.ErrorCode
	expectRoot          string
	expectReason        contract.ReasonCode
	requireConfirmation bool
}

func TestResolverScenarios(t *testing.T) {
	ctx := context.Background()

	tests := []scenario{
		{
			name:         "explicit workspace root",
			req:          contract.ResolveWorkspaceRequest{WorkspaceRoot: "/tmp/project"},
			expectRoot:   "/tmp/project",
			expectReason: contract.ReasonExplicitWorkspaceRoot,
		},
		{
			name:         "file path detection",
			req:          contract.ResolveWorkspaceRequest{FilePath: "/tmp/project/main.go"},
			expectRoot:   "/detected/root",
			expectReason: contract.ReasonFilePath,
			deps: resolver.Dependencies{
				Detector: &fakeDetector{candidate: &resolver.WorkspaceCandidate{Root: "/detected/root", Reason: contract.ReasonFilePath}},
			},
		},
		{
			name:         "workspace alias",
			req:          contract.ResolveWorkspaceRequest{Workspace: "alias"},
			expectRoot:   "/alias/root",
			expectReason: contract.ReasonWorkspaceAlias,
			deps: resolver.Dependencies{
				Registry: &fakeRegistry{candidate: &resolver.WorkspaceCandidate{Root: "/alias/root", Reason: contract.ReasonWorkspaceAlias}},
			},
		},
		{
			name:         "single root entry",
			req:          contract.ResolveWorkspaceRequest{Roots: []string{"/only"}},
			expectRoot:   "/only",
			expectReason: contract.ReasonRootsList,
		},
		{
			name:      "ambiguous roots strict mode",
			req:       contract.ResolveWorkspaceRequest{Roots: []string{"/a", "/b"}, StrictMode: true},
			expectErr: contract.ErrorAmbiguousWorkspace,
		},
		{
			name:                "ambiguous roots confirmation",
			req:                 contract.ResolveWorkspaceRequest{Roots: []string{"/a", "/b"}},
			requireConfirmation: true,
			expectReason:        contract.ReasonConfirmationRequired,
		},
		{
			name:      "missing context",
			req:       contract.ResolveWorkspaceRequest{},
			expectErr: contract.ErrorNoContext,
		},
		{
			name:      "workspace alias missing",
			req:       contract.ResolveWorkspaceRequest{Workspace: "missing"},
			deps:      resolver.Dependencies{Registry: &fakeRegistry{}},
			expectErr: contract.ErrorAmbiguousWorkspace,
		},
		{
			name:      "file path without detector",
			req:       contract.ResolveWorkspaceRequest{FilePath: "/tmp/file.go"},
			expectErr: contract.ErrorNoContext,
		},
		{
			name:      "roots whitespace only",
			req:       contract.ResolveWorkspaceRequest{Roots: []string{"   "}},
			expectErr: contract.ErrorNoContext,
		},
		{
			name:         "roots scoring selects deepest",
			req:          contract.ResolveWorkspaceRequest{Roots: []string{"/a", "/a/b/c"}},
			expectRoot:   "/a/b/c",
			expectReason: contract.ReasonRootsList,
		},
		{
			name:                "ambiguous roots confirmation reason",
			req:                 contract.ResolveWorkspaceRequest{Roots: []string{"/c", "/d"}},
			requireConfirmation: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			res := resolver.New(tt.deps)
			req := tt.req
			resp, err := res.Resolve(ctx, req)

			if tt.expectErr != "" {
				if err == nil {
					t.Fatalf("expected error %s, got nil", tt.expectErr)
				}
				if err.Code != tt.expectErr {
					t.Fatalf("expected error %s, got %s", tt.expectErr, err.Code)
				}
				return
			}

			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if tt.requireConfirmation {
				if !resp.RequiresConfirmation {
					t.Fatalf("expected confirmation required")
				}
				if resp.Reason != contract.ReasonConfirmationRequired {
					t.Fatalf("expected reason %s, got %s", contract.ReasonConfirmationRequired, resp.Reason)
				}
				return
			}

			if resp.ResolvedRoot != tt.expectRoot {
				t.Fatalf("expected root %s, got %s", tt.expectRoot, resp.ResolvedRoot)
			}
			if tt.expectReason != "" && resp.Reason != tt.expectReason {
				t.Fatalf("expected reason %s, got %s", tt.expectReason, resp.Reason)
			}
		})
	}
}

type fakeDetector struct {
	candidate *resolver.WorkspaceCandidate
	err       *contract.ResolveWorkspaceError
}

func (f *fakeDetector) DetectFromFilePath(ctx context.Context, filePath string) (*resolver.WorkspaceCandidate, *contract.ResolveWorkspaceError) {
	if f.err != nil {
		return nil, f.err
	}
	return f.candidate, nil
}

type fakeRegistry struct {
	candidate *resolver.WorkspaceCandidate
	err       *contract.ResolveWorkspaceError
}

func (f *fakeRegistry) ResolveAlias(ctx context.Context, alias string) (*resolver.WorkspaceCandidate, *contract.ResolveWorkspaceError) {
	if f.err != nil {
		return nil, f.err
	}
	return f.candidate, nil
}

type branchstateAnnotator struct {
	mgr *branchstate.Manager
}

func (a *branchstateAnnotator) Annotate(ctx context.Context, root string, resp *contract.ResolveWorkspaceResponse) *contract.ResolveWorkspaceError {
	_, reindex, reason, err := a.mgr.CompareAndUpdate(ctx, root)
	if err != nil {
		return err
	}
	resp.ReindexRequired = reindex
	if reason != "" {
		resp.Reason = reason
	}
	return nil
}

func TestResolverBranchstateFirstSeen(t *testing.T) {
	repo := initGitRepo(t)
	annotator := &branchstateAnnotator{mgr: branchstate.NewManager(branchstate.WithCacheTTL(0))}
	r := resolver.New(resolver.Dependencies{BranchAnnotator: annotator})

	resp, err := r.Resolve(context.Background(), contract.ResolveWorkspaceRequest{WorkspaceRoot: repo})
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if !resp.ReindexRequired {
		t.Fatalf("expected reindex required on first seen")
	}
	if resp.Reason != contract.ReasonFirstSeen {
		t.Fatalf("expected reason %s, got %s", contract.ReasonFirstSeen, resp.Reason)
	}
}

func TestResolverBranchstateBranchChanged(t *testing.T) {
	repo := initGitRepo(t)
	annotator := &branchstateAnnotator{mgr: branchstate.NewManager(branchstate.WithCacheTTL(0))}
	r := resolver.New(resolver.Dependencies{BranchAnnotator: annotator})

	_, err := r.Resolve(context.Background(), contract.ResolveWorkspaceRequest{WorkspaceRoot: repo})
	if err != nil {
		t.Fatalf("unexpected err on first resolve: %v", err)
	}
	runGit(t, repo, "checkout", "-b", "feature/test")

	resp, err := r.Resolve(context.Background(), contract.ResolveWorkspaceRequest{WorkspaceRoot: repo})
	if err != nil {
		t.Fatalf("unexpected err after branch switch: %v", err)
	}
	if !resp.ReindexRequired {
		t.Fatalf("expected reindex after branch switch")
	}
	if resp.Reason != contract.ReasonBranchChanged {
		t.Fatalf("expected reason %s, got %s", contract.ReasonBranchChanged, resp.Reason)
	}
}

func TestResolverBranchstateHeadChangedSameBranch(t *testing.T) {
	repo := initGitRepo(t)
	annotator := &branchstateAnnotator{mgr: branchstate.NewManager(branchstate.WithCacheTTL(0))}
	r := resolver.New(resolver.Dependencies{BranchAnnotator: annotator})

	_, err := r.Resolve(context.Background(), contract.ResolveWorkspaceRequest{WorkspaceRoot: repo})
	if err != nil {
		t.Fatalf("unexpected err on first resolve: %v", err)
	}

	file := filepath.Join(repo, "next.txt")
	if err := os.WriteFile(file, []byte("v2"), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}
	runGit(t, repo, "add", "next.txt")
	runGit(t, repo, "commit", "-m", "update head")

	resp, err := r.Resolve(context.Background(), contract.ResolveWorkspaceRequest{WorkspaceRoot: repo})
	if err != nil {
		t.Fatalf("unexpected err after head change: %v", err)
	}
	if !resp.ReindexRequired {
		t.Fatalf("expected reindex after head change")
	}
	if resp.Reason != contract.ReasonHeadChanged {
		t.Fatalf("expected reason %s, got %s", contract.ReasonHeadChanged, resp.Reason)
	}
}

func TestResolverBranchstateNonGitFallback(t *testing.T) {
	nonRepo := t.TempDir()
	annotator := &branchstateAnnotator{mgr: branchstate.NewManager(branchstate.WithCacheTTL(0))}
	r := resolver.New(resolver.Dependencies{BranchAnnotator: annotator})

	resp, err := r.Resolve(context.Background(), contract.ResolveWorkspaceRequest{WorkspaceRoot: nonRepo})
	if err != nil {
		t.Fatalf("expected no hard error for non-git repo, got: %v", err)
	}
	if resp.Reason != contract.ReasonRootsUnavailable {
		t.Fatalf("expected reason %s, got %s", contract.ReasonRootsUnavailable, resp.Reason)
	}
	if resp.ReindexRequired {
		t.Fatalf("did not expect reindex for non-git fallback")
	}
}

func initGitRepo(t *testing.T) string {
	t.Helper()
	repo := t.TempDir()
	runGit(t, repo, "init")
	runGit(t, repo, "config", "user.email", "test@example.com")
	runGit(t, repo, "config", "user.name", "Test User")
	if err := os.WriteFile(filepath.Join(repo, "init.txt"), []byte("init"), 0o644); err != nil {
		t.Fatalf("write init file: %v", err)
	}
	runGit(t, repo, "add", "init.txt")
	runGit(t, repo, "commit", "-m", "init")
	return repo
}

func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v failed: %v\n%s", args, err, string(out))
	}
}

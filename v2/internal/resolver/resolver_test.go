package resolver

import (
	context "context"
	testing "testing"

	"github.com/doITmagic/rag-code-mcp/v2/internal/contract"
)

func TestResolveWorkspaceRoot(t *testing.T) {
	ctx := context.Background()
	r := New(Dependencies{})
	req := contract.ResolveWorkspaceRequest{WorkspaceRoot: "/tmp/project"}

	resp, err := r.Resolve(ctx, req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.ResolvedRoot != "/tmp/project" {
		t.Fatalf("expected root /tmp/project, got %s", resp.ResolvedRoot)
	}
	if resp.Reason != contract.ReasonExplicitWorkspaceRoot {
		t.Fatalf("expected reason %s, got %s", contract.ReasonExplicitWorkspaceRoot, resp.Reason)
	}
}

func TestResolveFilePath(t *testing.T) {
	detector := &fakeDetector{candidate: &WorkspaceCandidate{Root: "/tmp/detected", Reason: contract.ReasonFilePath}}
	r := New(Dependencies{Detector: detector})
	req := contract.ResolveWorkspaceRequest{FilePath: "/tmp/detected/main.go"}

	resp, err := r.Resolve(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.ResolvedRoot != "/tmp/detected" {
		t.Fatalf("expected root /tmp/detected, got %s", resp.ResolvedRoot)
	}
	if resp.Reason != contract.ReasonFilePath {
		t.Fatalf("expected reason %s, got %s", contract.ReasonFilePath, resp.Reason)
	}
}

func TestResolveAlias(t *testing.T) {
	registry := &fakeRegistry{candidate: &WorkspaceCandidate{Root: "/tmp/alias", Reason: contract.ReasonWorkspaceAlias}}
	r := New(Dependencies{Registry: registry})
	req := contract.ResolveWorkspaceRequest{Workspace: "alias"}

	resp, err := r.Resolve(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.ResolvedRoot != "/tmp/alias" {
		t.Fatalf("expected root /tmp/alias, got %s", resp.ResolvedRoot)
	}
	if resp.Reason != contract.ReasonWorkspaceAlias {
		t.Fatalf("expected reason %s, got %s", contract.ReasonWorkspaceAlias, resp.Reason)
	}
}

func TestResolveRootsAmbiguityStrictMode(t *testing.T) {
	r := New(Dependencies{})
	req := contract.ResolveWorkspaceRequest{Roots: []string{"/a", "/b"}, StrictMode: true}

	resp, err := r.Resolve(context.Background(), req)
	if err == nil {
		t.Fatalf("expected error, got nil")
	}
	if err.Code != contract.ErrorAmbiguousWorkspace {
		t.Fatalf("expected ambiguous workspace error, got %s", err.Code)
	}
	if resp != nil {
		t.Fatalf("expected nil response in strict mode")
	}
}

func TestResolveRootsAmbiguityFallback(t *testing.T) {
	r := New(Dependencies{})
	req := contract.ResolveWorkspaceRequest{Roots: []string{"/a", "/b"}}

	resp, err := r.Resolve(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !resp.RequiresConfirmation {
		t.Fatalf("expected confirmation required")
	}
	if len(resp.Candidates) != 2 {
		t.Fatalf("expected 2 candidates, got %d", len(resp.Candidates))
	}
}

func TestResolveDetectorMissing(t *testing.T) {
	r := New(Dependencies{})
	req := contract.ResolveWorkspaceRequest{FilePath: "/tmp/any"}

	resp, err := r.Resolve(context.Background(), req)
	if err == nil {
		t.Fatalf("expected error")
	}
	if err.Reason != contract.ReasonRootsUnavailable {
		t.Fatalf("expected ReasonRootsUnavailable, got %s", err.Reason)
	}
	if resp != nil {
		t.Fatalf("expected nil response")
	}
}

type fakeDetector struct {
	candidate *WorkspaceCandidate
	err       *contract.ResolveWorkspaceError
}

func (f *fakeDetector) DetectFromFilePath(ctx context.Context, filePath string) (*WorkspaceCandidate, *contract.ResolveWorkspaceError) {
	if f.err != nil {
		return nil, f.err
	}
	return f.candidate, nil
}

type fakeRegistry struct {
	candidate *WorkspaceCandidate
	err       *contract.ResolveWorkspaceError
}

func (f *fakeRegistry) ResolveAlias(ctx context.Context, alias string) (*WorkspaceCandidate, *contract.ResolveWorkspaceError) {
	if f.err != nil {
		return nil, f.err
	}
	return f.candidate, nil
}

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
	detector := &fakeDetector{candidate: &contract.WorkspaceCandidate{Root: "/tmp/detected", Reason: contract.ReasonFilePath}}
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
	registry := &fakeRegistry{candidate: &contract.WorkspaceCandidate{Root: "/tmp/alias", Reason: contract.ReasonWorkspaceAlias}}
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

func TestBranchAwareWorkspaceID(t *testing.T) {
	root := "/tmp/project"
	req := contract.ResolveWorkspaceRequest{WorkspaceRoot: root}

	// Case 1: No branch
	r1 := New(Dependencies{})
	resp1, _ := r1.Resolve(context.Background(), req)

	// Case 2: Branch main
	annotator2 := &fakeAnnotator{branch: "main"}
	r2 := New(Dependencies{BranchAnnotator: annotator2})
	resp2, _ := r2.Resolve(context.Background(), req)

	// Case 3: Branch feature
	annotator3 := &fakeAnnotator{branch: "feature"}
	r3 := New(Dependencies{BranchAnnotator: annotator3})
	resp3, _ := r3.Resolve(context.Background(), req)

	// Case 4: Same branch, different HEAD
	annotator4 := &fakeAnnotator{branch: "feature", headSHA: "abcdef"}
	r4 := New(Dependencies{BranchAnnotator: annotator4})
	resp4, _ := r4.Resolve(context.Background(), req)

	// Case 5: Same branch, same HEAD, different worktree
	annotator5 := &fakeAnnotator{branch: "feature", headSHA: "abcdef", worktreeID: "/tmp/wt2"}
	r5 := New(Dependencies{BranchAnnotator: annotator5})
	resp5, _ := r5.Resolve(context.Background(), req)

	if resp1.WorkspaceID == resp2.WorkspaceID {
		t.Errorf("WorkspaceID should be different for null branch and 'main' branch")
	}
	if resp2.WorkspaceID == resp3.WorkspaceID {
		t.Errorf("WorkspaceID should be different for 'main' and 'feature' branch")
	}
	if resp3.WorkspaceID == resp4.WorkspaceID {
		t.Errorf("WorkspaceID should be different for different HEAD SHAs")
	}
	if resp4.WorkspaceID == resp5.WorkspaceID {
		t.Errorf("WorkspaceID should be different for different worktree IDs")
	}
	if resp2.Branch != "main" {
		t.Errorf("expected branch main, got %s", resp2.Branch)
	}
}

func TestConfidenceDecay(t *testing.T) {
	root := "/tmp/project"
	req := contract.ResolveWorkspaceRequest{WorkspaceRoot: root}

	// Case 1: Low risk (no decay)
	annotator1 := &fakeAnnotator{branch: "main", mismatchRisk: "low"}
	r1 := New(Dependencies{BranchAnnotator: annotator1})
	resp1, _ := r1.Resolve(context.Background(), req)
	if resp1.Metadata.Confidence != 1.0 {
		t.Errorf("expected confidence 1.0, got %f", resp1.Metadata.Confidence)
	}

	// Case 2: Medium risk (0.9 decay)
	annotator2 := &fakeAnnotator{branch: "main", mismatchRisk: "medium"}
	r2 := New(Dependencies{BranchAnnotator: annotator2})
	resp2, _ := r2.Resolve(context.Background(), req)
	if resp2.Metadata.Confidence != 0.9 {
		t.Errorf("expected confidence 0.9, got %f", resp2.Metadata.Confidence)
	}

	// Case 3: High risk (0.5 decay)
	annotator3 := &fakeAnnotator{branch: "main", mismatchRisk: "high"}
	r3 := New(Dependencies{BranchAnnotator: annotator3})
	resp3, _ := r3.Resolve(context.Background(), req)
	if resp3.Metadata.Confidence != 0.5 {
		t.Errorf("expected confidence 0.5, got %f", resp3.Metadata.Confidence)
	}
}

type fakeDetector struct {
	candidate *contract.WorkspaceCandidate
	err       *contract.ResolveWorkspaceError
}

func (f *fakeDetector) DetectFromFilePath(ctx context.Context, filePath string) (*contract.WorkspaceCandidate, *contract.ResolveWorkspaceError) {
	if f.err != nil {
		return nil, f.err
	}
	return f.candidate, nil
}

type fakeRegistry struct {
	candidate *contract.WorkspaceCandidate
	err       *contract.ResolveWorkspaceError
}

func (f *fakeRegistry) ResolveAlias(ctx context.Context, alias string) (*contract.WorkspaceCandidate, *contract.ResolveWorkspaceError) {
	if f.err != nil {
		return nil, f.err
	}
	return f.candidate, nil
}

func (f *fakeRegistry) RecordFeedback(ctx context.Context, feedback *contract.PathFeedback) error {
	return nil
}

type fakeAnnotator struct {
	branch       string
	headSHA      string
	worktreeID   string
	mismatchRisk string
	err          *contract.ResolveWorkspaceError
}

func (f *fakeAnnotator) Annotate(ctx context.Context, root string, resp *contract.ResolveWorkspaceResponse) *contract.ResolveWorkspaceError {
	if f.err != nil {
		return f.err
	}
	resp.Branch = f.branch
	resp.HeadSHA = f.headSHA
	resp.WorktreeID = f.worktreeID
	resp.MismatchRisk = f.mismatchRisk
	return nil
}

package resolver

import (
	context "context"
	testing "testing"

	"github.com/doITmagic/rag-code-mcp/pkg/workspace/contract"
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
	if resp.Metadata.UsedFallback {
		t.Fatalf("workspace_root should not be marked as fallback")
	}
	if resp.Metadata.PathContextKey == "" {
		t.Fatalf("expected path context key")
	}
	if resp.PathResolutionSource == "" || resp.PathResolutionConfidence == 0 {
		t.Fatalf("expected top-level path resolution aliases to be populated")
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
	if resp.PathResolutionSource != "file_path" {
		t.Fatalf("expected file_path source, got %s", resp.PathResolutionSource)
	}
	if resp.Metadata.PathContextKey == "" {
		t.Fatalf("expected context key")
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
	if !resp.Metadata.UsedFallback {
		t.Fatalf("registry source should be marked as fallback")
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

func TestResolveRootsSingleMarksFallback(t *testing.T) {
	r := New(Dependencies{})
	req := contract.ResolveWorkspaceRequest{Roots: []string{"/a"}}

	resp, err := r.Resolve(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !resp.Metadata.UsedFallback {
		t.Fatalf("roots source should be marked as fallback")
	}
	if resp.PathResolutionSource != "roots" {
		t.Fatalf("expected roots source, got %s", resp.PathResolutionSource)
	}
}

func TestResolveFeedbackRecorded(t *testing.T) {
	reg := &fakeRegistry{}
	r := New(Dependencies{Registry: reg})
	req := contract.ResolveWorkspaceRequest{
		WorkspaceRoot: "/tmp/project",
		Feedback: &contract.PathFeedback{
			Status:        "mismatch",
			SuggestedPath: "/tmp/project",
			Reason:        "ide mismatch",
		},
	}

	_, err := r.Resolve(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if reg.feedbackCount != 1 {
		t.Fatalf("expected feedback to be recorded once, got %d", reg.feedbackCount)
	}
	if reg.promoteCount != 0 {
		t.Fatalf("did not expect promotion without execution signal")
	}
}

func TestResolveFeedbackPromotedOnExecutionSignal(t *testing.T) {
	reg := &fakeRegistry{}
	r := New(Dependencies{Registry: reg})
	req := contract.ResolveWorkspaceRequest{
		WorkspaceRoot: "/tmp/project",
		Client:        contract.ClientInfo{Name: "windsurf"},
		Feedback: &contract.PathFeedback{
			Status:             "mismatch",
			SuggestedPath:      "/tmp/project",
			ExecutionSucceeded: true,
		},
	}

	_, err := r.Resolve(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if reg.promoteCount != 1 {
		t.Fatalf("expected one promotion, got %d", reg.promoteCount)
	}
}

func TestResolveRepeatedInvalidPathStable(t *testing.T) {
	r := New(Dependencies{})
	req := contract.ResolveWorkspaceRequest{FilePath: "/definitely/missing/file.go"}

	for i := 0; i < 5; i++ {
		resp, err := r.Resolve(context.Background(), req)
		if err == nil {
			t.Fatalf("expected error on iteration %d", i)
		}
		if err.Code != contract.ErrorNoContext {
			t.Fatalf("expected ErrorNoContext, got %s on iteration %d", err.Code, i)
		}
		if resp != nil {
			t.Fatalf("expected nil response on iteration %d", i)
		}
	}
}

func TestResolveConfidenceDecayFromBranchRisk(t *testing.T) {
	annotator := &fakeAnnotator{branch: "feature", mismatchRisk: "high"}
	r := New(Dependencies{BranchAnnotator: annotator})
	req := contract.ResolveWorkspaceRequest{WorkspaceRoot: "/tmp/project"}

	resp, err := r.Resolve(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.PathResolutionConfidence >= 1.0 {
		t.Fatalf("expected decayed confidence, got %f", resp.PathResolutionConfidence)
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
	candidate     *contract.WorkspaceCandidate
	err           *contract.ResolveWorkspaceError
	feedbackCount int
	promoteCount  int
}

func (f *fakeRegistry) ResolveAlias(ctx context.Context, alias string) (*contract.WorkspaceCandidate, *contract.ResolveWorkspaceError) {
	if f.err != nil {
		return nil, f.err
	}
	return f.candidate, nil
}

func (f *fakeRegistry) RecordFeedback(ctx context.Context, feedback *contract.PathFeedback) error {
	f.feedbackCount++
	return nil
}

func (f *fakeRegistry) PromoteCandidate(ctx context.Context, root, client string, executionSucceeded bool) error {
	if executionSucceeded {
		f.promoteCount++
	}
	return nil
}

func (f *fakeRegistry) GetActiveWorkspace() (string, error) {
	if f.candidate != nil {
		return f.candidate.Root, nil
	}
	return "", nil
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

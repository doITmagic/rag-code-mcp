package tests

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/doITmagic/rag-code-mcp/pkg/workspace/contract"
	"github.com/doITmagic/rag-code-mcp/pkg/workspace/registry"
	"github.com/doITmagic/rag-code-mcp/pkg/workspace/resolver"
)

type ToggleableAnnotator struct {
	Branch       string
	Head         string
	Worktree     string
	Reason       contract.ReasonCode
	Reindex      bool
	MismatchRisk string
}

func (a *ToggleableAnnotator) Annotate(ctx context.Context, root string, resp *contract.ResolveWorkspaceResponse) *contract.ResolveWorkspaceError {
	resp.Branch = a.Branch
	resp.HeadSHA = a.Head
	resp.WorktreeID = a.Worktree
	resp.Reason = a.Reason
	resp.ReindexRequired = a.Reindex
	resp.MismatchRisk = a.MismatchRisk

	return nil
}

func TestBranchIsolationAndFeedbackPromotionIntegration(t *testing.T) {
	ctx := context.Background()
	tmpDir := t.TempDir()

	// 1. Setup real registry
	regPath := filepath.Join(tmpDir, "registry.json")
	reg, err := registry.New(regPath)
	if err != nil {
		t.Fatalf("failed to create registry: %v", err)
	}

	// 2. Setup toggleable annotator
	annotator := &ToggleableAnnotator{
		Branch:   "main",
		Head:     "sha-old",
		Worktree: "wt-1",
		Reason:   contract.ReasonFirstSeen,
	}

	r := resolver.New(resolver.Dependencies{
		Registry:        reg,
		BranchAnnotator: annotator,
	})

	projectRoot := filepath.Clean(filepath.Join(tmpDir, "project"))
	_ = os.MkdirAll(projectRoot, 0755)

	// --- STEP 1: Initial resolution on main ---
	req1 := contract.ResolveWorkspaceRequest{WorkspaceRoot: projectRoot}
	resp1, errRes := r.Resolve(ctx, req1)
	if errRes != nil {
		t.Fatalf("First resolve failed: %v", errRes)
	}

	id1 := resp1.WorkspaceID

	// --- STEP 2: Branch switch to 'feature' ---
	annotator.Branch = "feature"
	annotator.Head = "sha-new"
	annotator.Reason = contract.ReasonBranchChanged
	annotator.MismatchRisk = "high" // Force confidence decay

	req2 := contract.ResolveWorkspaceRequest{WorkspaceRoot: projectRoot}
	resp2, errRes := r.Resolve(ctx, req2)
	if id1 == resp2.WorkspaceID {
		t.Errorf("Workspace IDs should be different for different branches")
	}
	if resp2.Metadata.Confidence >= 1.0 {
		t.Errorf("Confidence should have decayed on high mismatch risk, got %f", resp2.Metadata.Confidence)
	}

	// --- STEP 3: IDE provides feedback ---
	// We use two paths of same depth to ensure ambiguity if they were chosen
	req3 := contract.ResolveWorkspaceRequest{
		Roots: []string{"/a/b/c", "/x/y/z"},
		Feedback: &contract.PathFeedback{
			Mismatch:      false,
			SuggestedPath: projectRoot,
		},
	}
	// projectRoot (/tmp/...) is deeper than /a/b/c, so it will probably win anyway.
	resp3, _ := r.Resolve(ctx, req3)
	if resp3.ResolvedRoot != projectRoot {
		// If projectRoot didn't win, it should be in candidates
		found := false
		for _, c := range resp3.Candidates {
			if c.Root == projectRoot {
				found = true
				break
			}
		}
		if !found {
			t.Error("projectRoot should be resolved or a candidate")
		}
	}

	// --- STEP 4: Confirm that feedback is remembered ---
	// We simulate a request where the IDE suggests projectRoot via feedback
	req4 := contract.ResolveWorkspaceRequest{
		Roots: []string{"/other/path"},
		Feedback: &contract.PathFeedback{
			SuggestedPath: projectRoot,
		},
	}
	resp4, _ := r.Resolve(ctx, req4)
	if !strings.Contains(resp4.ResolvedRoot, "project") && !resp4.RequiresConfirmation {
		t.Error("Feedback should have influenced resolution to pick projectRoot")
	}

	// --- STEP 5: Promotion (Upsert) ---
	if _, err := reg.Upsert(projectRoot, "MyProject", "windsurf"); err != nil {
		t.Fatalf("Upsert failed: %v", err)
	}

	// --- STEP 6: Final check ---
	// Now searching by Name "MyProject" should work perfectly.
	req5 := contract.ResolveWorkspaceRequest{Workspace: "MyProject"}
	resp5, errRes := r.Resolve(ctx, req5)
	if errRes != nil {
		t.Fatalf("Final resolve failed: %v", errRes)
	}
	if resp5.ResolvedRoot != projectRoot {
		t.Errorf("Expected promoted root %s, got %s", projectRoot, resp5.ResolvedRoot)
	}
	if resp5.Metadata.Source != "registry" {
		t.Errorf("Expected source 'registry', got %s", resp5.Metadata.Source)
	}
}

func TestFeedbackCandidatePromotionRequiresExecutionSignal(t *testing.T) {
	ctx := context.Background()
	tmpDir := t.TempDir()

	regPath := filepath.Join(tmpDir, "registry.json")
	reg, err := registry.New(regPath)
	if err != nil {
		t.Fatalf("failed to create registry: %v", err)
	}

	r := resolver.New(resolver.Dependencies{Registry: reg})
	projectRoot := filepath.Clean(filepath.Join(tmpDir, "project"))
	_ = os.MkdirAll(projectRoot, 0o755)

	_, errRes := r.Resolve(ctx, contract.ResolveWorkspaceRequest{
		WorkspaceRoot: projectRoot,
		Feedback: &contract.PathFeedback{
			Status:        "mismatch",
			SuggestedPath: projectRoot,
		},
	})
	if errRes != nil {
		t.Fatalf("resolve without execution signal failed: %v", errRes)
	}

	metrics := reg.MetricsSnapshot()
	if metrics.CandidatesPromoted != 0 {
		t.Fatalf("candidate should not be promoted without execution signal")
	}

	_, errRes = r.Resolve(ctx, contract.ResolveWorkspaceRequest{
		WorkspaceRoot: projectRoot,
		Client:        contract.ClientInfo{Name: "windsurf"},
		Feedback: &contract.PathFeedback{
			Status:             "mismatch",
			SuggestedPath:      projectRoot,
			ExecutionSucceeded: true,
		},
	})
	if errRes != nil {
		t.Fatalf("resolve with execution signal failed: %v", errRes)
	}

	metrics = reg.MetricsSnapshot()
	if metrics.CandidatesPromoted == 0 {
		t.Fatalf("expected candidate promotion after execution signal")
	}
}

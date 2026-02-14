package resolver

import (
	context "context"
	"crypto/sha1"
	"encoding/hex"
	"sort"
	"strings"

	"github.com/doITmagic/rag-code-mcp/v2/internal/contract"
)

// Detector resolves a workspace root from a given file path.
type Detector interface {
	DetectFromFilePath(ctx context.Context, filePath string) (*WorkspaceCandidate, *contract.ResolveWorkspaceError)
}

// Registry resolves workspace aliases and persists confirmations.
type Registry interface {
	ResolveAlias(ctx context.Context, alias string) (*WorkspaceCandidate, *contract.ResolveWorkspaceError)
}

// RootValidator ensures resolved roots are within allowed boundaries.
type RootValidator interface {
	Validate(root string) *contract.ResolveWorkspaceError
}

// BranchAnnotator enriches responses with branch/head metadata.
type BranchAnnotator interface {
	Annotate(ctx context.Context, root string, resp *contract.ResolveWorkspaceResponse) *contract.ResolveWorkspaceError
}

// Logger allows structured logging.
type Logger interface {
	Debug(ctx context.Context, step string, fields map[string]any)
}

// WorkspaceCandidate represents a potential workspace resolution.
type WorkspaceCandidate struct {
	Root    string
	Name    string
	Markers []string
	Reason  contract.ReasonCode
}

// Dependencies bundles resolver collaborators.
type Dependencies struct {
	Detector        Detector
	Registry        Registry
	RootValidator   RootValidator
	BranchAnnotator BranchAnnotator
	Logger          Logger
}

// Resolver orchestrates deterministic workspace detection.
type Resolver struct {
	deps Dependencies
}

// New creates a resolver with the provided dependencies.
func New(deps Dependencies) *Resolver {
	if deps.Logger == nil {
		deps.Logger = noopLogger{}
	}
	return &Resolver{deps: deps}
}

func (r *Resolver) log(ctx context.Context, step string, fields map[string]any) {
	if r.deps.Logger == nil {
		return
	}
	if fields == nil {
		fields = map[string]any{}
	}
	fields["step"] = step
	r.deps.Logger.Debug(ctx, step, fields)
}

// Resolve evaluates the incoming request using the deterministic cascade.
func (r *Resolver) Resolve(ctx context.Context, req contract.ResolveWorkspaceRequest) (*contract.ResolveWorkspaceResponse, *contract.ResolveWorkspaceError) {
	if err := contract.ValidateRequest(req); err != nil {
		return nil, err
	}

	// 1. workspace_root provided explicitly
	if resp, err := r.handleWorkspaceRoot(ctx, strings.TrimSpace(req.WorkspaceRoot)); resp != nil || err != nil {
		return resp, err
	}

	// 2. file_path detection
	if resp, err := r.handleFilePath(ctx, strings.TrimSpace(req.FilePath)); resp != nil || err != nil {
		return resp, err
	}

	// 3. workspace alias via registry
	if resp, err := r.handleWorkspaceAlias(ctx, strings.TrimSpace(req.Workspace)); resp != nil || err != nil {
		return resp, err
	}

	// 4. roots list
	return r.handleRoots(ctx, req)
}

func (r *Resolver) handleWorkspaceRoot(ctx context.Context, root string) (*contract.ResolveWorkspaceResponse, *contract.ResolveWorkspaceError) {
	if root == "" {
		return nil, nil
	}
	r.log(ctx, "workspace_root", map[string]any{"root": root, "source": "workspace_root"})
	candidate := &WorkspaceCandidate{Root: root, Reason: contract.ReasonExplicitWorkspaceRoot}
	return r.finalize(ctx, candidate)
}

func (r *Resolver) handleFilePath(ctx context.Context, path string) (*contract.ResolveWorkspaceResponse, *contract.ResolveWorkspaceError) {
	if path == "" {
		return nil, nil
	}
	if r.deps.Detector == nil {
		return nil, &contract.ResolveWorkspaceError{
			Code:    contract.ErrorNoContext,
			Message: "detector dependency not configured",
			Reason:  contract.ReasonRootsUnavailable,
		}
	}
	result, err := r.deps.Detector.DetectFromFilePath(ctx, path)
	if err != nil {
		return nil, err
	}
	if result == nil || strings.TrimSpace(result.Root) == "" {
		return nil, &contract.ResolveWorkspaceError{
			Code:    contract.ErrorInvalidPath,
			Message: "unable to detect workspace from file_path",
			Reason:  contract.ReasonInvalidPath,
		}
	}
	r.log(ctx, "file_path", map[string]any{"root": result.Root, "source": "file_path"})
	return r.finalize(ctx, result)
}

func (r *Resolver) handleWorkspaceAlias(ctx context.Context, alias string) (*contract.ResolveWorkspaceResponse, *contract.ResolveWorkspaceError) {
	if alias == "" {
		return nil, nil
	}
	if r.deps.Registry == nil {
		return nil, &contract.ResolveWorkspaceError{
			Code:    contract.ErrorAmbiguousWorkspace,
			Message: "registry dependency not configured",
			Reason:  contract.ReasonWorkspaceAlias,
		}
	}
	candidate, err := r.deps.Registry.ResolveAlias(ctx, alias)
	if err != nil {
		return nil, err
	}
	if candidate == nil {
		return nil, &contract.ResolveWorkspaceError{
			Code:    contract.ErrorAmbiguousWorkspace,
			Message: "workspace alias not found",
			Reason:  contract.ReasonWorkspaceAlias,
		}
	}
	if candidate.Reason == "" {
		candidate.Reason = contract.ReasonWorkspaceAlias
	}
	r.log(ctx, "workspace_alias", map[string]any{"alias": alias, "root": candidate.Root, "source": "workspace_alias"})
	return r.finalize(ctx, candidate)
}

func (r *Resolver) handleRoots(ctx context.Context, req contract.ResolveWorkspaceRequest) (*contract.ResolveWorkspaceResponse, *contract.ResolveWorkspaceError) {
	var roots []string
	for _, root := range req.Roots {
		if trimmed := strings.TrimSpace(root); trimmed != "" {
			roots = append(roots, trimmed)
		}
	}
	if len(roots) == 0 {
		return nil, &contract.ResolveWorkspaceError{
			Code:    contract.ErrorNoContext,
			Message: "roots context unavailable",
			Reason:  contract.ReasonRootsUnavailable,
		}
	}

	if len(roots) == 1 {
		candidate := &WorkspaceCandidate{Root: roots[0], Reason: contract.ReasonRootsList}
		r.log(ctx, "roots_single", map[string]any{"root": candidate.Root, "source": "roots"})
		return r.finalize(ctx, candidate)
	}

	if best, ok := selectBestRoot(roots); ok {
		candidate := &WorkspaceCandidate{Root: best, Reason: contract.ReasonRootsList}
		r.log(ctx, "roots_scored", map[string]any{"root": candidate.Root, "source": "roots", "strategy": "depth"})
		return r.finalize(ctx, candidate)
	}

	// Multiple candidates → require confirmation
	candidates := make([]contract.Candidate, 0, len(roots))
	for _, root := range roots {
		candidates = append(candidates, contract.Candidate{
			Root:   root,
			Name:   root,
			Reason: "resolved from roots list",
		})
	}
	sort.Slice(candidates, func(i, j int) bool { return candidates[i].Root < candidates[j].Root })

	err := &contract.ResolveWorkspaceError{
		Code:    contract.ErrorAmbiguousWorkspace,
		Message: "multiple roots provided; confirmation required",
		Reason:  contract.ReasonConfirmationRequired,
	}

	if req.StrictMode {
		return nil, err
	}

	r.log(ctx, "roots_ambiguous", map[string]any{"candidates": len(candidates), "strict_mode": req.StrictMode})
	return &contract.ResolveWorkspaceResponse{
		RequiresConfirmation: true,
		Reason:               contract.ReasonConfirmationRequired,
		Candidates:           candidates,
	}, nil
}

func selectBestRoot(roots []string) (string, bool) {
	if len(roots) == 0 {
		return "", false
	}
	bestIndex := -1
	bestScore := -1
	duplicate := false
	scores := make([]int, len(roots))
	for i, root := range roots {
		score := len(root) + strings.Count(root, string('/'))
		scores[i] = score
		if score > bestScore {
			bestScore = score
			bestIndex = i
			duplicate = false
		} else if score == bestScore {
			duplicate = true
		}
	}
	if bestIndex == -1 || duplicate {
		return "", false
	}
	return roots[bestIndex], true
}

func (r *Resolver) finalize(ctx context.Context, candidate *WorkspaceCandidate) (*contract.ResolveWorkspaceResponse, *contract.ResolveWorkspaceError) {
	if candidate == nil || strings.TrimSpace(candidate.Root) == "" {
		return nil, &contract.ResolveWorkspaceError{
			Code:    contract.ErrorInvalidPath,
			Message: "resolved workspace root is empty",
			Reason:  contract.ReasonInvalidPath,
		}
	}
	if r.deps.RootValidator != nil {
		if err := r.deps.RootValidator.Validate(candidate.Root); err != nil {
			return nil, err
		}
	}
	resp := &contract.ResolveWorkspaceResponse{
		ResolvedRoot: candidate.Root,
		WorkspaceID:  deriveWorkspaceID(candidate.Root),
		MarkersFound: candidate.Markers,
		Reason:       candidate.Reason,
	}
	if r.deps.BranchAnnotator != nil {
		if err := r.deps.BranchAnnotator.Annotate(ctx, candidate.Root, resp); err != nil {
			return nil, err
		}
	}
	return resp, nil
}

func deriveWorkspaceID(root string) string {
	digest := sha1.Sum([]byte(root))
	return hex.EncodeToString(digest[:])
}

type noopLogger struct{}

func (noopLogger) Debug(ctx context.Context, step string, fields map[string]any) {}

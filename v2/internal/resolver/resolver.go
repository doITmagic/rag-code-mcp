package resolver

import (
	context "context"
	"path/filepath"
	"sort"
	"strings"

	"github.com/doITmagic/rag-code-mcp/v2/internal/contract"
)

// Detector resolves a workspace root from a given file path.
type Detector interface {
	DetectFromFilePath(ctx context.Context, filePath string) (*contract.WorkspaceCandidate, *contract.ResolveWorkspaceError)
}

// Registry resolves workspace aliases and persists confirmations.
type Registry interface {
	ResolveAlias(ctx context.Context, alias string) (*contract.WorkspaceCandidate, *contract.ResolveWorkspaceError)
	RecordFeedback(ctx context.Context, feedback *contract.PathFeedback) error
	PromoteCandidate(ctx context.Context, root, client string, executionSucceeded bool) error
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

// WorkspaceCandidate is kept as a compatibility alias during migration.
type WorkspaceCandidate = contract.WorkspaceCandidate

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
	r.recordFeedback(ctx, req.Feedback)

	// 1. workspace_root provided explicitly
	if resp, err := r.handleWorkspaceRoot(ctx, strings.TrimSpace(req.WorkspaceRoot)); resp != nil || err != nil {
		if err == nil {
			r.promoteFeedback(ctx, req, resp)
		}
		return resp, err
	}

	// 2. file_path detection
	if resp, err := r.handleFilePath(ctx, strings.TrimSpace(req.FilePath)); resp != nil || err != nil {
		if err == nil {
			r.promoteFeedback(ctx, req, resp)
		}
		return resp, err
	}

	// 3. workspace alias via registry
	if resp, err := r.handleWorkspaceAlias(ctx, strings.TrimSpace(req.Workspace)); resp != nil || err != nil {
		if err == nil {
			r.promoteFeedback(ctx, req, resp)
		}
		return resp, err
	}

	// 4. roots list
	resp, err := r.handleRoots(ctx, req)
	if err == nil {
		r.promoteFeedback(ctx, req, resp)
	}
	return resp, err
}

func (r *Resolver) handleWorkspaceRoot(ctx context.Context, root string) (*contract.ResolveWorkspaceResponse, *contract.ResolveWorkspaceError) {
	if root == "" {
		return nil, nil
	}
	r.log(ctx, "workspace_root", map[string]any{"root": root, "source": "workspace_root"})
	candidate := &contract.WorkspaceCandidate{Root: root, Reason: contract.ReasonExplicitWorkspaceRoot, Source: "workspace_root", Confidence: 1.0}
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
	if result.Source == "" {
		result.Source = "file_path"
	}
	if result.Confidence == 0 {
		result.Confidence = 0.95
	}
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
	if candidate.Source == "" {
		candidate.Source = "registry"
	}
	if candidate.Confidence == 0 {
		candidate.Confidence = 0.9
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
	if req.Feedback != nil {
		if suggested := strings.TrimSpace(req.Feedback.SuggestedPath); suggested != "" {
			roots = append(roots, suggested)
		}
	}
	roots = uniqueRoots(roots)
	if len(roots) == 0 {
		return nil, &contract.ResolveWorkspaceError{
			Code:    contract.ErrorNoContext,
			Message: "roots context unavailable",
			Reason:  contract.ReasonRootsUnavailable,
		}
	}

	if len(roots) == 1 {
		candidate := &contract.WorkspaceCandidate{Root: roots[0], Reason: contract.ReasonRootsList, Source: "roots", Confidence: 0.8}
		r.log(ctx, "roots_single", map[string]any{"root": candidate.Root, "source": "roots"})
		return r.finalize(ctx, candidate)
	}

	if best, ok := selectBestRoot(roots); ok {
		candidate := &contract.WorkspaceCandidate{Root: best, Reason: contract.ReasonRootsList, Source: "roots", Confidence: 0.75}
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

func (r *Resolver) finalize(ctx context.Context, candidate *contract.WorkspaceCandidate) (*contract.ResolveWorkspaceResponse, *contract.ResolveWorkspaceError) {
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
	if candidate.Source == "" {
		candidate.Source = sourceFromReason(candidate.Reason)
	}
	if candidate.Confidence == 0 {
		candidate.Confidence = defaultConfidence(candidate.Source)
	}
	resp := &contract.ResolveWorkspaceResponse{
		ResolvedRoot: candidate.Root,
		MarkersFound: candidate.Markers,
		Reason:       candidate.Reason,
		Metadata: contract.ResponseMetadata{
			Source:        candidate.Source,
			Confidence:    candidate.Confidence,
			UsedFallback:  isFallbackSource(candidate.Source),
			WorkspaceRoot: candidate.Root,
		},
	}
	if r.deps.BranchAnnotator != nil {
		if err := r.deps.BranchAnnotator.Annotate(ctx, candidate.Root, resp); err != nil {
			return nil, err
		}
	}
	resp.Metadata.Confidence = adjustConfidence(resp.Metadata.Confidence, resp.MismatchRisk)
	resp.Metadata.GitBranch = resp.Branch
	resp.Metadata.GitHead = resp.HeadSHA
	resp.Metadata.WorktreeID = resp.WorktreeID
	resp.Metadata.PathContextKey = contract.DerivePathContextKey(resp.ResolvedRoot, resp.Branch, resp.HeadSHA, resp.WorktreeID)
	resp.PathResolutionSource = resp.Metadata.Source
	resp.PathResolutionConfidence = resp.Metadata.Confidence
	resp.UsedFallback = resp.Metadata.UsedFallback
	resp.WorkspaceRoot = resp.Metadata.WorkspaceRoot
	resp.GitBranch = resp.Metadata.GitBranch
	resp.GitHead = resp.Metadata.GitHead
	resp.PathContextKey = resp.Metadata.PathContextKey
	resp.WorkspaceID = contract.DeriveWorkspaceID(resp.ResolvedRoot, resp.Branch, resp.WorktreeID)
	return resp, nil
}

func sourceFromReason(reason contract.ReasonCode) string {
	switch reason {
	case contract.ReasonExplicitWorkspaceRoot:
		return "workspace_root"
	case contract.ReasonFilePath:
		return "file_path"
	case contract.ReasonWorkspaceAlias:
		return "registry"
	case contract.ReasonRootsList:
		return "roots"
	default:
		return "resolver"
	}
}

func defaultConfidence(source string) float64 {
	switch source {
	case "workspace_root":
		return 1.0
	case "file_path":
		return 0.95
	case "registry":
		return 0.9
	case "roots":
		return 0.75
	default:
		return 0.8
	}
}

func isFallbackSource(source string) bool {
	switch strings.ToLower(strings.TrimSpace(source)) {
	case "roots", "registry", "metadata", "resolver":
		return true
	default:
		return false
	}
}

func adjustConfidence(confidence float64, mismatchRisk string) float64 {
	switch strings.ToLower(strings.TrimSpace(mismatchRisk)) {
	case "high":
		confidence *= 0.6
	case "medium":
		confidence *= 0.8
	}
	if confidence < 0 {
		return 0
	}
	if confidence > 1 {
		return 1
	}
	return confidence
}

func uniqueRoots(roots []string) []string {
	if len(roots) == 0 {
		return roots
	}
	seen := make(map[string]struct{}, len(roots))
	result := make([]string, 0, len(roots))
	for _, root := range roots {
		normalized := strings.ToLower(filepath.Clean(strings.TrimSpace(root)))
		if normalized == "" {
			continue
		}
		if _, ok := seen[normalized]; ok {
			continue
		}
		seen[normalized] = struct{}{}
		result = append(result, filepath.Clean(strings.TrimSpace(root)))
	}
	return result
}

func (r *Resolver) recordFeedback(ctx context.Context, feedback *contract.PathFeedback) {
	if r.deps.Registry == nil || feedback == nil {
		return
	}
	if err := r.deps.Registry.RecordFeedback(ctx, feedback); err != nil {
		r.log(ctx, "feedback_record_failed", map[string]any{"error": err.Error()})
	}
}

func (r *Resolver) promoteFeedback(ctx context.Context, req contract.ResolveWorkspaceRequest, resp *contract.ResolveWorkspaceResponse) {
	if r.deps.Registry == nil || req.Feedback == nil || resp == nil {
		return
	}
	if !req.Feedback.ExecutionSucceeded {
		return
	}
	suggested := strings.TrimSpace(req.Feedback.SuggestedPath)
	if suggested == "" || strings.TrimSpace(resp.ResolvedRoot) == "" {
		return
	}
	if !samePath(suggested, resp.ResolvedRoot) {
		return
	}
	if err := r.deps.Registry.PromoteCandidate(ctx, resp.ResolvedRoot, strings.TrimSpace(req.Client.Name), true); err != nil {
		r.log(ctx, "feedback_promote_failed", map[string]any{"error": err.Error()})
	}
}

func samePath(left, right string) bool {
	leftClean := strings.ToLower(filepath.Clean(strings.TrimSpace(left)))
	rightClean := strings.ToLower(filepath.Clean(strings.TrimSpace(right)))
	return leftClean != "" && leftClean == rightClean
}

type noopLogger struct{}

func (noopLogger) Debug(ctx context.Context, step string, fields map[string]any) {}

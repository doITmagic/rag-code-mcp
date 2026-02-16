package contract

import "strings"

// ClientInfo captures identifying metadata about the MCP client/IDE.
type ClientInfo struct {
	Name    string `json:"name,omitempty"`
	Version string `json:"version,omitempty"`
}

// ClientCapabilities describes optional features exposed by the client.
type ClientCapabilities struct {
	SupportsRoots      bool `json:"supports_roots"`
	RootsListChanged   bool `json:"roots_list_changed"`
	SupportsStrictMode bool `json:"supports_strict_mode"`
}

// ContractVersion tracks breaking changes in resolver contracts.
const ContractVersion = "v2"

// PathFeedback allows the IDE to provide corrections or suggestions to the resolver.
type PathFeedback struct {
	Mismatch      bool   `json:"mismatch"`
	SuggestedPath string `json:"suggested_path,omitempty"`
	Reason        string `json:"reason,omitempty"`
}

// ResolveWorkspaceRequest is the canonical resolver input shared by all tools.
type ResolveWorkspaceRequest struct {
	WorkspaceRoot string             `json:"workspace_root,omitempty"`
	FilePath      string             `json:"file_path,omitempty"`
	Workspace     string             `json:"workspace,omitempty"`
	Roots         []string           `json:"roots,omitempty"`
	Client        ClientInfo         `json:"client,omitempty"`
	Capabilities  ClientCapabilities `json:"capabilities"`
	StrictMode    bool               `json:"strict_mode"`
	Feedback      *PathFeedback      `json:"feedback,omitempty"`
}

// ReasonCode explains how the resolver reached a decision or why it failed.
type ReasonCode string

const (
	ReasonExplicitWorkspaceRoot ReasonCode = "EXPLICIT_WORKSPACE_ROOT"
	ReasonFilePath              ReasonCode = "FILE_PATH"
	ReasonWorkspaceAlias        ReasonCode = "WORKSPACE_ALIAS"
	ReasonRootsList             ReasonCode = "ROOTS_LIST"
	ReasonRootsUnavailable      ReasonCode = "ROOTS_UNAVAILABLE"
	ReasonConfirmationRequired  ReasonCode = "CONFIRMATION_REQUIRED"
	ReasonRegistryFallback      ReasonCode = "REGISTRY_FALLBACK"
	ReasonAmbiguousContext      ReasonCode = "AMBIGUOUS_CONTEXT"
	ReasonNoContext             ReasonCode = "NO_CONTEXT"
	ReasonInvalidPath           ReasonCode = "INVALID_PATH"
	ReasonOutsideAllowedRoots   ReasonCode = "OUTSIDE_ALLOWED_ROOTS"
	ReasonBranchChanged         ReasonCode = "BRANCH_CHANGED"
	ReasonHeadChanged           ReasonCode = "HEAD_CHANGED"
	ReasonFirstSeen             ReasonCode = "FIRST_SEEN"
)

// ErrorCode enumerates resolver failures surfaced to clients.
type ErrorCode string

const (
	ErrorNoContext           ErrorCode = "NO_CONTEXT"
	ErrorAmbiguousWorkspace  ErrorCode = "AMBIGUOUS_WORKSPACE"
	ErrorInvalidPath         ErrorCode = "INVALID_PATH"
	ErrorOutsideAllowedRoots ErrorCode = "OUTSIDE_ALLOWED_ROOTS"
)

// ResolveWorkspaceError is an actionable error payload for IDE clients.
type ResolveWorkspaceError struct {
	Code    ErrorCode      `json:"code"`
	Message string         `json:"message"`
	Details map[string]any `json:"details,omitempty"`
	Reason  ReasonCode     `json:"reason,omitempty"`
}

// Candidate describes a workspace option that requires confirmation.
type Candidate struct {
	Root    string   `json:"root"`
	Name    string   `json:"name,omitempty"`
	Markers []string `json:"markers,omitempty"`
	Reason  string   `json:"reason,omitempty"`
}

// WorkspaceCandidate represents a potential workspace resolution during internal processing.
type WorkspaceCandidate struct {
	Root       string
	Name       string
	Markers    []string
	Reason     ReasonCode
	Confidence float64
	Source     string
}

// ResponseMetadata captures diagnostics and resolution quality metrics.
type ResponseMetadata struct {
	Confidence   float64 `json:"confidence"`
	Source       string  `json:"source,omitempty"`
	UsedFallback bool    `json:"used_fallback"`
}

// ResolveWorkspaceResponse represents the resolver output for success cases.
type ResolveWorkspaceResponse struct {
	ResolvedRoot         string           `json:"resolved_root,omitempty"`
	WorkspaceID          string           `json:"workspace_id,omitempty"`
	MarkersFound         []string         `json:"markers_found,omitempty"`
	Branch               string           `json:"branch,omitempty"`
	HeadSHA              string           `json:"head_sha,omitempty"`
	WorktreeID           string           `json:"worktree_id,omitempty"`
	MismatchRisk         string           `json:"mismatch_risk,omitempty"` // low|medium|high
	ReindexRequired      bool             `json:"reindex_required"`
	Metadata             ResponseMetadata `json:"metadata"`
	Reason               ReasonCode       `json:"reason,omitempty"`
	RequiresConfirmation bool             `json:"requires_confirmation"`
	Candidates           []Candidate      `json:"candidates,omitempty"`
}

// hasValue returns true when the string contains non-whitespace characters.
func hasValue(v string) bool {
	return strings.TrimSpace(v) != ""
}

// ValidateRequest performs lightweight validation before resolver processing.
func ValidateRequest(req ResolveWorkspaceRequest) *ResolveWorkspaceError {
	if hasValue(req.WorkspaceRoot) || hasValue(req.FilePath) || hasValue(req.Workspace) {
		return nil
	}
	for _, root := range req.Roots {
		if hasValue(root) {
			return nil
		}
	}
	return &ResolveWorkspaceError{
		Code:    ErrorNoContext,
		Message: "provide workspace_root, file_path, workspace alias, or client roots",
		Reason:  ReasonNoContext,
	}
}

package contract

import "testing"

func TestValidateRequest(t *testing.T) {
	tests := []struct {
		name        string
		request     ResolveWorkspaceRequest
		wantErr     bool
		wantErrCode ErrorCode
	}{
		{
			name:    "with workspace_root",
			request: ResolveWorkspaceRequest{WorkspaceRoot: "/tmp/project"},
			wantErr: false,
		},
		{
			name:    "with file_path",
			request: ResolveWorkspaceRequest{FilePath: "/tmp/project/main.go"},
			wantErr: false,
		},
		{
			name:    "with workspace alias",
			request: ResolveWorkspaceRequest{Workspace: "project-a"},
			wantErr: false,
		},
		{
			name:        "file_path whitespace only treated as empty",
			request:     ResolveWorkspaceRequest{FilePath: "   "},
			wantErr:     true,
			wantErrCode: ErrorNoContext,
		},
		{
			name:    "with roots list",
			request: ResolveWorkspaceRequest{Roots: []string{"/tmp/project"}},
			wantErr: false,
		},
		{
			name:        "roots slice with only whitespace should fail",
			request:     ResolveWorkspaceRequest{Roots: []string{"   ", ""}},
			wantErr:     true,
			wantErrCode: ErrorNoContext,
		},
		{
			name:    "roots slice with mix of blank and real entries succeeds",
			request: ResolveWorkspaceRequest{Roots: []string{"", " /tmp/app "}},
			wantErr: false,
		},
		{
			name:        "missing context returns error",
			request:     ResolveWorkspaceRequest{},
			wantErr:     true,
			wantErrCode: ErrorNoContext,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateRequest(tt.request)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error, got nil")
				}
				if err.Code != tt.wantErrCode {
					t.Fatalf("expected error code %s, got %s", tt.wantErrCode, err.Code)
				}
				if err.Reason != ReasonNoContext {
					t.Fatalf("expected reason %s, got %s", ReasonNoContext, err.Reason)
				}
			} else if err != nil {
				t.Fatalf("expected no error, got %v", err)
			}
		})
	}
}

func TestReasonCodesCoverage(t *testing.T) {
	// Ensure new reason codes have non-empty values and no accidental duplicates.
	reasonSet := map[ReasonCode]bool{}
	reasons := []ReasonCode{
		ReasonExplicitWorkspaceRoot,
		ReasonFilePath,
		ReasonWorkspaceAlias,
		ReasonRootsList,
		ReasonRootsUnavailable,
		ReasonConfirmationRequired,
		ReasonRegistryFallback,
		ReasonAmbiguousContext,
		ReasonNoContext,
		ReasonInvalidPath,
		ReasonOutsideAllowedRoots,
		ReasonBranchChanged,
		ReasonHeadChanged,
		ReasonFirstSeen,
	}

	for _, reason := range reasons {
		if reason == "" {
			t.Fatalf("reason code must not be empty")
		}
		if reasonSet[reason] {
			t.Fatalf("duplicate reason code detected: %s", reason)
		}
		reasonSet[reason] = true
	}
}

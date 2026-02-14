# Contract Module - Tasks

## Goal
Define stable, deterministic request/response contracts for workspace resolution.

## Task 1: Request contract
### Subtasks
- 1.1 Define fields: `workspace_root`, `file_path`, `workspace`, `roots`, `client_capabilities`.
- 1.2 Define precedence and validation rules per field.
- 1.3 Define schema constraints (required vs optional, path format expectations).

## Task 2: Response contract
### Subtasks
- 2.1 Define canonical outputs: `resolved_root`, `workspace_id`, `markers_found`.
- 2.2 Define branch outputs: `branch`, `head_sha`, `reindex_required`, `reason`.
- 2.3 Define ambiguity outputs: `requires_confirmation`, `candidates`.
- 2.4 Add path-resolution metadata fields: `resolved_file_path`, `path_resolution_source`, `path_resolution_confidence`, `used_fallback`.
- 2.5 Add context metadata fields: `workspace_root`, `git_branch`, `git_head`, `worktree_id`, `path_context_key`.
- 2.6 Add risk metadata field: `branch_mismatch_risk` (`low|medium|high`).

## Task 3: Error contract
### Subtasks
- 3.1 Define deterministic error codes (`NO_CONTEXT`, `AMBIGUOUS_WORKSPACE`, `INVALID_PATH`, `OUTSIDE_ALLOWED_ROOTS`).
- 3.2 Define actionable error messages with remediation hints.
- 3.3 Define machine-readable error metadata for IDE clients.

## Task 4: Contract tests
### Subtasks
- 4.1 Add schema validation tests for request and response payloads.
- 4.2 Add error contract stability tests.
- 4.3 Add backward-compatibility tests for future contract evolution.

## Task 5: Feedback contract (Issue #21)
### Subtasks
- 5.1 Add request-side feedback object: `path_feedback.status`, `path_feedback.suggested_file_path`, `path_feedback.reason`.
- 5.2 Validate accepted statuses (at minimum: `mismatch`) and required constraints for suggested paths.
- 5.3 Document promotion rule: suggested path becomes trusted only after successful resolution/execution.
- 5.4 Add contract tests for valid/invalid feedback payloads.

## Implementation Checklist by Phase

### Phase 1 - Contract expansion for resolver metadata
- [ ] **[P0]** Add response fields: `path_resolution_source`, `path_resolution_confidence`, `used_fallback`.
- [ ] **[P0]** Add context fields: `path_context_key`, `worktree_id`, `git_branch`, `git_head`.
- [ ] **[P0]** Add risk field: `branch_mismatch_risk` enum (`low|medium|high`).
- [ ] **[P1]** Add backward-compatible defaults for clients not yet reading new metadata fields.

### Phase 2 - Feedback contract and validation
- [ ] **[P0]** Add `path_feedback` object to request contract.
- [ ] **[P0]** Validate `path_feedback.status=mismatch` and suggested-path constraints.
- [ ] **[P1]** Add exhaustive contract tests for invalid feedback combinations.

# Tests Module - Tasks

## Goal
Create AI-like end-to-end test coverage for deterministic detection behavior.

## Task 1: Table-driven scenario matrix
### Subtasks
- 1.1 Build scenario table for request combinations (`workspace_root`, `file_path`, alias, roots).
- 1.2 Build expected outcome table (resolved/ambiguous/fail-fast).
- 1.3 Build expected reason-code table for each scenario.

## Task 2: AI-client simulation tests
### Subtasks
- 2.1 Simulate missing `file_path` from IDE agents.
- 2.2 Simulate clients with roots support and without roots support.
- 2.3 Simulate repeated calls with stale/updated registry context.

## Task 3: Branch-aware behavior tests
### Subtasks
- 3.1 Validate `reindex_required` on branch and HEAD transitions.
- 3.2 Validate unchanged state returns `reindex_required=false`.
- 3.3 Validate warning messages and deterministic reason values.

## Task 4: Regression and robustness suite
### Subtasks
- 4.1 Add regression tests for previously observed path-detection failures.
- 4.2 Add stress tests with multiple repositories and ambiguous roots.
- 4.3 Add snapshot tests for stable error payloads and contract outputs.

## Task 5: Response metadata coverage (Issue #21)
### Subtasks
- 5.1 Assert `path_resolution_source` for each resolution path (`workspace_root`, `file_path`, alias, roots).
- 5.2 Assert `path_resolution_confidence` is deterministic per source and invalidation state.
- 5.3 Assert `used_fallback` toggles correctly in fallback paths.
- 5.4 Assert `path_context_key` and branch metadata are present and stable.

## Task 6: Invalidation and anti-loop tests
### Subtasks
- 6.1 Validate cache isolation across branch switches and worktree contexts.
- 6.2 Validate confidence decay on HEAD mismatch/rewrite.
- 6.3 Validate fallback TTL behavior and stale-entry expiration.
- 6.4 Validate no infinite retry loops on repeated missing-path resolution.

## Task 7: Feedback-loop tests
### Subtasks
- 7.1 Validate `path_feedback.status=mismatch` updates candidate pool.
- 7.2 Validate suggested path is not promoted before successful execution.
- 7.3 Validate promotion occurs only after successful resolution + execution signal.

## Task 8: Branch mismatch risk tests
### Subtasks
- 8.1 Validate `branch_mismatch_risk=low` for expected branch/head matches.
- 8.2 Validate `branch_mismatch_risk=medium` for same-branch/head-changed states.
- 8.3 Validate `branch_mismatch_risk=high` for branch mismatch or fallback-only confidence.

## Implementation Checklist by Phase

### Phase 1 - Metadata, invalidation, and safety coverage
- [x] **[P0]** Add assertions for `path_resolution_source` across all decision paths.
- [x] **[P0]** Add assertions for `path_resolution_confidence` stability.
- [x] **[P0]** Add assertions for `used_fallback` correctness.
- [x] **[P0]** Add assertions for `path_context_key` and branch metadata.
- [x] **[P0]** Add anti-loop tests for repeated invalid/missing path retries.
- [x] **[P1]** Add TTL/decay tests for fallback and HEAD rewrite behavior.

### Phase 2 - Feedback and promotion flow coverage
- [x] **[P0]** Add tests for `path_feedback.status=mismatch` ingestion.
- [x] **[P0]** Add tests proving no promotion before successful execution.
- [x] **[P0]** Add tests proving promotion only after successful resolution + execution.
- [x] **[P1]** Add tests for feedback audit signals/metrics emission.

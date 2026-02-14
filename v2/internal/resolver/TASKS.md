# Resolver Module - Tasks

## Goal
Implement a single deterministic decision engine for workspace resolution.

## Task 1: Decision pipeline
### Subtasks
- 1.1 Implement ordered resolution: `workspace_root` -> `file_path` -> alias -> roots.
- 1.2 Implement candidate scoring only when deterministic ties are impossible.
- 1.3 Return explicit `reason` for every decision path.

## Task 2: Ambiguity handling
### Subtasks
- 2.1 Detect multi-candidate situations deterministically.
- 2.2 Return `requires_confirmation=true` with candidate list.
- 2.3 Block implicit selection when strict mode is enabled.

## Task 3: Fail-fast policy
### Subtasks
- 3.1 Return `NO_CONTEXT` when no valid source is available.
- 3.2 Return `INVALID_PATH` for malformed/non-existent user-provided path.
- 3.3 Return `OUTSIDE_ALLOWED_ROOTS` for boundary violations.

## Task 4: Resolver integration points
### Subtasks
- 4.1 Expose a single API callable by all tools.
- 4.2 Ensure idempotent behavior for identical inputs.
- 4.3 Add structured logs for each decision step.

## Task 5: Branch-aware context key (Issue #21)
### Subtasks
- 5.1 Compute `path_context_key` as `workspace_root + git_branch + git_head (+ worktree_id)`.
- 5.2 Ensure context isolation to prevent cross-branch/worktree cache collisions.
- 5.3 Keep temporary backward-compatible fallback only during migration rollout.

## Task 6: Resolution metadata envelope
### Subtasks
- 6.1 Populate `path_resolution_source` for every success path.
- 6.2 Populate `path_resolution_confidence` with deterministic heuristics per source.
- 6.3 Populate `used_fallback` when non-primary resolution paths are used.
- 6.4 Return `resolved_file_path` when file-level resolution is available.

## Task 7: Invalidation and anti-loop hardening
### Subtasks
- 7.1 Invalidate or isolate cached context on branch switch.
- 7.2 Apply confidence decay on HEAD rewrites/mismatch.
- 7.3 Introduce short TTL for fallback-derived cache entries.
- 7.4 Prevent infinite retries on missing/invalid paths.

## Task 8: AI feedback loop
### Subtasks
- 8.1 Accept `path_feedback` input (`mismatch`, suggested path, optional reason).
- 8.2 Store suggested paths as candidates without immediate promotion.
- 8.3 Promote candidates to trusted state only after successful resolution/execution.

## Task 9: Branch mismatch risk
### Subtasks
- 9.1 Compute and return `branch_mismatch_risk` (`low|medium|high`).
- 9.2 Raise risk for branch mismatch or fallback-only confidence.
- 9.3 Keep risk computation deterministic and testable.

## Implementation Checklist by Phase

### Phase 1 - Deterministic metadata and invalidation
- [ ] **[P0]** Build `path_context_key` from `workspace_root + branch + head (+ worktree_id)`.
- [ ] **[P0]** Populate `path_resolution_source` on every successful decision path.
- [ ] **[P0]** Populate `path_resolution_confidence` with deterministic per-source heuristics.
- [ ] **[P0]** Populate `used_fallback` only on non-primary resolution paths.
- [ ] **[P0]** Return deterministic `branch_mismatch_risk` (`low|medium|high`).
- [ ] **[P1]** Apply confidence decay and branch/head invalidation policy hardening.

### Phase 2 - Feedback-driven correction flow
- [ ] **[P0]** Accept `path_feedback` input and store suggested paths as candidates.
- [ ] **[P0]** Prevent immediate promotion of suggested path to trusted context.
- [ ] **[P0]** Promote suggested path only after successful resolution + execution.
- [ ] **[P1]** Add metrics/log counters for mismatch feedback and promotions.

# V2 - Delivery Plan

## Goal
Deliver a modular V2 detection core that is deterministic, testable, and safe for IDE-driven MCP usage.

## Task 1: Define V2 acceptance criteria
### Subtasks
- 1.1 Define success metrics (deterministic resolution rate, ambiguity handling, fail-fast quality).
- 1.2 Define non-goals for V2 (no embedding/search engine rewrite in this phase).
- 1.3 Define rollout gates for integration into existing tools.

## Task 2: Build module-by-module roadmap
### Subtasks
- 2.1 Lock contracts for `contract`, `resolver`, `detector`, `branchstate`, `registry`.
- 2.2 Sequence implementation with tests-first milestones.
- 2.3 Assign ownership and review checkpoints per module.

## Task 3: Integration and migration strategy
### Subtasks
- 3.1 Add feature flag for gradual adoption in production tools.
- 3.2 Migrate tools to single resolver path in controlled batches.
- 3.3 Remove legacy fallback behavior once parity is confirmed.

## Task 4: Quality and release
### Subtasks
- 4.1 Validate behavior with AI-like end-to-end scenarios.
- 4.2 Add observability (reason codes, fallback counters, ambiguity counters).
- 4.3 Create release checklist and rollback plan.

## Task 5: Issue #21 hardening (Path Resolver V2)
### Subtasks
- 5.1 Add branch-aware context key (`workspace_root + branch + head + worktree_id`).
- 5.2 Add response metadata envelope in all relevant tool responses.
- 5.3 Add `branch_mismatch_risk` computation (`low|medium|high`).
- 5.4 Add deterministic confidence and fallback semantics.

## Task 6: Feedback and promotion workflow
### Subtasks
- 6.1 Add request-side `path_feedback` schema (`mismatch`, suggested path, optional reason).
- 6.2 Store suggested paths as candidates, not as trusted context.
- 6.3 Promote candidate paths only after successful resolution and execution.
- 6.4 Add audit logs for feedback ingestion and promotion events.

## Implementation Checklist (Phase 1 / Phase 2)

### Phase 1 - Core metadata and deterministic safety
- [x] **[P0]** Implement branch-aware `path_context_key` (`workspace_root + branch + head + worktree_id`).
- [x] **[P0]** Add response metadata envelope (`path_resolution_source`, `path_resolution_confidence`, `used_fallback`).
- [x] **[P0]** Add `branch_mismatch_risk` computation (`low|medium|high`) with deterministic rules.
- [x] **[P0]** Add invalidation hardening (branch isolation + anti-loop behavior on missing paths).
- [x] **[P1]** Add confidence decay policy on HEAD mismatch/rewrite.

### Phase 2 - Feedback loop and promotion workflow
- [x] **[P0]** Add request-side `path_feedback` contract and validation.
- [x] **[P0]** Persist suggestions as non-trusted candidates.
- [x] **[P0]** Promote candidate paths only after successful resolution + execution.
- [ ] **[P1]** Add audit logs and metrics for feedback ingestion/promotion.

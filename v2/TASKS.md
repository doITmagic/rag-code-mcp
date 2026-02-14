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

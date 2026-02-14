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

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

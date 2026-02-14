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

# Internal Layer - Cross-Module Tasks

## Goal
Provide stable shared behavior across all V2 internal modules.

## Task 1: Shared conventions
### Subtasks
- 1.1 Define naming conventions for reason codes and error codes.
- 1.2 Define logging structure for traceability (`resolver_step`, `decision`, `source`).
- 1.3 Define JSON schema versioning for persisted files.

## Task 2: Dependency boundaries
### Subtasks
- 2.1 Keep `resolver` as the only orchestrator entry point.
- 2.2 Ensure `detector`, `branchstate`, and `registry` are dependency-light utilities.
- 2.3 Prevent tool-specific logic from leaking into core modules.

## Task 3: Test harness standardization
### Subtasks
- 3.1 Create shared fixtures for fake workspaces and fake git states.
- 3.2 Create helpers for roots/no-roots client simulations.
- 3.3 Provide reusable assertion helpers for deterministic outcomes.

## Task 4: Backward compatibility controls
### Subtasks
- 4.1 Add compatibility mode for clients without roots support.
- 4.2 Add strict mode for fail-fast-only behavior.
- 4.3 Add migration hooks for old state/registry formats.

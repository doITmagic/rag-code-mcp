# Detector Module - Tasks

## Goal
Implement robust root detection using markers and secure path validation.

## Task 1: Marker-based root detection
### Subtasks
- 1.1 Implement upward directory traversal from input path.
- 1.2 Evaluate marker presence (`.git`, `go.mod`, `.ragcode`, `.agent`, etc.).
- 1.3 Return marker evidence in a machine-readable structure.

## Task 2: Security validation
### Subtasks
- 2.1 Canonicalize all candidate paths.
- 2.2 Enforce allowed roots/path boundaries.
- 2.3 Reject traversal and symlink-escape scenarios.

## Task 3: Metadata-assisted detection
### Subtasks
- 3.1 Support `.ragcode` metadata as optimization input (not source of truth).
- 3.2 Validate metadata-derived roots with the same security checks.
- 3.3 Record decision reason when metadata is used.

## Task 4: Detector tests
### Subtasks
- 4.1 Add tests for marker precedence and conflict cases.
- 4.2 Add tests for nested repositories and monorepos.
- 4.3 Add tests for invalid/missing markers and path edge cases.

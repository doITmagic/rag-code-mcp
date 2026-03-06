# Registry Module - Tasks

## Depends On
- resolver (for workspace IDs and alias usage)
- branchstate (for last-confirmed metadata)

## Goal
Persist confirmed workspace context for deterministic cross-session behavior.

## Task 1: Registry schema
### Subtasks
- 1.1 Define workspace record fields (`id`, `root`, `name`, `confirmed_at`, `last_used_at`).
- 1.2 Define optional client/session binding fields.
- 1.3 Define schema version and migration policy.

```json
{
  "schema_version": "v1",
  "id": "abc123",
  "root": "/home/user/project",
  "name": "project",
  "client": "windsurf",
  "confirmed_at": "2025-02-14T09:00:00Z",
  "last_used_at": "2025-02-15T10:00:00Z"
}
```

## Task 2: Registry operations
### Subtasks
- 2.1 Implement upsert for confirmed workspace entries.
- 2.2 Implement lookup by id/root/name.
- 2.3 Implement list and stale-entry cleanup operations (e.g., remove entries unused for 30 days).

## Task 3: Confirmation workflow support
### Subtasks
- 3.1 Store user/client-selected workspace after ambiguity resolution.
- 3.2 Retrieve last confirmed workspace when context is incomplete.
- 3.3 Invalidate entries that no longer satisfy allowed roots/security checks.

## Task 4: Registry tests
### Subtasks
- 4.1 Test registry creation and persistence across runs.
- 4.2 Test lookup correctness with duplicate names and renamed roots.
- 4.3 Test invalidation and cleanup behavior.

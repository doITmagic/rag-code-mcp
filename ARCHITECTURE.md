# V2 Detection Core - Simple and Modular Architecture

## Objective
An isolated module for workspace detection + branch state (variant 2), easy to test and integrate into tools.

## Structure

```text
/
  ARCHITECTURE.md
  internal/
    contract/
      # DTOs and public contracts for the resolver
    resolver/
      # Single orchestrator: resolves workspace, ambiguity, fail-fast behavior
    detector/
      # Root detection via markers + path/allowed-roots validation
    branchstate/
      # Branch/HEAD reading and persisted-state comparison
    registry/
      # Persistence for confirmed workspaces (e.g., workspaces.json)
    tests/
      # Table-driven tests, AI-like scenarios
```

## Module Responsibilities

1. `contract`
   - defines standard input/output for all tools.
   - includes deterministic error codes (`NO_CONTEXT`, `AMBIGUOUS_WORKSPACE`, `OUTSIDE_ALLOWED_ROOTS`).

2. `resolver`
   - single entry point for all tools.
   - priority: `workspace_root` > `file_path` > active roots > confirmation > fail-fast.
   - contains no tool-specific logic.

3. `detector`
   - searches markers (`.git`, `go.mod`, `.ragcode`, `.agent`, etc.).
   - validates security boundaries and allowed paths.

4. `branchstate`
   - reads current branch + head SHA.
   - compares with `last_branch` and `last_head_sha`.
   - sets `reindex_required=true` when a mismatch is detected.

5. `registry`
   - stores workspaces confirmed by user/client.
   - uses a local JSON file for cross-session persistent context.

6. `tests`
   - real AI scenarios: missing `file_path`, invalid path, multi-workspace, branch switch, detached HEAD.
   - validates deterministic behavior and clear error messages.

## Key Rules (V2)

- No opaque fallback.
- If context is ambiguous: require explicit confirmation.
- If context is insufficient: fail fast with an actionable error.
- All tools must use the same `resolver`.

## Issue #21 Alignment (Path Resolver V2)

The V2 detection core is the foundation for issue #21 and should expose branch-aware and explainable path resolution behavior.

### 1. Branch-aware context key

Resolver context identity should be computed as:

```text
workspace_root + git_branch + git_head (+ worktree_id)
```

This prevents cache collisions between branches/worktrees and keeps branch-specific state isolated.

### 2. Explicit path-resolution metadata

Relevant tool responses should expose a consistent metadata envelope:

- `resolved_file_path`
- `path_resolution_source`
- `path_resolution_confidence`
- `used_fallback`
- `workspace_root`
- `git_branch`
- `git_head`
- `worktree_id` (when available)
- `path_context_key`
- `branch_mismatch_risk`

`reason` remains mandatory for deterministic observability.

### 3. Invalidation and anti-loop policy

- Isolate/invalidate state on branch switch.
- Decay confidence on HEAD mismatch/rewrite.
- Keep short TTL for fallback entries.
- Avoid infinite retry loops on missing/invalid paths.

### 4. AI <-> resolver feedback loop

Request payloads may provide structured feedback:

```text
path_feedback.status = "mismatch"
path_feedback.suggested_file_path
path_feedback.reason (optional)
```

Suggested paths are stored as candidates and promoted only after a successful resolution + execution cycle.

### 5. Branch mismatch risk signal

Resolver should return `branch_mismatch_risk` with values:

- `low`: branch/head match expected context.
- `medium`: branch matches but head changed.
- `high`: branch mismatch or fallback-driven uncertainty.

## Module Dependencies

```
contract -> resolver -> {detector, branchstate, registry} -> tests
```

Each module exposes a focused API but the resolver orchestrates them all. 
- detector provides root candidates.
- branchstate annotates responses with reindex decisions.
- registry persists confirmations for future runs.
- tests run cross-module matrices to ensure deterministic behavior.

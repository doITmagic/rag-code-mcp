# V2 Detection Core - Simple and Modular Architecture

## Objective
An isolated module for workspace detection + branch state (variant 2), easy to test and integrate into tools.

## Structure

```text
v2/
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

## Module Dependencies

```
contract -> resolver -> {detector, branchstate, registry} -> tests
```

Each module exposes a focused API but the resolver orchestrates them all. 
- detector provides root candidates.
- branchstate annotates responses with reindex decisions.
- registry persists confirmations for future runs.
- tests run cross-module matrices to ensure deterministic behavior.

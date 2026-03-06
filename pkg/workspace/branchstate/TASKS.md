# BranchState Module - Tasks

## Goal
Implement branch/HEAD tracking (Variant 2) to trigger deterministic reindex decisions.

## Task 1: Git state reader
### Subtasks
- 1.1 Read current branch name from git metadata.
- 1.2 Read current HEAD SHA.
- 1.3 Handle detached HEAD state explicitly.

## Task 2: Persisted state model
### Subtasks
- 2.1 Define fields: `last_branch`, `last_head_sha`, `last_indexed_at`.
- 2.2 Persist and load state from workspace-local JSON.
- 2.3 Add schema version field for future migration safety.

## Task 3: Reindex decision engine
### Subtasks
- 3.1 Compare current vs persisted state on each resolve.
- 3.2 Emit `reindex_required=true` on branch mismatch.
- 3.3 Emit `reindex_required=true` on head mismatch in same branch.

## Task 4: Branch state tests
### Subtasks
- 4.1 Test branch switch: `dev` -> `feature/*`.
- 4.2 Test same branch with new commits (HEAD changed).
- 4.3 Test detached HEAD and repository edge states.

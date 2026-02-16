# RagCode MCP Planning

## Scope
Track upcoming improvements for workspace path detection, indexing stability, and branch-awareness.

## 1) Branch switch handling in the same workspace

### Problem
When users switch Git branches in the same repository, indexed data may become stale and search results can mix contexts.

### Options
1. **Branch-aware collection naming**
   - Pattern: `{workspace_id}-{branch}-{language}`
   - Benefit: strict separation of indexes per branch.
   - Tradeoff: higher storage usage.

2. **HEAD/branch change detection + cache invalidation**
   - Persist `last_branch` and `last_head_sha` in workspace state.
   - On mismatch, mark workspace as dirty and trigger incremental reindex.
   - Benefit: lower storage than branch-aware collections.

3. **Watcher trigger on `.git/HEAD` and refs**
   - Detect branch switches immediately from filesystem events.
   - Benefit: faster convergence, less stale window.

4. **Staleness warning while reindexing**
   - If branch changed and indexing is not complete, return a clear warning.
   - Benefit: safer UX and fewer confusing answers.

### Suggested phased approach
- Phase 1: implement HEAD mismatch detection + incremental reindex.
- Phase 2: add optional `branch_aware_collections` config flag.
- Phase 3: add watcher optimization for `.git/HEAD` updates.

---

## 2) Path detection cascade improvements

### Problem
Current root detection mostly depends on classic project markers. We can improve speed and reliability in repeated sessions.

### Candidate cascade (logical order)
1. **Classic markers** (`.git`, `go.mod`, `package.json`, etc.)
2. **Workspace registry lookup** (if an exact known root exists)
3. **Agent metadata fallback** (`.ragcode/`, optionally `.agent/`) with validation
4. **Heuristic fallback scan** (existing detector behavior)

### Notes
- `.ragcode/` and `.agent/` should be optimization sources, not primary truth.
- Any root loaded from metadata must be validated against security constraints and allowed paths.
- Confirmed IDE/agent markers to keep in defaults: `.cursor/`, `.windsurf/`, `AGENTS.md`, `CLAUDE.md`.

---

## 3) Detection marker rule model extension

### Problem
`detection_markers` is currently a simple list and cannot express conditional rules.

### Proposal
Support structured marker rules, e.g.:
- `match: .git`
- `unless_dir_contains: [vendor, node_modules]`
- `priority: 10`

### Next steps
1. Design backward-compatible YAML schema.
2. Implement parser + evaluator in detector.
3. Add tests for rule combinations and edge cases.

---

## 4) Testing strategy for startup/detection behavior

### Goal
Cover startup decisions and path detection deterministically.

### Plan
- Add tests for config creation with/without CLI overrides.
- Add tests for marker-first detection and metadata fallbacks.
- Add tests for branch switch invalidation and reindex trigger behavior.

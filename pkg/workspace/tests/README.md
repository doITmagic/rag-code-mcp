# Tests Module

The `tests` package provides **end-to-end integration tests** for workspace resolution, covering decision paths, branch transitions, feedback workflows, and scenario validation with real git repositories and concurrent operations.

## 🎯 Objectives

Tests validate:
- **Decision Cascade**: All resolution paths (`workspace_root`, `file_path`, alias, roots)
- **Branch Awareness**: Reindex signals on branch/HEAD changes
- **Feedback Loop**: IDE suggestions and candidate promotion
- **Registry Persistence**: Cross-session workspace lookup
- **Isolation**: Workspace IDs isolate cache across branches
- **Risk Assessment**: Confidence decay on mismatch conditions
- **Regression Prevention**: Previously observed failures don't reoccur

---

## 📊 Test Categories

### 1. Decision Path Tests (`scenario_test.go`)

| Scenario | Expected Outcome |
|----------|-----------------|
| Explicit `workspace_root` | ✅ Return immediately (highest priority) |
| `file_path` with marker | ✅ Detect via marker walk |
| `workspace` alias | ✅ Resolve from registry |
| Single `root` | ✅ Return that root |
| Ambiguous `roots` + strict mode | ❌ Error + nil response |
| Ambiguous `roots` + fallback | ✅ Require confirmation + candidates |
| Missing context | ❌ Error: NO_CONTEXT |
| No detector configured | ❌ Error: NO_CONTEXT |

### 2. Branch State Tests (`scenario_test.go`)

| Test | Setup | Expected |
|------|-------|----------|
| `TestResolverBranchstateFirstSeen` | New workspace | `reindex_required=true`, reason=`FIRST_SEEN` |
| `TestResolverBranchstateBranchChanged` | git checkout new branch | `reindex_required=true`, reason=`BRANCH_CHANGED` |
| `TestResolverBranchstateHeadChangedSameBranch` | New commit on same branch | `reindex_required=true`, reason=`HEAD_CHANGED` |
| `TestResolverBranchstateNonGitFallback` | Non-git folder | No error, graceful fallback |

### 3. Integration Tests (`integration_test.go`)

#### TestBranchIsolationAndFeedbackPromotionIntegration
- **Setup**: Real registry + git repo + toggleable annotator
- **Flow**:
  1. First resolve on `main` branch
  2. Switch to `feature` branch → different workspace ID
  3. Confidence decays on high mismatch risk
  4. IDE provides feedback suggestion
  5. Promotion on execution success
  6. Final alias lookup uses promoted entry

#### TestFeedbackCandidatePromotionRequiresExecutionSignal
- **Setup**: Real registry
- **Flow**:
  1. Resolve with feedback (no execution signal)
  2. Verify metrics: `feedback_ingested=1`, `candidates_promoted=0`
  3. Resolve with feedback + execution signal
  4. Verify metrics: `candidates_promoted=1`

---

## 🏗️ Test Helpers

### fakeDetector
```go
type fakeDetector struct {
    candidate *contract.WorkspaceCandidate
    err       *contract.ResolveWorkspaceError
}
```
Simulates detector behavior without filesystem I/O.

### fakeRegistry
```go
type fakeRegistry struct {
    candidate *contract.WorkspaceCandidate
    err       *contract.ResolveWorkspaceError
    feedbackCount int
    promoteCount  int
}
```
Tracks feedback / promotion events; simulates storage.

### fakeAnnotator
```go
type fakeAnnotator struct {
    branch       string
    headSHA      string
    worktreeID   string
    mismatchRisk string
    err          *contract.ResolveWorkspaceError
}
```
Toggleable branch metadata for testing risk scenarios.

### branchstateAnnotator
```go
type branchstateAnnotator struct {
    mgr *branchstate.Manager
}
```
Real branch state manager for end-to-end git testing.

---

## 🚀 Running Tests

### All Tests
```bash
go test ./pkg/workspace/tests -v
```

### Specific Test
```bash
go test ./pkg/workspace/tests -run TestResolverScenarios -v
```

### With Race Detector
```bash
go test ./pkg/workspace/tests -race -v
```

### Coverage
```bash
go test ./pkg/workspace/tests -cover
```

---

## 🔍 Test Coverage

### Scenario Matrix (Decision Paths)
- ✅ Explicit workspace root
- ✅ File path detection
- ✅ Workspace alias lookup
- ✅ Single root from list
- ✅ Ambiguous roots (strict mode)
- ✅ Ambiguous roots (fallback + confirmation)
- ✅ Missing context error
- ✅ Roots with whitespace-only entries
- ✅ Roots scoring (deepest path wins)

### Branch Transitions (Git-Aware)
- ✅ First time (no cached state)
- ✅ Branch switch (different scope)
- ✅ HEAD change on same branch (new symbols)
- ✅ Non-git folder (graceful fallback)

### Feedback & Promotion
- ✅ Feedback recorded without execution signal
- ✅ Candidate NOT promoted without execution signal
- ✅ Candidate promoted WITH execution signal
- ✅ Metrics tracking (feedback_ingested, candidates_promoted)

### Metadata & Confidence
- ✅ `path_resolution_source` correct per decision path
- ✅ `path_resolution_confidence` deterministic
- ✅ `used_fallback` toggles on fallback paths
- ✅ `path_context_key` and branch metadata present
- ✅ Confidence decay on high mismatch risk

### Edge Cases
- ✅ Repeated invalid path (stable error responses)
- ✅ Branch switching isolation (different workspace IDs)
- ✅ TTL/cache expiration
- ✅ Anti-loop behavior (no infinite retries)

---

## 📝 Writing New Tests

### Template: Single Scenario
```go
func TestMyScenario(t *testing.T) {
    ctx := context.Background()
    
    // Setup
    deps := resolver.Dependencies{
        Detector: &fakeDetector{...},
        Registry: &fakeRegistry{...},
    }
    res := resolver.New(deps)
    
    // Execute
    req := contract.ResolveWorkspaceRequest{...}
    resp, err := res.Resolve(ctx, req)
    
    // Assert
    if err != nil {
        t.Fatalf("unexpected error: %v", err)
    }
    if resp.ResolvedRoot != "/expected/path" {
        t.Errorf("expected /expected/path, got %s", resp.ResolvedRoot)
    }
}
```

### Template: Integration Test with Git
```go
func TestMyGitScenario(t *testing.T) {
    repo := initGitRepo(t)  // Helper: creates temp .git
    
    manager := branchstate.NewManager(branchstate.WithCacheTTL(0))
    res := resolver.New(resolver.Dependencies{
        BranchAnnotator: &branchstateAnnotator{mgr: manager},
    })
    
    // First resolve
    resp1, _ := res.Resolve(ctx, contract.ResolveWorkspaceRequest{
        WorkspaceRoot: repo,
    })
    
    // Git state change (e.g., checkout branch)
    runGit(t, repo, "checkout", "-b", "feature")
    
    // Second resolve
    resp2, _ := res.Resolve(ctx, contract.ResolveWorkspaceRequest{
        WorkspaceRoot: repo,
    })
    
    // Verify change detected
    if resp2.ReindexRequired != true {
        t.Fatal("expected reindex signal after branch change")
    }
}
```

---

## 🔗 Test Dependencies

- **contract**: ResolveWorkspaceRequest/Response types
- **resolver**: Main resolution engine
- **detector**: File path detection
- **registry**: Workspace storage
- **branchstate**: Git metadata
- **Standard lib**: `testing`, `os`, `path/filepath`, `exec` (git)

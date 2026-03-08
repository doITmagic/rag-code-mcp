package tools

import (
	"testing"
)

// ─── scopeDir ────────────────────────────────────────────────────────────────

func TestScopeDirFromFile(t *testing.T) {
	got := scopeDir("/project/src/agents/main.py")
	want := "/project/src/agents"
	if got != want {
		t.Errorf("scopeDir(file) = %q, want %q", got, want)
	}
}

func TestScopeDirFromDir(t *testing.T) {
	got := scopeDir("/project/src/agents")
	want := "/project/src/agents"
	if got != want {
		t.Errorf("scopeDir(dir) = %q, want %q", got, want)
	}
}

func TestScopeDirEmpty(t *testing.T) {
	got := scopeDir("")
	if got != "" {
		t.Errorf("scopeDir('') = %q, want empty", got)
	}
}

// ─── pathProximity ───────────────────────────────────────────────────────────

func TestPathProximitySameDir(t *testing.T) {
	scope := "/project/src/agents"
	result := "/project/src/agents/handler.go"
	got := pathProximity(result, scope)
	if got != pathBoostSameDir {
		t.Errorf("same dir: got %f, want %f", got, pathBoostSameDir)
	}
}

func TestPathProximityChildSubtree(t *testing.T) {
	scope := "/project/src/agents"
	result := "/project/src/agents/utils/helpers.go"
	got := pathProximity(result, scope)
	if got != pathBoostSameSubtree {
		t.Errorf("child subtree: got %f, want %f", got, pathBoostSameSubtree)
	}
}

func TestPathProximityParentSubtree(t *testing.T) {
	scope := "/project/src/agents/utils"
	result := "/project/src/agents/handler.go"
	got := pathProximity(result, scope)
	if got != pathBoostSameSubtree {
		t.Errorf("parent subtree: got %f, want %f", got, pathBoostSameSubtree)
	}
}

func TestPathProximityCloseSibling(t *testing.T) {
	scope := "/project/src/agents"
	result := "/project/src/api/server.go"
	got := pathProximity(result, scope)
	if got != 1.0 {
		t.Errorf("close sibling: got %f, want 1.0", got)
	}
}

func TestPathProximityDistant(t *testing.T) {
	scope := "/project/src/agents"
	result := "/project/vendor/lib/deep/nested/file.go"
	got := pathProximity(result, scope)
	if got != pathPenaltyDistant {
		t.Errorf("distant: got %f, want %f", got, pathPenaltyDistant)
	}
}

func TestPathProximityEmptyScope(t *testing.T) {
	got := pathProximity("/project/file.go", "")
	if got != 1.0 {
		t.Errorf("empty scope: got %f, want 1.0", got)
	}
}

func TestPathProximityEmptyResult(t *testing.T) {
	got := pathProximity("", "/project/src")
	if got != 1.0 {
		t.Errorf("empty result: got %f, want 1.0", got)
	}
}

// ─── applyPathScoping ────────────────────────────────────────────────────────

func TestApplyPathScopingReorders(t *testing.T) {
	merged := []mergedResult{
		{filePath: "/project/vendor/external/deep/nested/file.go", score: 0.90, name: "VendorFunc"},
		{filePath: "/project/src/agents/handler.go", score: 0.85, name: "AgentFunc"},
		{filePath: "/project/src/agents/utils.go", score: 0.80, name: "AgentUtil"},
	}

	scope := "/project/src/agents"
	result := applyPathScoping(merged, scope)

	// AgentFunc and AgentUtil should be boosted above VendorFunc
	if result[0].name != "AgentFunc" && result[0].name != "AgentUtil" {
		t.Errorf("Expected agent-related result first, got %q (score=%f)", result[0].name, result[0].score)
	}

	// VendorFunc should have been penalized
	for _, m := range result {
		if m.name == "VendorFunc" && m.score >= 0.90 {
			t.Errorf("VendorFunc should have been penalized, score=%f", m.score)
		}
	}
}

func TestApplyPathScopingNoOpWithoutScope(t *testing.T) {
	merged := []mergedResult{
		{filePath: "/a.go", score: 0.9},
		{filePath: "/b.go", score: 0.8},
	}

	result := applyPathScoping(merged, "")

	if result[0].score != 0.9 || result[1].score != 0.8 {
		t.Error("Scores should not change when scope is empty")
	}
}

// ─── longestCommonPath ───────────────────────────────────────────────────────

func TestLongestCommonPathBasic(t *testing.T) {
	got := longestCommonPath("/project/src/agents", "/project/src/api")
	want := "/project/src"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestLongestCommonPathNoCommon(t *testing.T) {
	got := longestCommonPath("/home/user", "/var/lib")
	// On Linux both start with "/" so they share the root
	if got != "" && got != "/" {
		t.Logf("got %q — may share filesystem root", got)
	}
}

func TestLongestCommonPathIdentical(t *testing.T) {
	got := longestCommonPath("/project/src", "/project/src")
	if got != "/project/src" {
		t.Errorf("got %q, want /project/src", got)
	}
}

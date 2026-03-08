package scoring

import (
	"path/filepath"
	"testing"
)

// ─── ScopeDir ────────────────────────────────────────────────────────────────

func TestScopeDirFromFile(t *testing.T) {
	got := ScopeDir(filepath.FromSlash("/project/src/agents/main.py"))
	want := filepath.FromSlash("/project/src/agents")
	if got != want {
		t.Errorf("ScopeDir(file) = %q, want %q", got, want)
	}
}

func TestScopeDirFromDir(t *testing.T) {
	got := ScopeDir(filepath.FromSlash("/project/src/agents"))
	want := filepath.FromSlash("/project/src/agents")
	if got != want {
		t.Errorf("ScopeDir(dir) = %q, want %q", got, want)
	}
}

func TestScopeDirEmpty(t *testing.T) {
	if got := ScopeDir(""); got != "" {
		t.Errorf("ScopeDir('') = %q, want empty", got)
	}
}

// ─── PathProximity ───────────────────────────────────────────────────────────

func TestPathProximitySameDir(t *testing.T) {
	got := PathProximity(filepath.FromSlash("/project/src/agents/handler.go"), filepath.FromSlash("/project/src/agents"))
	if got != BoostSameDir {
		t.Errorf("same dir: got %f, want %f", got, BoostSameDir)
	}
}

func TestPathProximityChildSubtree(t *testing.T) {
	got := PathProximity(filepath.FromSlash("/project/src/agents/utils/helpers.go"), filepath.FromSlash("/project/src/agents"))
	if got != BoostSameSubtree {
		t.Errorf("child subtree: got %f, want %f", got, BoostSameSubtree)
	}
}

func TestPathProximityParentSubtree(t *testing.T) {
	got := PathProximity(filepath.FromSlash("/project/src/agents/handler.go"), filepath.FromSlash("/project/src/agents/utils"))
	if got != BoostSameSubtree {
		t.Errorf("parent subtree: got %f, want %f", got, BoostSameSubtree)
	}
}

func TestPathProximityCloseSibling(t *testing.T) {
	got := PathProximity(filepath.FromSlash("/project/src/api/server.go"), filepath.FromSlash("/project/src/agents"))
	if got != 1.0 {
		t.Errorf("close sibling: got %f, want 1.0", got)
	}
}

func TestPathProximityDistant(t *testing.T) {
	got := PathProximity(filepath.FromSlash("/project/vendor/external/deep/nested/file.go"), filepath.FromSlash("/project/src/agents"))
	if got != PenaltyDistant {
		t.Errorf("distant: got %f, want %f", got, PenaltyDistant)
	}
}

func TestPathProximityEmptyScope(t *testing.T) {
	if got := PathProximity(filepath.FromSlash("/project/file.go"), ""); got != 1.0 {
		t.Errorf("empty scope: got %f, want 1.0", got)
	}
}

func TestPathProximityEmptyResult(t *testing.T) {
	if got := PathProximity("", filepath.FromSlash("/project/src")); got != 1.0 {
		t.Errorf("empty result: got %f, want 1.0", got)
	}
}

// ─── LongestCommonPath ───────────────────────────────────────────────────────

func TestLongestCommonPathBasic(t *testing.T) {
	got := LongestCommonPath(filepath.FromSlash("/project/src/agents"), filepath.FromSlash("/project/src/api"))
	if got != filepath.FromSlash("/project/src") {
		t.Errorf("got %q, want /project/src", got)
	}
}

func TestLongestCommonPathIdentical(t *testing.T) {
	got := LongestCommonPath(filepath.FromSlash("/project/src"), filepath.FromSlash("/project/src"))
	if got != filepath.FromSlash("/project/src") {
		t.Errorf("got %q, want /project/src", got)
	}
}

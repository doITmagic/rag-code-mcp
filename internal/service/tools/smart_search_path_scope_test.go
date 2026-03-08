package tools

import "testing"

// TestApplyPathScopingReorders verifies that the adapter correctly uses
// pkg/scoring.PathProximity to reorder mergedResults.
func TestApplyPathScopingReorders(t *testing.T) {
	merged := []mergedResult{
		{filePath: "/project/vendor/external/deep/nested/file.go", score: 0.90, name: "VendorFunc"},
		{filePath: "/project/src/agents/handler.go", score: 0.85, name: "AgentFunc"},
		{filePath: "/project/src/agents/utils.go", score: 0.80, name: "AgentUtil"},
	}

	scope := "/project/src/agents"
	result := applyPathScoping(merged, scope)

	if result[0].name != "AgentFunc" && result[0].name != "AgentUtil" {
		t.Errorf("Expected agent-related result first, got %q (score=%f)", result[0].name, result[0].score)
	}

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

package scoring

import "testing"

func TestFilterTokensRemovesShort(t *testing.T) {
	got := FilterTokens([]string{"go", "ab", "calculator", "x", "   test   "})
	if len(got) != 2 {
		t.Fatalf("expected 2 tokens, got %d: %v", len(got), got)
	}
	if got[0] != "calculator" || got[1] != "test" {
		t.Errorf("unexpected tokens: %v", got)
	}
}

func TestFilterTokensEmpty(t *testing.T) {
	got := FilterTokens(nil)
	if len(got) != 0 {
		t.Errorf("expected empty, got %v", got)
	}
}

func TestLexicalMatchScore(t *testing.T) {
	content := "func Calculator() Calculator { return Calculator{} }"
	tokens := []string{"Calculator"}
	score := LexicalMatchScore(content, tokens)
	if score != 3 {
		t.Errorf("expected 3 occurrences, got %f", score)
	}
}

func TestLexicalMatchScoreNoTokens(t *testing.T) {
	score := LexicalMatchScore("anything", nil)
	if score != 0 {
		t.Errorf("expected 0, got %f", score)
	}
}

func TestTokenMatchRatioAll(t *testing.T) {
	ratio := TokenMatchRatio("func Add(a int, b int) int", []string{"func", "Add", "int"})
	if ratio != 1.0 {
		t.Errorf("expected 1.0, got %f", ratio)
	}
}

func TestTokenMatchRatioPartial(t *testing.T) {
	ratio := TokenMatchRatio("func Add(a int)", []string{"Add", "missing"})
	if ratio != 0.5 {
		t.Errorf("expected 0.5, got %f", ratio)
	}
}

func TestTokenMatchRatioEmpty(t *testing.T) {
	ratio := TokenMatchRatio("text", nil)
	if ratio != 0 {
		t.Errorf("expected 0, got %f", ratio)
	}
}

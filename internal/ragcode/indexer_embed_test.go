package ragcode

import (
	"strings"
	"testing"

	"github.com/doITmagic/rag-code-mcp/internal/codetypes"
)

func TestBuildEmbedText_NoTruncation(t *testing.T) {
	ch := codetypes.CodeChunk{
		Package:   "mypkg",
		Name:      "MyFunc",
		Signature: "func MyFunc() string",
		Code:      "return \"hello\"",
		Docstring: "MyFunc returns hello.",
		FilePath:  "foo.go",
	}
	text, truncated := buildEmbedText(ch, maxEmbedChars)
	if truncated {
		t.Fatal("expected no truncation for small chunk")
	}
	if !strings.Contains(text, ch.Docstring) {
		t.Errorf("embed text missing docstring")
	}
	if !strings.Contains(text, ch.Signature) {
		t.Errorf("embed text missing signature")
	}
	if !strings.Contains(text, ch.Code) {
		t.Errorf("embed text missing code")
	}
}

func TestBuildEmbedText_TruncatesCode(t *testing.T) {
	bigCode := strings.Repeat("x", 40_000)
	ch := codetypes.CodeChunk{
		Package:   "mypkg",
		Name:      "BigFunc",
		Signature: "func BigFunc()",
		Code:      bigCode,
		FilePath:  "big.go",
	}
	limit := 30_000
	text, truncated := buildEmbedText(ch, limit)
	if !truncated {
		t.Fatal("expected truncation for large code body")
	}
	runes := []rune(text)
	if len(runes) > limit {
		t.Errorf("truncated text has %d runes, want <= %d", len(runes), limit)
	}
	// Signature must survive truncation
	if !strings.Contains(text, "func BigFunc()") {
		t.Errorf("embed text missing signature after truncation")
	}
}

func TestBuildEmbedText_WithDocstring_TruncatesCode(t *testing.T) {
	bigCode := strings.Repeat("y", 40_000)
	ch := codetypes.CodeChunk{
		Signature: "func Fn()",
		Code:      bigCode,
		Docstring: "This is a docstring.",
		FilePath:  "fn.go",
	}
	limit := 30_000
	text, truncated := buildEmbedText(ch, limit)
	if !truncated {
		t.Fatal("expected truncation")
	}
	if !strings.Contains(text, "This is a docstring.") {
		t.Errorf("docstring missing after truncation")
	}
	if len([]rune(text)) > limit {
		t.Errorf("text exceeds limit after truncation")
	}
}

func TestBuildEmbedText_ExactlyAtLimit(t *testing.T) {
	limit := 100
	// meta = "func Fn()\n\n" → 11 chars
	meta := "func Fn()\n\n"
	codeLen := limit - len([]rune(meta))
	ch := codetypes.CodeChunk{
		Signature: "func Fn()",
		Code:      strings.Repeat("a", codeLen),
		FilePath:  "fn.go",
	}
	text, truncated := buildEmbedText(ch, limit)
	if truncated {
		t.Fatalf("expected no truncation at exact boundary, got truncated; len=%d", len([]rune(text)))
	}
	_ = text
}

func TestBuildEmbedText_EmptyCode(t *testing.T) {
	ch := codetypes.CodeChunk{
		Signature: "func Empty()",
		Code:      "",
		Docstring: "Empty function.",
		FilePath:  "empty.go",
	}
	text, truncated := buildEmbedText(ch, maxEmbedChars)
	if truncated {
		t.Fatal("expected no truncation for empty code")
	}
	if !strings.Contains(text, "Empty function.") {
		t.Errorf("docstring missing")
	}
	if !strings.Contains(text, "func Empty()") {
		t.Errorf("signature missing")
	}
}

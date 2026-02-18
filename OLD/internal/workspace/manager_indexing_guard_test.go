package workspace

import (
	"context"
	"strings"
	"testing"
)

func TestSupportsLanguageIndexing(t *testing.T) {
	mgr := &Manager{}

	if !mgr.supportsLanguageIndexing("go") {
		t.Fatalf("expected go to be supported")
	}
	if mgr.supportsLanguageIndexing("markdown") {
		t.Fatalf("expected markdown to be unsupported")
	}
	if mgr.supportsLanguageIndexing("yaml") {
		t.Fatalf("expected yaml to be unsupported")
	}
}

func TestStartIndexingUnsupportedLanguageFailsFast(t *testing.T) {
	mgr := &Manager{}
	info := &Info{ID: "ws1", Root: "/tmp/ws1"}

	err := mgr.StartIndexing(context.Background(), info, "yaml", false)
	if err == nil {
		t.Fatalf("expected error for unsupported language")
	}
	if !strings.Contains(err.Error(), "no code analyzer available for language 'yaml'") {
		t.Fatalf("unexpected error: %v", err)
	}
}

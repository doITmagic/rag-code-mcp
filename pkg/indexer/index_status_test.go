package indexer

import (
	"testing"
)

func TestIndexStatusRoundTrip(t *testing.T) {
	wsRoot := t.TempDir()

	// Ensure no file exists yet
	if s := LoadIndexStatus(wsRoot); s != nil {
		t.Fatal("expected nil before first save")
	}

	status := &IndexStatus{
		StartedAt: "2025-01-01T00:00:00Z",
		Elapsed:   "5s",
		Languages: map[string]LangStatus{
			// Changed is json:"-" (internal-only field) and is not persisted.
			"go": {OnDisk: 100, Processed: 5},
		},
	}

	SaveIndexStatus(wsRoot, status)

	loaded := LoadIndexStatus(wsRoot)
	if loaded == nil {
		t.Fatal("expected non-nil after save")
	}
	if loaded.Languages["go"].OnDisk != 100 {
		t.Errorf("OnDisk: got %d, want 100", loaded.Languages["go"].OnDisk)
	}
	if loaded.Languages["go"].Processed != 5 {
		t.Errorf("Processed: got %d, want 5", loaded.Languages["go"].Processed)
	}
}

func TestLoadIndexStatusMissing(t *testing.T) {
	s := LoadIndexStatus(t.TempDir())
	if s != nil {
		t.Fatal("expected nil for missing file")
	}
}

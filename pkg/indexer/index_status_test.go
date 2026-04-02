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

func TestIndexStatusBreakdownRoundTrip(t *testing.T) {
	wsRoot := t.TempDir()

	status := &IndexStatus{
		StartedAt: "2026-04-01T00:00:00Z",
		Languages: map[string]LangStatus{
			"javascript": {
				OnDisk:    41,
				Processed: 41,
				Breakdown: map[string]int{
					".ts":  30,
					".tsx": 7,
					".js":  3,
					".vue": 1,
				},
			},
			"go": {
				OnDisk:    80,
				Processed: 80,
				Breakdown: map[string]int{
					".go": 80,
				},
			},
			"docs": {
				OnDisk:    15,
				Processed: 15,
				Breakdown: map[string]int{
					".md":   10,
					".yaml": 3,
					".json": 2,
				},
			},
		},
	}

	SaveIndexStatus(wsRoot, status)
	loaded := LoadIndexStatus(wsRoot)

	if loaded == nil {
		t.Fatal("expected non-nil after save")
	}

	// Verify JavaScript breakdown
	jsStatus := loaded.Languages["javascript"]
	if jsStatus.OnDisk != 41 {
		t.Errorf("javascript OnDisk: got %d, want 41", jsStatus.OnDisk)
	}
	if jsStatus.Breakdown[".ts"] != 30 {
		t.Errorf("javascript .ts: got %d, want 30", jsStatus.Breakdown[".ts"])
	}
	if jsStatus.Breakdown[".tsx"] != 7 {
		t.Errorf("javascript .tsx: got %d, want 7", jsStatus.Breakdown[".tsx"])
	}
	if jsStatus.Breakdown[".js"] != 3 {
		t.Errorf("javascript .js: got %d, want 3", jsStatus.Breakdown[".js"])
	}
	if jsStatus.Breakdown[".vue"] != 1 {
		t.Errorf("javascript .vue: got %d, want 1", jsStatus.Breakdown[".vue"])
	}
	if len(jsStatus.Breakdown) != 4 {
		t.Errorf("javascript breakdown length: got %d, want 4", len(jsStatus.Breakdown))
	}

	// Verify docs breakdown has multiple extensions
	docsStatus := loaded.Languages["docs"]
	if docsStatus.Breakdown[".md"] != 10 {
		t.Errorf("docs .md: got %d, want 10", docsStatus.Breakdown[".md"])
	}

	// Verify go breakdown (single extension)
	goStatus := loaded.Languages["go"]
	if goStatus.Breakdown[".go"] != 80 {
		t.Errorf("go .go: got %d, want 80", goStatus.Breakdown[".go"])
	}
}

func TestIndexStatusBreakdownOmitEmpty(t *testing.T) {
	wsRoot := t.TempDir()

	// Status without breakdown — should not appear in JSON
	status := &IndexStatus{
		StartedAt: "2026-04-01T00:00:00Z",
		Languages: map[string]LangStatus{
			"go": {OnDisk: 50, Processed: 50},
		},
	}

	SaveIndexStatus(wsRoot, status)
	loaded := LoadIndexStatus(wsRoot)

	if loaded == nil {
		t.Fatal("expected non-nil after save")
	}
	goStatus := loaded.Languages["go"]
	if goStatus.Breakdown != nil {
		t.Errorf("expected nil breakdown for omitempty, got %v", goStatus.Breakdown)
	}
}

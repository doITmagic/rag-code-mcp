package registry

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/doITmagic/rag-code-mcp/v2/internal/contract"
)

type testAuditSink struct {
	events []string
}

func (s *testAuditSink) Record(ctx context.Context, event string, fields map[string]any) {
	s.events = append(s.events, event)
}

func TestRegistryUpsertAndLookup(t *testing.T) {
	tmp := filepath.Join(t.TempDir(), "registry.json")
	r, err := New(tmp)
	if err != nil {
		t.Fatalf("new registry: %v", err)
	}

	entry, err := r.Upsert("/root/project", "proj", "windsurf")
	if err != nil {
		t.Fatalf("upsert: %v", err)
	}
	if entry.ID == "" {
		t.Fatalf("expected entry id")
	}

	retrieved, ok := r.LookupByID(entry.ID)
	if !ok || retrieved.Root != "/root/project" {
		t.Fatalf("lookup by id failed")
	}

	retrieved, ok = r.LookupByRoot("/root/project")
	if !ok || retrieved.Name != "proj" {
		t.Fatalf("lookup by root failed")
	}
}

func TestRegistryPersistence(t *testing.T) {
	tmp := filepath.Join(t.TempDir(), "registry.json")
	r, err := New(tmp)
	if err != nil {
		t.Fatalf("new registry: %v", err)
	}

	if _, err := r.Upsert("/one", "one", "clientA"); err != nil {
		t.Fatalf("upsert: %v", err)
	}

	r2, err := New(tmp)
	if err != nil {
		t.Fatalf("reload registry: %v", err)
	}
	if _, ok := r2.LookupByRoot("/one"); !ok {
		t.Fatalf("expected entry after reload")
	}
}

func TestRegistryCleanup(t *testing.T) {
	tmp := filepath.Join(t.TempDir(), "registry.json")
	r, err := New(tmp)
	if err != nil {
		t.Fatalf("new registry: %v", err)
	}

	r.clock = func() time.Time { return time.Unix(0, 0) }
	if _, err := r.Upsert("/old", "old", "client"); err != nil {
		t.Fatalf("upsert old: %v", err)
	}
	r.clock = func() time.Time { return time.Unix(10, 0) }
	if _, err := r.Upsert("/new", "new", "client"); err != nil {
		t.Fatalf("upsert new: %v", err)
	}

	cutoff := time.Unix(5, 0)
	if err := r.Cleanup(cutoff); err != nil {
		t.Fatalf("cleanup: %v", err)
	}

	if _, ok := r.LookupByRoot("/old"); ok {
		t.Fatalf("old entry should be removed")
	}
	if _, ok := r.LookupByRoot("/new"); !ok {
		t.Fatalf("new entry should remain")
	}
}

func TestFeedbackAndPromotion(t *testing.T) {
	ctx := context.Background()
	tmp := filepath.Join(t.TempDir(), "registry.json")
	r, err := New(tmp)
	if err != nil {
		t.Fatalf("new registry: %v", err)
	}

	sink := &testAuditSink{}
	r.SetAuditSink(sink)

	// 1. Record feedback
	feedback := &contract.PathFeedback{
		SuggestedPath: "/suggested/path",
		Reason:        "matches user intent",
	}
	if err := r.RecordFeedback(ctx, feedback); err != nil {
		t.Fatalf("record feedback: %v", err)
	}

	// Check if stored as candidate
	if cand, ok := r.candidates[strings.ToLower(filepath.Clean("/suggested/path"))]; !ok || cand.Count != 1 {
		t.Fatalf("candidate not stored correctly")
	}

	// 2. No promotion without execution signal
	if err := r.PromoteCandidate(ctx, "/suggested/path", "ide", false); err != nil {
		t.Fatalf("unexpected promote error: %v", err)
	}
	if _, ok := r.candidates[strings.ToLower(filepath.Clean("/suggested/path"))]; !ok {
		t.Fatalf("candidate should remain until execution signal")
	}

	// 3. Promote with execution signal
	if err := r.PromoteCandidate(ctx, "/suggested/path", "ide", true); err != nil {
		t.Fatalf("promotion failed: %v", err)
	}

	// Candidate should be removed
	if _, ok := r.candidates[strings.ToLower(filepath.Clean("/suggested/path"))]; ok {
		t.Fatalf("candidate should have been removed after promotion")
	}

	// Entry should exist
	if _, ok := r.LookupByRoot("/suggested/path"); !ok {
		t.Fatalf("entry should exist after promotion")
	}

	metrics := r.MetricsSnapshot()
	if metrics.FeedbackIngested != 1 {
		t.Fatalf("expected feedback_ingested=1, got %d", metrics.FeedbackIngested)
	}
	if metrics.CandidatesPromoted != 1 {
		t.Fatalf("expected candidates_promoted=1, got %d", metrics.CandidatesPromoted)
	}

	if len(sink.events) < 2 {
		t.Fatalf("expected audit events for feedback + promotion")
	}
}

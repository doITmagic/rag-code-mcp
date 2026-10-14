package indexer

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/doITmagic/rag-code-mcp/pkg/parser"
	"github.com/doITmagic/rag-code-mcp/pkg/parser/javascript"
	"github.com/doITmagic/rag-code-mcp/pkg/parser/php"
)

func TestStoredCallsKeepClassIdentity(t *testing.T) {
	for _, tc := range []struct {
		ext, code string
		analyzer  parser.Analyzer
	}{
		{"php", `<?php namespace App; class A { function validate() {} function run() { $this->validate(); } } class B { function validate() {} function run() { $this->validate(); } }`, php.NewAnalyzer()},
		{"js", `export class A {
  validate(value) { return value; }
  run(value) { return this.validate(value); }
}
export class B {
  validate(value) { return value; }
  run(value) { return this.validate(value); }
}`, javascript.NewCodeAnalyzer()},
	} {
		t.Run(tc.ext, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "owners."+tc.ext)
			if err := os.WriteFile(path, []byte(tc.code), 0600); err != nil {
				t.Fatal(err)
			}
			res, err := tc.analyzer.Analyze(context.Background(), path)
			if err != nil {
				t.Fatal(err)
			}
			store := &mockStore{}
			if err := NewService(&mockEmbedder{}, store).IndexItems(context.Background(), "test", res.Symbols); err != nil {
				t.Fatal(err)
			}
			owners := map[string]string{}
			seenIDs := map[string]bool{}
			for _, p := range store.upsertPoints {
				if seenIDs[p.ID] {
					t.Fatalf("duplicate symbol ID: %s", p.ID)
				}
				seenIDs[p.ID] = true
				if p.Payload["type"] == "method" {
					meta := p.Payload["metadata"].(map[string]interface{})
					owners[p.Payload["symbol_id"].(string)] = meta["class"].(string)
				}
			}
			calls := 0
			for _, p := range store.upsertPoints {
				rels, _ := p.Payload["relations"].([]interface{})
				for _, raw := range rels {
					r := raw.(map[string]interface{})
					if r["type"] != "calls" {
						continue
					}
					calls++
					if p.Payload["type"] != "method" {
						t.Fatalf("class duplicates calls: %+v", p.Payload)
					}
					sourceID := p.Payload["symbol_id"].(string)
					targetID, _ := r["target_id"].(string)
					if targetID == "" || owners[sourceID] != owners[targetID] || r["resolution"] != "resolved" {
						t.Fatalf("wrong owner: source=%s relation=%+v", owners[sourceID], r)
					}
				}
			}
			if calls != 2 {
				t.Fatalf("got %d calls, want 2; symbols=%+v", calls, res.Symbols)
			}
		})
	}
}

func TestLocalRelationsDoNotGuessDynamicTargets(t *testing.T) {
	symbols := []parser.Symbol{
		{Name: "validate", Type: parser.Method, FilePath: "a.php", QualifiedName: "A::validate", StartLine: 1},
		{Name: "validate", Type: parser.Method, FilePath: "a.php", QualifiedName: "B::validate", StartLine: 2},
		{Name: "run", Type: parser.Function, FilePath: "a.php", Relations: []parser.Relation{{TargetName: "validate", Type: parser.RelCalls, Receiver: "obj"}}},
	}
	resolveLocalRelations(symbols)
	r := symbols[2].Relations[0]
	if r.TargetID != "" || r.Resolution != "unresolved" {
		t.Fatalf("guessed dynamic receiver: %+v", r)
	}
}

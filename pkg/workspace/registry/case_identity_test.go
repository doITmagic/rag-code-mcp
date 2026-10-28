package registry

import (
	"context"
	"crypto/sha1"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/doITmagic/rag-code-mcp/pkg/workspace/contract"
)

func caseDistinctRoots(t *testing.T) (string, string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("Windows path identities are case-insensitive")
	}
	base := t.TempDir()
	upper, lower := filepath.Join(base, "Project"), filepath.Join(base, "project")
	for _, root := range []string{upper, lower} {
		if err := os.MkdirAll(root, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	u, _ := os.Stat(upper)
	l, _ := os.Stat(lower)
	if os.SameFile(u, l) {
		t.Skip("filesystem does not support case-distinct directories")
	}
	return upper, lower
}

func TestCaseDistinctWorkspaceRegistryAndCandidates(t *testing.T) {
	upper, lower := caseDistinctRoots(t)
	r, err := New(filepath.Join(t.TempDir(), "registry.json"))
	if err != nil {
		t.Fatal(err)
	}
	for _, root := range []string{upper, lower} {
		if err := r.RecordFeedback(context.Background(), &contract.PathFeedback{SuggestedPath: root}); err != nil {
			t.Fatal(err)
		}
	}
	if len(r.candidates) != 2 {
		t.Fatal("case-distinct candidates merged")
	}
	if err := r.PromoteCandidate(context.Background(), upper, "test", true); err != nil {
		t.Fatal(err)
	}
	if _, ok := r.candidates[contract.WorkspacePathKey(lower)]; !ok || len(r.candidates) != 1 {
		t.Fatal("promoting uppercase root consumed lowercase candidate")
	}
	if _, err := r.Upsert(lower, "lower", "test"); err != nil {
		t.Fatal(err)
	}
	if len(r.List()) != 2 {
		t.Fatal("case-distinct workspaces merged")
	}
	for _, root := range []string{upper, lower} {
		entry, ok := r.LookupByRoot(root)
		if !ok || entry.Root != root {
			t.Fatalf("lookup %s: %v, %v", root, entry, ok)
		}
		if parent, ok := r.FindParentWorkspace(filepath.Join(root, "src")); !ok || parent != root {
			t.Fatalf("parent for %s: %s, %v", root, parent, ok)
		}
	}
}

func TestCaseDistinctParentDoesNotAbsorbChild(t *testing.T) {
	upper, lower := caseDistinctRoots(t)
	child := filepath.Join(upper, "child")
	state := filepath.Join(child, ".ragcode", "state.json")
	if err := os.MkdirAll(filepath.Dir(state), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(state, []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	r, err := New(filepath.Join(t.TempDir(), "registry.json"))
	if err != nil {
		t.Fatal(err)
	}
	for _, root := range []string{child, lower} {
		if _, err := r.Upsert(root, "", "test"); err != nil {
			t.Fatal(err)
		}
	}
	if _, ok := r.LookupByRoot(child); !ok {
		t.Fatal("different-case parent absorbed unrelated child")
	}
	if _, err := os.Stat(state); err != nil {
		t.Fatalf("unrelated child state was deleted: %v", err)
	}
}

func TestLoadRekeysLegacyUppercaseWorkspace(t *testing.T) {
	upper, lower := caseDistinctRoots(t)
	legacyID := fmt.Sprintf("%x", sha1.Sum([]byte(strings.ToLower(upper))))
	path := filepath.Join(t.TempDir(), "registry.json")
	data, err := json.Marshal(registryStore{Version: registryStoreVersion, Entries: []*Entry{{ID: legacyID, Root: upper, Name: "upper"}}})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	r, err := New(path)
	if err != nil {
		t.Fatal(err)
	}
	entry, ok := r.LookupByRoot(upper)
	if !ok || entry.ID == legacyID || entry.ID != hashRoot(upper) {
		t.Fatalf("legacy entry not re-keyed: %v", entry)
	}
	if _, ok := r.LookupByID(legacyID); ok {
		t.Fatal("legacy case-folded alias retained")
	}
	if _, err := r.Upsert(lower, "lower", "test"); err != nil {
		t.Fatal(err)
	}
	r, err = New(path)
	if err != nil || len(r.List()) != 2 {
		t.Fatalf("re-keyed entries did not survive reload: %v", err)
	}
}

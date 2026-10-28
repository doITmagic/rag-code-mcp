package contract

import (
	"crypto/sha1"
	"fmt"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestWorkspaceIdentityPreservesPathCase(t *testing.T) {
	upper := filepath.Join(t.TempDir(), "Project")
	lower := filepath.Join(filepath.Dir(upper), "project")
	wantEqual := runtime.GOOS == "windows"
	if equal := DeriveWorkspaceID(upper, "main", upper) == DeriveWorkspaceID(lower, "main", lower); equal != wantEqual {
		t.Fatalf("workspace path equality=%v, want %v", equal, wantEqual)
	}
	if equal := DerivePathContextKey(upper, "main", "abc", upper) == DerivePathContextKey(lower, "main", "abc", lower); equal != wantEqual {
		t.Fatalf("context path equality=%v, want %v", equal, wantEqual)
	}
}

func TestWorkspaceIdentityPreservesGitBranchCase(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("case-distinct Git refs require a case-sensitive filesystem")
	}
	root := t.TempDir()
	git := func(args ...string) string {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-c", "user.name=Test", "-c", "user.email=test@example.invalid", "-c", "commit.gpgsign=false"}, args...)...)
		cmd.Dir = root
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	git("init", "-q")
	git("commit", "-qm", "initial", "--allow-empty")
	git("switch", "-qc", "Feature")
	upper := git("rev-parse", "--abbrev-ref", "HEAD")
	git("switch", "-qc", "feature")
	lower := git("rev-parse", "--abbrev-ref", "HEAD")
	head := git("rev-parse", "HEAD")
	if DeriveWorkspaceID(root, upper, root) == DeriveWorkspaceID(root, lower, root) {
		t.Fatal("case-distinct Git branches share a workspace ID")
	}
	if DerivePathContextKey(root, upper, head, root) == DerivePathContextKey(root, lower, head, root) {
		t.Fatal("case-distinct Git branches share a context key")
	}
}

func TestWorkspaceIdentityDoesNotReuseLegacyCollections(t *testing.T) {
	root := filepath.Clean("/workspace/project")
	legacyWorkspace := fmt.Sprintf("%x", sha1.Sum([]byte(strings.ToLower(root)+"|main|"+strings.ToLower(root))))
	legacyContext := fmt.Sprintf("%x", sha1.Sum([]byte(strings.ToLower(root)+"|main|abc|"+strings.ToLower(root))))
	if DeriveWorkspaceID(root, "main", root) == legacyWorkspace || DerivePathContextKey(root, "main", "abc", root) == legacyContext {
		t.Fatal("new identities reuse possibly contaminated case-folded data")
	}
}

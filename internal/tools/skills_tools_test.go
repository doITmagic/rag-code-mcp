package tools

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/doITmagic/rag-code-mcp/internal/skills"
	"github.com/doITmagic/rag-code-mcp/internal/workspace"
)

func restoreSkillToolStubs() func() {
	origInstall := installSkillFunc
	origUninstall := uninstallSkillFunc
	origList := listAvailableSkillsFunc
	origIsInstalled := isSkillInstalledFunc
	return func() {
		installSkillFunc = origInstall
		uninstallSkillFunc = origUninstall
		listAvailableSkillsFunc = origList
		isSkillInstalledFunc = origIsInstalled
	}
}

func newTestWorkspaceManager(t *testing.T) (*workspace.Manager, *workspace.Info) {
	t.Helper()
	regDir := t.TempDir()
	cache := workspace.NewCache(time.Minute)
	mgr := workspace.NewManagerForTest(cache, regDir)
	info := &workspace.Info{Root: regDir, ID: "ws-test"}
	if err := mgr.GetRegistry().Register(info); err != nil {
		t.Fatalf("failed to register test workspace: %v", err)
	}
	return mgr, info
}

func TestInstallSkillTool_InstallSuccess(t *testing.T) {
	defer restoreSkillToolStubs()()
	mgr, info := newTestWorkspaceManager(t)

	var called bool
	installSkillFunc = func(skillID string, root string) error {
		called = true
		if skillID != "go-best-practices" {
			t.Fatalf("unexpected skill id %s", skillID)
		}
		if root != info.Root {
			t.Fatalf("unexpected root %s", root)
		}
		return nil
	}

	tool := NewInstallSkillTool(mgr)
	msg, err := tool.Execute(context.Background(), map[string]interface{}{
		"skill_id": "go-best-practices",
		"active":   true,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !called {
		t.Fatalf("expected installSkillFunc to be invoked")
	}
	if !strings.Contains(msg, "successfully installed") {
		t.Fatalf("unexpected success message: %s", msg)
	}
}

func TestInstallSkillTool_UninstallError(t *testing.T) {
	defer restoreSkillToolStubs()()
	mgr, _ := newTestWorkspaceManager(t)

	uninstallSkillFunc = func(skillID string, root string) error {
		return errors.New("oops")
	}

	tool := NewInstallSkillTool(mgr)
	_, err := tool.Execute(context.Background(), map[string]interface{}{
		"skill_id": "ragcode-priority",
		"active":   false,
	})
	if err == nil || !strings.Contains(err.Error(), "failed to uninstall") {
		t.Fatalf("expected uninstall error, got %v", err)
	}
}

func TestInstallSkillTool_WorkspaceResolveError(t *testing.T) {
	defer restoreSkillToolStubs()()
	cache := workspace.NewCache(time.Minute)
	mgr := workspace.NewManagerForTest(cache, "") // empty registry

	tool := NewInstallSkillTool(mgr)
	_, err := tool.Execute(context.Background(), map[string]interface{}{
		"skill_id": "anything",
		"active":   true,
	})
	if err == nil {
		t.Fatalf("expected workspace resolution error")
	}
}

func TestListSkillsTool_WithWorkspace(t *testing.T) {
	defer restoreSkillToolStubs()()
	mgr, info := newTestWorkspaceManager(t)

	listAvailableSkillsFunc = func() ([]skills.SkillInfo, error) {
		return []skills.SkillInfo{
			{ID: "skill-a", Name: "Skill A"},
			{ID: "skill-b", Name: "Skill B"},
		}, nil
	}
	isSkillInstalledFunc = func(skillID, root string) bool {
		if root != info.Root {
			t.Fatalf("unexpected root passed to isSkillInstalled")
		}
		return skillID == "skill-a"
	}

	tool := NewListSkillsTool(mgr)
	out, err := tool.Execute(context.Background(), map[string]interface{}{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, "Detected Workspace") {
		t.Fatalf("expected workspace header, got %s", out)
	}
	if !strings.Contains(out, "\"skill-a\"") || !strings.Contains(out, "\"skill-b\"") {
		t.Fatalf("output missing skills: %s", out)
	}
	if !strings.Contains(out, "\"installed\": true") || !strings.Contains(out, "\"installed\": false") {
		t.Fatalf("output missing installed statuses: %s", out)
	}
}

func TestListSkillsTool_ListError(t *testing.T) {
	defer restoreSkillToolStubs()()
	mgr, _ := newTestWorkspaceManager(t)

	listAvailableSkillsFunc = func() ([]skills.SkillInfo, error) {
		return nil, errors.New("fail list")
	}

	tool := NewListSkillsTool(mgr)
	if _, err := tool.Execute(context.Background(), nil); err == nil {
		t.Fatalf("expected error from list skills")
	}
}

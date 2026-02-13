package tools

import (
	"context"
	"fmt"

	"github.com/doITmagic/rag-code-mcp/internal/skills"
	"github.com/doITmagic/rag-code-mcp/internal/workspace"
)

var (
	installSkillFunc   = skills.InstallSkill
	uninstallSkillFunc = skills.UninstallSkill
)

type InstallSkillTool struct {
	workspaceManager *workspace.Manager
}

func NewInstallSkillTool(workspaceManager *workspace.Manager) *InstallSkillTool {
	return &InstallSkillTool{
		workspaceManager: workspaceManager,
	}
}

func (t *InstallSkillTool) Name() string {
	return "install_skill"
}

func (t *InstallSkillTool) Description() string {
	return `Installs or uninstalls an AI skill to/from the current workspace. 
Param 'skill_id' is the unique identifier of the skill (e.g., 'go-best-practices', 'ragcode-priority').
Param 'active' (bool) determines if it should be installed (true) or removed (false). This flag is MANDATORY; omitting it will return an error even for installs.
Param 'file_path' (string) is HIGHLY RECOMMENDED to help detect the correct workspace root.

EXAMPLES:
- mcp_ragcode_install_skill(skill_id="go-best-practices", active=true, file_path="/path/to/project/go.mod")
- mcp_ragcode_install_skill(skill_id="ragcode-priority", active=true, file_path="/path/to/project/README.md")`
}

func (t *InstallSkillTool) Execute(ctx context.Context, args map[string]interface{}) (string, error) {
	active, ok := args["active"].(bool)
	if !ok {
		return "", fmt.Errorf("parameter 'active' (boolean) is required")
	}

	skillID, ok := args["skill_id"].(string)
	if !ok || skillID == "" {
		return "", fmt.Errorf("parameter 'skill_id' is required")
	}

	// Resolve workspace from registry
	workspaceInfo, err := t.workspaceManager.ResolveWorkspace(args)
	if err != nil {
		return "", fmt.Errorf("could not resolve workspace for skill installation: %w", err)
	}

	workspaceRoot := workspaceInfo.Root

	if active {
		err = installSkillFunc(skillID, workspaceRoot)
		if err != nil {
			return "", fmt.Errorf("failed to install skill %s: %w", skillID, err)
		}
		return fmt.Sprintf("✅ Skill '%s' has been successfully installed in %s/.agent/skills/%s", skillID, workspaceRoot, skillID), nil
	} else {
		err = uninstallSkillFunc(skillID, workspaceRoot)
		if err != nil {
			return "", fmt.Errorf("failed to uninstall skill %s: %w", skillID, err)
		}
		return fmt.Sprintf("🗑️ Skill '%s' has been removed from %s", skillID, workspaceRoot), nil
	}
}

func (t *InstallSkillTool) SetWorkspaceManager(m *workspace.Manager) {
	t.workspaceManager = m
}

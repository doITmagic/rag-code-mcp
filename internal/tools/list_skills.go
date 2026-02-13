package tools

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/doITmagic/rag-code-mcp/internal/skills"
	"github.com/doITmagic/rag-code-mcp/internal/workspace"
)

var (
	listAvailableSkillsFunc = skills.ListAvailableSkills
	isSkillInstalledFunc    = skills.IsSkillInstalled
)

type ListSkillsTool struct {
	workspaceManager *workspace.Manager
}

func NewListSkillsTool(workspaceManager *workspace.Manager) *ListSkillsTool {
	return &ListSkillsTool{workspaceManager: workspaceManager}
}

func (t *ListSkillsTool) Name() string {
	return "rag_list_skills"
}

func (t *ListSkillsTool) Description() string {
	return "Lists all available AI skills bundled within the ragcode binary. These skills can be installed to help the AI better understand the project using ragcode tools."
}

func (t *ListSkillsTool) Execute(ctx context.Context, args map[string]interface{}) (string, error) {
	available, err := listAvailableSkillsFunc()
	if err != nil {
		return "", fmt.Errorf("failed to list skills: %w", err)
	}

	if len(available) == 0 {
		return "No skills found in the binary.", nil
	}

	workspaceRoot := t.detectWorkspaceRoot(ctx, args)

	type SkillWithStatus struct {
		skills.SkillInfo
		Installed bool `json:"installed"`
	}

	var extendedList []SkillWithStatus
	for _, s := range available {
		installed := false
		if workspaceRoot != "" {
			installed = isSkillInstalledFunc(s.ID, workspaceRoot)
		}
		extendedList = append(extendedList, SkillWithStatus{
			SkillInfo: s,
			Installed: installed,
		})
	}

	data, err := json.MarshalIndent(extendedList, "", "  ")
	if err != nil {
		return "", fmt.Errorf("failed to format skills list: %w", err)
	}

	output := string(data)
	if workspaceRoot != "" {
		output = fmt.Sprintf("🌍 Detected Workspace: %s\n\n%s", workspaceRoot, output)
	} else {
		output = "⚠️  Warning: Could not detect active workspace. 'installed' status may be inaccurate.\nProvide 'file_path' argument to detect workspace.\n\n" + output
	}

	output += "\n\n---\n💡 Tip:\n"
	output += "This command lists skills bundled in the current ragcode binary.\n"
	output += "Use 'rag_install_skill' with one of the listed IDs to install/uninstall a skill in this workspace."

	return output, nil
}

func (t *ListSkillsTool) SetWorkspaceManager(m *workspace.Manager) {
	t.workspaceManager = m
}

func (t *ListSkillsTool) detectWorkspaceRoot(ctx context.Context, args map[string]interface{}) string {
	if t.workspaceManager == nil {
		return ""
	}
	info, err := t.workspaceManager.DetectWorkspace(args)
	if err != nil || info == nil {
		return ""
	}
	return info.Root
}

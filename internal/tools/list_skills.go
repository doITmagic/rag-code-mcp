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

	output += "\n\n---\n💡 Want to add your own skill?\n"
	output += "Create a folder in .agent/skills/ (or define RAGCODE_SKILLS_PATH) with a SKILL.md file.\n"
	output += "IMPORTANT: You MUST include 'compatible-with: [rag-code-mcp]' in the frontmatter!\n\n"
	output += "Example SKILL.md:\n"
	output += "```yaml\n"
	output += "---\n"
	output += "name: my-new-skill\n"
	output += "description: Description of what this skill does\n"
	output += "compatible-with: [rag-code-mcp]\n"
	output += "---\n\n"
	output += "# My New Skill\n"
	output += "Instructions for the AI...\n"
	output += "```"

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

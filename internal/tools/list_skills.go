package tools

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/doITmagic/rag-code-mcp/internal/skills"
)

type ListSkillsTool struct{}

func NewListSkillsTool() *ListSkillsTool {
	return &ListSkillsTool{}
}

func (t *ListSkillsTool) Name() string {
	return "rag_list_skills"
}

func (t *ListSkillsTool) Description() string {
	return "Lists all available AI skills bundled within the ragcode binary. These skills can be installed to help the AI better understand the project using ragcode tools."
}

func (t *ListSkillsTool) Execute(ctx context.Context, args map[string]interface{}) (string, error) {
	available, err := skills.ListAvailableSkills()
	if err != nil {
		return "", fmt.Errorf("failed to list skills: %w", err)
	}

	if len(available) == 0 {
		return "No skills found in the binary.", nil
	}

	data, err := json.MarshalIndent(available, "", "  ")
	if err != nil {
		return "", fmt.Errorf("failed to format skills list: %w", err)
	}

	return string(data), nil
}

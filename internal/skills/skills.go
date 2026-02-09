package skills

import (
	"embed"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

//go:embed embedded/*
var embeddedSkills embed.FS

// SkillInfo holds the metadata of an embedded skill
type SkillInfo struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
}

// ListAvailableSkills scans the embedded filesystem for skills and extracts metadata from SKILL.md
func ListAvailableSkills() ([]SkillInfo, error) {
	var available []SkillInfo

	entries, err := embeddedSkills.ReadDir("embedded")
	if err != nil {
		return nil, fmt.Errorf("failed to read embedded skills: %w", err)
	}

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}

		skillID := entry.Name()
		info, err := GetSkillMetadata(skillID)
		if err != nil {
			// Skip skills with invalid metadata but log or handle as needed
			continue
		}
		available = append(available, info)
	}

	return available, nil
}

// GetSkillMetadata extracts metadata from the SKILL.md of a specific skill
func GetSkillMetadata(skillID string) (SkillInfo, error) {
	filePath := fmt.Sprintf("embedded/%s/SKILL.md", skillID)
	content, err := embeddedSkills.ReadFile(filePath)
	if err != nil {
		return SkillInfo{}, fmt.Errorf("failed to read SKILL.md for %s: %w", skillID, err)
	}

	// Basic parsing of YAML frontmatter
	parts := strings.Split(string(content), "---")
	if len(parts) < 3 {
		return SkillInfo{}, fmt.Errorf("invalid frontmatter in SKILL.md for %s", skillID)
	}

	var metadata struct {
		Name        string `yaml:"name"`
		Description string `yaml:"description"`
	}

	if err := yaml.Unmarshal([]byte(parts[1]), &metadata); err != nil {
		return SkillInfo{}, fmt.Errorf("failed to parse metadata for %s: %w", skillID, err)
	}

	return SkillInfo{
		ID:          skillID,
		Name:        metadata.Name,
		Description: metadata.Description,
	}, nil
}

// InstallSkill copies all files of an embedded skill to the destination directory
func InstallSkill(skillID string, workspaceRoot string) error {
	destDir := filepath.Join(workspaceRoot, ".agent", "skills", skillID)
	srcDir := "embedded/" + skillID

	return fs.WalkDir(embeddedSkills, srcDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		// Calculate relative path from skill root
		relPath, err := filepath.Rel(srcDir, path)
		if err != nil {
			return err
		}

		targetPath := filepath.Join(destDir, relPath)

		if d.IsDir() {
			return os.MkdirAll(targetPath, 0755)
		}

		// Read from embed and write to disk
		content, err := embeddedSkills.ReadFile(path)
		if err != nil {
			return err
		}

		return os.WriteFile(targetPath, content, 0644)
	})
}

// UninstallSkill removes a skill from the workspace
func UninstallSkill(skillID string, workspaceRoot string) error {
	destDir := filepath.Join(workspaceRoot, ".agent", "skills", skillID)
	return os.RemoveAll(destDir)
}

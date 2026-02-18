package skills

import (
	"embed"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"
)

var skillIDRegex = regexp.MustCompile(`^[a-z0-9-]+$`)

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

// validateSkillID ensures the skill ID is safe and matches a strict allowlist
func validateSkillID(skillID string) error {
	if !skillIDRegex.MatchString(skillID) {
		return fmt.Errorf("invalid skill ID: must be lowercase alphanumeric with hyphens (e.g. 'my-skill')")
	}
	return nil
}

// InstallSkill copies all files of an embedded skill to the destination directory
func InstallSkill(skillID string, workspaceRoot string) error {
	if err := validateSkillID(skillID); err != nil {
		return err
	}

	destDir := filepath.Join(workspaceRoot, ".agent", "skills", skillID)
	if IsSkillInstalled(skillID, workspaceRoot) {
		return fmt.Errorf("skill '%s' is already installed at %s", skillID, destDir)
	}
	// Additional safety check: ensure destDir is within workspaceRoot
	// Note: This might be redundant with validateSkillID but good for defense in depth
	// However, workspaceRoot might be relative or absolute, so we rely on skillID validation primarily.

	srcDir := "embedded/" + skillID

	// Verify the skill exists in embedded FS before trying to walk
	if _, err := embeddedSkills.ReadDir(srcDir); err != nil {
		return fmt.Errorf("skill '%s' not found in embedded library", skillID)
	}

	return fs.WalkDir(embeddedSkills, srcDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		// Calculate relative path from skill root
		relPath, err := filepath.Rel(srcDir, path)
		if err != nil {
			return err
		}

		// Join with destination directory
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
	if err := validateSkillID(skillID); err != nil {
		return err
	}
	destDir := filepath.Join(workspaceRoot, ".agent", "skills", skillID)
	return os.RemoveAll(destDir)
}

// IsSkillInstalled checks whether a skill folder exists within the workspace
func IsSkillInstalled(skillID, workspaceRoot string) bool {
	if err := validateSkillID(skillID); err != nil {
		return false
	}
	path := filepath.Join(workspaceRoot, ".agent", "skills", skillID)
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

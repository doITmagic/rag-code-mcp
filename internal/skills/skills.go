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
	Source      string `json:"source,omitempty"` // "binary" or absolute path to source
}

var externalSkillsPaths []string

// AddExternalSkillsPath adds a directory to be scanned for skills
func AddExternalSkillsPath(path string) {
	if path == "" {
		return
	}
	abs, err := filepath.Abs(path)
	if err == nil {
		path = abs
	}
	// Avoid duplicates
	for _, p := range externalSkillsPaths {
		if p == path {
			return
		}
	}
	externalSkillsPaths = append(externalSkillsPaths, path)
}

// ListAvailableSkills scans the embedded filesystem and external paths for skills
func ListAvailableSkills() ([]SkillInfo, error) {
	var available []SkillInfo
	seen := make(map[string]bool)

	// 1. Scan embedded skills
	entries, err := embeddedSkills.ReadDir("embedded")
	if err == nil {
		for _, entry := range entries {
			if !entry.IsDir() {
				continue
			}
			skillID := entry.Name()
			info, err := GetSkillMetadata(skillID)
			if err == nil {
				info.Source = "binary"
				available = append(available, info)
				seen[skillID] = true
			}
		}
	}

	// 2. Scan external paths recursively
	for _, rootPath := range externalSkillsPaths {
		err := filepath.WalkDir(rootPath, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return nil // Skip unreadable paths
			}

			// We are looking for SKILL.md files
			if d.IsDir() || d.Name() != "SKILL.md" {
				return nil
			}

			// Found a SKILL.md file!
			// The skill ID is the name of the parent directory
			skillDir := filepath.Dir(path)
			skillID := filepath.Base(skillDir)

			if seen[skillID] {
				return nil
			}

			// Get metadata from this specific file path and CHECK COMPATIBILITY
			info, err := GetExternalSkillMetadataFromFile(path, skillID)
			if err == nil {
				info.Source = skillDir
				available = append(available, info)
				seen[skillID] = true
			}
			return nil
		})
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error scanning external skills path %s: %v\n", rootPath, err)
		}
	}

	return available, nil
}

// GetExternalSkillMetadataFromFile extracts metadata from a specific SKILL.md file path
func GetExternalSkillMetadataFromFile(filePath, skillID string) (SkillInfo, error) {
	content, err := os.ReadFile(filePath)
	if err != nil {
		return SkillInfo{}, fmt.Errorf("failed to read SKILL.md from %s: %w", filePath, err)
	}

	parts := strings.Split(string(content), "---")
	if len(parts) < 3 {
		return SkillInfo{}, fmt.Errorf("invalid frontmatter in SKILL.md for %s", skillID)
	}

	var metadata struct {
		Name           string   `yaml:"name"`
		Description    string   `yaml:"description"`
		CompatibleWith []string `yaml:"compatible-with"`
	}

	if err := yaml.Unmarshal([]byte(parts[1]), &metadata); err != nil {
		return SkillInfo{}, fmt.Errorf("failed to parse metadata for %s: %w", skillID, err)
	}

	// Filter: Check compatibility
	isCompatible := false
	for _, comp := range metadata.CompatibleWith {
		if comp == "rag-code-mcp" {
			isCompatible = true
			break
		}
	}
	if !isCompatible {
		return SkillInfo{}, fmt.Errorf("skill %s is not compatible with rag-code-mcp", skillID)
	}

	return SkillInfo{
		ID:          skillID,
		Name:        metadata.Name,
		Description: metadata.Description,
	}, nil
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
		Name           string   `yaml:"name"`
		Description    string   `yaml:"description"`
		CompatibleWith []string `yaml:"compatible-with"`
	}

	if err := yaml.Unmarshal([]byte(parts[1]), &metadata); err != nil {
		return SkillInfo{}, fmt.Errorf("failed to parse metadata for %s: %w", skillID, err)
	}

	// Filter: Check compatibility
	isCompatible := false
	for _, comp := range metadata.CompatibleWith {
		if comp == "rag-code-mcp" {
			isCompatible = true
			break
		}
	}
	if !isCompatible {
		return SkillInfo{}, fmt.Errorf("skill %s is not compatible with rag-code-mcp (missing 'compatible-with: [rag-code-mcp]')", skillID)
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
	// Additional safety check: ensure destDir is within workspaceRoot
	// Note: This might be redundant with validateSkillID but good for defense in depth
	// However, workspaceRoot might be relative or absolute, so we rely on skillID validation primarily.

	// Try embedded first
	srcDir := "embedded/" + skillID
	if _, err := embeddedSkills.ReadDir(srcDir); err == nil {
		// Verify compatibility before installing
		if _, err := GetSkillMetadata(skillID); err != nil {
			return fmt.Errorf("skill '%s' is not compatible: %w", skillID, err)
		}

		return fs.WalkDir(embeddedSkills, srcDir, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}

			relPath, err := filepath.Rel(srcDir, path)
			if err != nil {
				return err
			}

			targetPath := filepath.Join(destDir, relPath)

			if d.IsDir() {
				return os.MkdirAll(targetPath, 0755)
			}

			content, err := embeddedSkills.ReadFile(path)
			if err != nil {
				return err
			}

			return os.WriteFile(targetPath, content, 0644)
		})
	}

	// Try external paths RECURSIVELY
	for _, rootPath := range externalSkillsPaths {
		var foundDir string
		var foundPath string

		// Walk to find the skill directory
		_ = filepath.WalkDir(rootPath, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return nil
			}
			// Look for the folder that matches skillID AND contains SKILL.md
			if d.IsDir() && d.Name() == skillID {
				// Verify it has a SKILL.md inside
				skillMdPath := filepath.Join(path, "SKILL.md")
				if _, err := os.Stat(skillMdPath); err == nil {
					foundDir = path
					foundPath = skillMdPath
					return fs.SkipAll // Stop searching once found
				}
			}
			return nil
		})

		if foundDir != "" {
			// Verify compatibility before installing
			if _, err := GetExternalSkillMetadataFromFile(foundPath, skillID); err != nil {
				return fmt.Errorf("skill '%s' found but is not compatible: %w", skillID, err)
			}

			return filepath.Walk(foundDir, func(path string, info os.FileInfo, err error) error {
				if err != nil {
					return err
				}

				relPath, err := filepath.Rel(foundDir, path)
				if err != nil {
					return err
				}

				targetPath := filepath.Join(destDir, relPath)

				if info.IsDir() {
					return os.MkdirAll(targetPath, 0755)
				}

				content, err := os.ReadFile(path)
				if err != nil {
					return err
				}

				return os.WriteFile(targetPath, content, 0644)
			})
		}
	}

	return fmt.Errorf("skill '%s' not found", skillID)
}

// UninstallSkill removes a skill from the workspace
func UninstallSkill(skillID string, workspaceRoot string) error {
	if err := validateSkillID(skillID); err != nil {
		return err
	}
	destDir := filepath.Join(workspaceRoot, ".agent", "skills", skillID)
	return os.RemoveAll(destDir)
}

// IsSkillInstalled checks if a skill is installed in the workspace
func IsSkillInstalled(skillID string, workspaceRoot string) bool {
	destDir := filepath.Join(workspaceRoot, ".agent", "skills", skillID)
	// Check if directory exists and has SKILL.md
	if _, err := os.Stat(filepath.Join(destDir, "SKILL.md")); err == nil {
		return true
	}
	return false
}

package skills

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
)

var skillIDRegex = regexp.MustCompile(`^[a-z0-9-]+$`)

// SkillInfo holds the metadata of a skill, compatible with the remote registry format.
type SkillInfo struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
}

// ListAvailableSkills fetches the list of skills from the remote GitHub registry.
func ListAvailableSkills() ([]SkillInfo, error) {
	registry, err := FetchRemoteRegistry()
	if err != nil {
		return nil, fmt.Errorf("failed to fetch remote skill registry: %w", err)
	}

	result := make([]SkillInfo, 0, len(registry.Skills))
	for _, s := range registry.Skills {
		result = append(result, SkillInfo{
			ID:          s.ID,
			Name:        s.Name,
			Description: s.Description,
		})
	}
	return result, nil
}

// validateSkillID ensures the skill ID is safe and matches a strict allowlist.
func validateSkillID(skillID string) error {
	if !skillIDRegex.MatchString(skillID) {
		return fmt.Errorf("invalid skill ID: must be lowercase alphanumeric with hyphens (e.g. 'my-skill')")
	}
	return nil
}

// skillCandidateDirs returns all standard directories where skills may be placed,
// in priority order. Covers the naming conventions used by rag-code-mcp, GitHub Copilot,
// Claude (Anthropic), and Cursor.
// resolveTargetBaseDir maps a target string to the corresponding skills base directory.
// Valid values: "agent" (default), "agents", "claude", "cursor", "windsurf".
func resolveTargetBaseDir(workspaceRoot, target string) string {
	switch target {
	case "agents":
		return filepath.Join(workspaceRoot, ".agents", "skills")
	case "claude":
		return filepath.Join(workspaceRoot, ".claude", "skills")
	case "cursor":
		return filepath.Join(workspaceRoot, ".cursor", "skills")
	case "windsurf":
		return filepath.Join(workspaceRoot, ".windsurf", "skills")
	default: // "agent" or empty
		return filepath.Join(workspaceRoot, ".agent", "skills")
	}
}

// skillCandidateDirs returns all standard directories where skills may be placed,
// in priority order. Aligned with the workspace detector markers.
func skillCandidateDirs(workspaceRoot string) []string {
	return []string{
		filepath.Join(workspaceRoot, ".agent", "skills"),    // rag-code-mcp / OpenCode / Antigravity
		filepath.Join(workspaceRoot, ".agents", "skills"),   // GitHub Copilot / VS Code
		filepath.Join(workspaceRoot, ".claude", "skills"),   // Claude (Anthropic)
		filepath.Join(workspaceRoot, ".cursor", "skills"),   // Cursor
		filepath.Join(workspaceRoot, ".windsurf", "skills"), // Windsurf (Codeium)
	}
}

// FindSkillPath returns the first directory where the skill is already installed,
// or an empty string if it is not found in any known location.
func FindSkillPath(skillID, workspaceRoot string) string {
	for _, base := range skillCandidateDirs(workspaceRoot) {
		p := filepath.Join(base, skillID)
		info, err := os.Stat(p)
		if err == nil && info.IsDir() {
			return p
		}
	}
	return ""
}

// InstallSkill downloads a skill from the remote GitHub registry and installs it
// into the target tool directory within the workspace.
// target can be: "agent" (default), "agents", "claude", "cursor".
func InstallSkill(skillID string, workspaceRoot string, target string) error {
	if err := validateSkillID(skillID); err != nil {
		return err
	}

	// Check all known locations before installing
	if existing := FindSkillPath(skillID, workspaceRoot); existing != "" {
		return fmt.Errorf("skill '%s' is already installed at %s", skillID, existing)
	}

	// Fetch registry to resolve the skill's path in the repo
	registry, err := FetchRemoteRegistry()
	if err != nil {
		return fmt.Errorf("failed to fetch registry: %w", err)
	}

	var skillPath string
	for _, s := range registry.Skills {
		if s.ID == skillID {
			skillPath = s.Path
			break
		}
	}
	if skillPath == "" {
		return fmt.Errorf("skill '%s' not found in registry", skillID)
	}

	// Resolve destination based on the target tool directory
	destDir := filepath.Join(resolveTargetBaseDir(workspaceRoot, target), skillID)

	if err := downloadSkillFromGitHub(skillPath, destDir); err != nil {
		// Clean up partial installation on failure
		_ = os.RemoveAll(destDir)
		return fmt.Errorf("failed to download skill '%s': %w", skillID, err)
	}

	return nil
}

// UninstallSkill removes a skill from all known locations in the workspace
// (.agent/, .agents/, .claude/, .cursor/).
func UninstallSkill(skillID string, workspaceRoot string) error {
	if err := validateSkillID(skillID); err != nil {
		return err
	}
	var lastErr error
	removed := 0
	for _, base := range skillCandidateDirs(workspaceRoot) {
		p := filepath.Join(base, skillID)
		if info, err := os.Stat(p); err == nil && info.IsDir() {
			if err := os.RemoveAll(p); err != nil {
				lastErr = err
			} else {
				removed++
			}
		}
	}
	if removed == 0 && lastErr == nil {
		return fmt.Errorf("skill '%s' not found in any known location", skillID)
	}
	return lastErr
}

// IsSkillInstalled checks whether a skill folder exists in any of the known
// standard locations (.agent/, .agents/, .claude/, .cursor/).
func IsSkillInstalled(skillID, workspaceRoot string) bool {
	if err := validateSkillID(skillID); err != nil {
		return false
	}
	return FindSkillPath(skillID, workspaceRoot) != ""
}

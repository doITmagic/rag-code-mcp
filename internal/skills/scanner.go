package skills

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"path"
	"strings"
	"time"

	"github.com/doITmagic/rag-code-mcp/internal/config"
	"github.com/doITmagic/rag-code-mcp/internal/logger"
)

// DiscoveredSkill represents a skill found by scanning a GitHub repo tree.
type DiscoveredSkill struct {
	ID          string `json:"id"`          // folder name, e.g. "docx"
	Name        string `json:"name"`        // from SKILL.md frontmatter (or ID if missing)
	Description string `json:"description"` // from SKILL.md frontmatter
	Source      string `json:"source"`      // "owner/repo"
	RepoPath    string `json:"repo_path"`   // full path in repo, e.g. "skills/docx"
	Branch      string `json:"branch"`      // branch name
	HasScripts  bool   `json:"has_scripts"` // true if scripts/ subfolder exists
}

// gitHubTreeEntry is a single node in the GitHub Git Tree API response.
type gitHubTreeEntry struct {
	Path string `json:"path"`
	Mode string `json:"mode"`
	Type string `json:"type"` // "blob" or "tree"
	SHA  string `json:"sha"`
	Size int    `json:"size,omitempty"`
}

// gitHubTreeResponse is the top-level response from the Git Tree API.
type gitHubTreeResponse struct {
	SHA       string            `json:"sha"`
	Tree      []gitHubTreeEntry `json:"tree"`
	Truncated bool              `json:"truncated"`
}

// scanHTTPClient is a package-level client for scanner HTTP requests.
var scanHTTPClient = &http.Client{Timeout: 30 * time.Second}

// ScanRepo uses the GitHub Git Tree API (recursive, single call) to discover
// all skills in a repository. A skill is any folder under skillsPath that
// contains a SKILL.md file.
func ScanRepo(repo config.SkillRepoConfig) ([]DiscoveredSkill, error) {
	parts := strings.SplitN(repo.Repo, "/", 2)
	if len(parts) != 2 {
		return nil, fmt.Errorf("invalid repo format %q: expected owner/repo", repo.Repo)
	}
	owner, repoName := parts[0], parts[1]

	branch := repo.Branch
	if branch == "" {
		branch = "main"
	}

	// Single API call to fetch the full recursive tree.
	treeURL := fmt.Sprintf(
		"https://api.github.com/repos/%s/%s/git/trees/%s?recursive=1",
		owner, repoName, branch,
	)

	req, err := http.NewRequest("GET", treeURL, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "rag-code-mcp/1.0")

	resp, err := scanHTTPClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch tree for %s: %w", repo.Repo, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GitHub tree API returned status %d for %s", resp.StatusCode, repo.Repo)
	}

	var tree gitHubTreeResponse
	if err := json.NewDecoder(resp.Body).Decode(&tree); err != nil {
		return nil, fmt.Errorf("failed to decode tree response: %w", err)
	}

	// Normalise skills path: "skills" → "skills/", "" or "." → ""
	prefix := strings.TrimRight(repo.SkillsPath, "/")
	if prefix == "." {
		prefix = ""
	}

	// Pass 1: find all SKILL.md files and scripts/ directories.
	skillMDs := make(map[string]string) // parent folder → full path to SKILL.md
	scriptDirs := make(map[string]bool) // parent folder of scripts/ dir

	for _, entry := range tree.Tree {
		if entry.Type == "blob" && path.Base(entry.Path) == "SKILL.md" {
			parentDir := path.Dir(entry.Path)
			// Check if parentDir is directly under the skills prefix.
			// We want skills at any depth under prefix.
			if prefix == "" || strings.HasPrefix(parentDir, prefix+"/") || parentDir == prefix {
				skillMDs[parentDir] = entry.Path
			}
		}
		if entry.Type == "tree" && path.Base(entry.Path) == "scripts" {
			scriptDirs[path.Dir(entry.Path)] = true
		}
	}

	// Build results.
	skills := make([]DiscoveredSkill, 0, len(skillMDs))
	for dir, mdPath := range skillMDs {
		// Skill ID is the immediate folder name.
		skillID := path.Base(dir)

		skills = append(skills, DiscoveredSkill{
			ID:         skillID,
			Name:       skillID, // Will be enriched later from frontmatter
			Source:     repo.Repo,
			RepoPath:   dir,
			Branch:     branch,
			HasScripts: scriptDirs[dir],
		})
		_ = mdPath // kept for clarity; used later in enrichment
	}

	return skills, nil
}

// ScanAllRepos scans all enabled repos from config and returns a merged list.
func ScanAllRepos(repos []config.SkillRepoConfig) []DiscoveredSkill {
	var all []DiscoveredSkill
	for _, repo := range repos {
		if !repo.Enabled {
			continue
		}
		skills, err := ScanRepo(repo)
		if err != nil {
			logger.Instance.Warn("Failed to scan skill repo %s: %v", repo.Repo, err)
			continue
		}
		all = append(all, skills...)
	}
	return all
}

// EnrichSkillsMetadata fetches SKILL.md frontmatter for each skill
// to populate Name and Description fields. This issues one HTTP request
// per skill but only runs when cache is expired (typically every 24h).
func EnrichSkillsMetadata(skills []DiscoveredSkill) {
	for i := range skills {
		s := &skills[i]
		name, desc := fetchSkillFrontmatter(s.Source, s.Branch, s.RepoPath)
		if name != "" {
			s.Name = name
		}
		if desc != "" {
			s.Description = desc
		}
	}
}

// fetchSkillFrontmatter downloads SKILL.md from GitHub and extracts
// the YAML frontmatter fields "name" and "description".
func fetchSkillFrontmatter(repo, branch, repoPath string) (name, description string) {
	url := fmt.Sprintf(
		"https://raw.githubusercontent.com/%s/%s/%s/SKILL.md",
		repo, branch, repoPath,
	)

	resp, err := scanHTTPClient.Get(url)
	if err != nil || resp.StatusCode != http.StatusOK {
		return "", ""
	}
	defer resp.Body.Close()

	return parseYAMLFrontmatter(resp.Body)
}

// parseYAMLFrontmatter extracts "name" and "description" from the YAML
// frontmatter block (between --- delimiters) of a SKILL.md file.
// It reads only the first ~50 lines to stay lightweight.
func parseYAMLFrontmatter(r io.Reader) (name, description string) {
	scanner := bufio.NewScanner(r)
	inFrontmatter := false
	lineCount := 0

	for scanner.Scan() {
		lineCount++
		if lineCount > 50 {
			break // frontmatter should be in the first few lines
		}

		line := scanner.Text()
		trimmed := strings.TrimSpace(line)

		if trimmed == "---" {
			if !inFrontmatter {
				inFrontmatter = true
				continue
			}
			break // end of frontmatter
		}

		if !inFrontmatter {
			continue
		}

		// Simple key: value parsing (no nested YAML)
		if strings.HasPrefix(trimmed, "name:") {
			name = strings.TrimSpace(strings.TrimPrefix(trimmed, "name:"))
			name = strings.Trim(name, "\"'")
		}
		if strings.HasPrefix(trimmed, "description:") {
			description = strings.TrimSpace(strings.TrimPrefix(trimmed, "description:"))
			description = strings.Trim(description, "\"'")
		}
	}

	return name, description
}

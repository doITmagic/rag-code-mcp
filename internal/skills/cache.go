package skills

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/doITmagic/rag-code-mcp/internal/config"
	"github.com/doITmagic/rag-code-mcp/internal/logger"
)

// SkillsCache holds the cached skill discovery results.
type SkillsCache struct {
	LastFetch time.Time         `json:"last_fetch"`
	TTL       string            `json:"ttl"` // duration string, e.g. "24h"
	Skills    []DiscoveredSkill `json:"skills"`
}

// cacheFilePath returns the path to the skills cache file.
// Stored alongside the ragcode binary in ~/.local/share/ragcode/.
func cacheFilePath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return filepath.Join(os.TempDir(), "ragcode_skills_cache.json")
	}
	return filepath.Join(home, ".local", "share", "ragcode", "skills_cache.json")
}

// loadCache reads the cache from disk. Returns nil if cache is missing or invalid.
func loadCache() *SkillsCache {
	data, err := os.ReadFile(cacheFilePath())
	if err != nil {
		return nil
	}
	var cache SkillsCache
	if err := json.Unmarshal(data, &cache); err != nil {
		return nil
	}
	return &cache
}

// saveCache writes the cache to disk.
func saveCache(cache *SkillsCache) error {
	cachePath := cacheFilePath()
	if err := os.MkdirAll(filepath.Dir(cachePath), 0755); err != nil {
		return fmt.Errorf("failed to create cache directory: %w", err)
	}
	data, err := json.MarshalIndent(cache, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal cache: %w", err)
	}
	return os.WriteFile(cachePath, data, 0644)
}

// isCacheValid checks if the cache is still fresh based on TTL.
func isCacheValid(cache *SkillsCache, ttl time.Duration) bool {
	if cache == nil || len(cache.Skills) == 0 {
		return false
	}
	if ttl == 0 {
		return false // caching disabled
	}
	return time.Since(cache.LastFetch) < ttl
}

// ListAllSkills returns all skills from all enabled repos, using cache when available.
// This is the main entry point replacing the old ListAvailableSkills().
func ListAllSkills(cfg config.SkillsConfig) []DiscoveredSkill {
	cache := loadCache()
	if isCacheValid(cache, cfg.CacheTTL) {
		logger.Instance.Debug("Skills cache hit (%d skills, age %v)", len(cache.Skills), time.Since(cache.LastFetch))
		return cache.Skills
	}

	logger.Instance.Info("Scanning %d skill repos for available skills...", len(cfg.Repos))
	skills := ScanAllRepos(cfg.Repos)

	// Enrich with name/description from SKILL.md frontmatter
	EnrichSkillsMetadata(skills)

	// Save to cache
	newCache := &SkillsCache{
		LastFetch: time.Now(),
		TTL:       cfg.CacheTTL.String(),
		Skills:    skills,
	}
	if err := saveCache(newCache); err != nil {
		logger.Instance.Warn("Failed to save skills cache: %v", err)
	} else {
		logger.Instance.Info("Cached %d skills from %d repos", len(skills), len(cfg.Repos))
	}

	return skills
}

// FindSkillByID looks up a skill by ID across all cached/discovered skills.
func FindSkillByID(skillID string, cfg config.SkillsConfig) *DiscoveredSkill {
	skills := ListAllSkills(cfg)
	for i := range skills {
		if skills[i].ID == skillID {
			return &skills[i]
		}
	}
	return nil
}

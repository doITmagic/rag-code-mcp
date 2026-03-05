package utils

import (
	"os"
	"path/filepath"
	"runtime"
	"sync"
)

// pathOverrides holds config-driven overrides for global application paths.
// Set once after config loads via ApplyPathOverrides; read concurrently by
// all path accessor functions.
var (
	overrideMu          sync.RWMutex
	overrideLogDir      string
	overrideRegistry    string
	overrideSkillsCache string
	overrideUpdateCache string
)

// ApplyPathOverrides sets config-driven overrides for global paths.
// Empty strings are ignored (keep default). Call once after config load.
func ApplyPathOverrides(logsDir, registry, skillsCache, updateCache string) {
	overrideMu.Lock()
	defer overrideMu.Unlock()
	overrideLogDir = logsDir
	overrideRegistry = registry
	overrideSkillsCache = skillsCache
	overrideUpdateCache = updateCache
}

// RagCodeHome returns the root directory for all ragcode application data.
// All files (config, logs, cache, registry) live under this single directory.
//
//	Linux/macOS: ~/.ragcode/
//	Windows:     %LOCALAPPDATA%\ragcode\
func RagCodeHome() string {
	switch runtime.GOOS {
	case "windows":
		base := os.Getenv("LOCALAPPDATA")
		if base == "" {
			home, err := os.UserHomeDir()
			if err != nil {
				return "ragcode"
			}
			base = filepath.Join(home, "AppData", "Local")
		}
		return filepath.Join(base, "ragcode")
	default: // linux, darwin, etc.
		home, err := os.UserHomeDir()
		if err != nil {
			return ".ragcode"
		}
		return filepath.Join(home, ".ragcode")
	}
}

// GetRegistryPath returns the absolute path to the workspace registry file.
func GetRegistryPath() string {
	overrideMu.RLock()
	v := overrideRegistry
	overrideMu.RUnlock()
	if v != "" {
		return v
	}
	return filepath.Join(RagCodeHome(), "registry.json")
}

// GetLogDir returns the directory for ragcode log files.
func GetLogDir() string {
	overrideMu.RLock()
	v := overrideLogDir
	overrideMu.RUnlock()
	if v != "" {
		return v
	}
	return filepath.Join(RagCodeHome(), "logs")
}

// GetSkillsCachePath returns the absolute path to the skills cache file.
func GetSkillsCachePath() string {
	overrideMu.RLock()
	v := overrideSkillsCache
	overrideMu.RUnlock()
	if v != "" {
		return v
	}
	return filepath.Join(RagCodeHome(), "skills_cache.json")
}

// GetUpdateCachePath returns the absolute path to the update cache file.
func GetUpdateCachePath() string {
	overrideMu.RLock()
	v := overrideUpdateCache
	overrideMu.RUnlock()
	if v != "" {
		return v
	}
	return filepath.Join(RagCodeHome(), "update_cache.json")
}

// GetConfigDir returns the ragcode configuration directory (same as RagCodeHome).
func GetConfigDir() string {
	return RagCodeHome()
}

// GetCacheDir returns the directory for ragcode cache files.
func GetCacheDir() string {
	return RagCodeHome()
}

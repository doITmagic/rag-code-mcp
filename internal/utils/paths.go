package utils

import (
	"os"
	"path/filepath"
	"runtime"
)

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
	return filepath.Join(RagCodeHome(), "registry.json")
}

// GetConfigDir returns the ragcode configuration directory (same as RagCodeHome).
func GetConfigDir() string {
	return RagCodeHome()
}

// GetLogDir returns the directory for ragcode log files.
func GetLogDir() string {
	return filepath.Join(RagCodeHome(), "logs")
}

// GetCacheDir returns the directory for ragcode cache files.
func GetCacheDir() string {
	return RagCodeHome()
}

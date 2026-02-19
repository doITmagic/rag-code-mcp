package utils

import (
	"os"
	"path/filepath"
)

// GetRegistryPath returns the default absolute path to the workspace registry.
func GetRegistryPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		// Fallback for systems without a home directory
		return ".ragcode-registry.json"
	}
	return filepath.Join(home, ".ragcode", "registry.json")
}

// GetConfigDir returns the default configuration directory.
func GetConfigDir() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".ragcode")
}

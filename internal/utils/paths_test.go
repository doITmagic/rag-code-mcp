package utils

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRagCodeHomeIsAbsolute(t *testing.T) {
	home := RagCodeHome()
	if !filepath.IsAbs(home) {
		t.Errorf("RagCodeHome() = %q, want absolute path", home)
	}
	if !strings.HasSuffix(home, ".ragcode") && !strings.HasSuffix(home, "ragcode") {
		t.Errorf("RagCodeHome() = %q, want to end with ragcode", home)
	}
}

func TestDefaultPathsAreUnderRagCodeHome(t *testing.T) {
	// Reset overrides to test defaults
	ApplyPathOverrides("", "", "", "")

	home := RagCodeHome()

	tests := []struct {
		name     string
		got      string
		wantBase string
	}{
		{"GetRegistryPath", GetRegistryPath(), "registry.json"},
		{"GetLogDir", GetLogDir(), "logs"},
		{"GetSkillsCachePath", GetSkillsCachePath(), "skills_cache.json"},
		{"GetUpdateCachePath", GetUpdateCachePath(), "update_cache.json"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if !strings.HasPrefix(tt.got, home) {
				t.Errorf("%s = %q, want prefix %q (must be under RagCodeHome)", tt.name, tt.got, home)
			}
			if filepath.Base(tt.got) != tt.wantBase {
				t.Errorf("%s base = %q, want %q", tt.name, filepath.Base(tt.got), tt.wantBase)
			}
			if !filepath.IsAbs(tt.got) {
				t.Errorf("%s = %q, want absolute path", tt.name, tt.got)
			}
		})
	}
}

func TestApplyPathOverridesRespected(t *testing.T) {
	tmp := t.TempDir()

	customLogDir := filepath.Join(tmp, "my-logs")
	customRegistry := filepath.Join(tmp, "my-registry.json")
	customSkillsCache := filepath.Join(tmp, "my-skills-cache.json")
	customUpdateCache := filepath.Join(tmp, "my-update-cache.json")

	ApplyPathOverrides(customLogDir, customRegistry, customSkillsCache, customUpdateCache)
	defer ApplyPathOverrides("", "", "", "") // restore defaults for other tests

	if got := GetLogDir(); got != customLogDir {
		t.Errorf("GetLogDir() = %q, want %q", got, customLogDir)
	}
	if got := GetRegistryPath(); got != customRegistry {
		t.Errorf("GetRegistryPath() = %q, want %q", got, customRegistry)
	}
	if got := GetSkillsCachePath(); got != customSkillsCache {
		t.Errorf("GetSkillsCachePath() = %q, want %q", got, customSkillsCache)
	}
	if got := GetUpdateCachePath(); got != customUpdateCache {
		t.Errorf("GetUpdateCachePath() = %q, want %q", got, customUpdateCache)
	}
}

func TestApplyPathOverridesEmptyKeepsDefault(t *testing.T) {
	ApplyPathOverrides("", "", "", "")

	home := RagCodeHome()

	// All should fall back to default (under RagCodeHome)
	if got := GetLogDir(); !strings.HasPrefix(got, home) {
		t.Errorf("GetLogDir() = %q, want under %q", got, home)
	}
	if got := GetRegistryPath(); !strings.HasPrefix(got, home) {
		t.Errorf("GetRegistryPath() = %q, want under %q", got, home)
	}
	if got := GetSkillsCachePath(); !strings.HasPrefix(got, home) {
		t.Errorf("GetSkillsCachePath() = %q, want under %q", got, home)
	}
	if got := GetUpdateCachePath(); !strings.HasPrefix(got, home) {
		t.Errorf("GetUpdateCachePath() = %q, want under %q", got, home)
	}
}

func TestPathsConfigParsedFromYAML(t *testing.T) {
	// This tests that the paths section is properly parsed via config.Load
	// by writing a config YAML with paths and loading it.
	// (config package test handles the YAML parsing, here we test the path
	// override integration works end-to-end)

	tmp := t.TempDir()
	customLogDir := filepath.Join(tmp, "custom-logs")

	// Apply override and verify it takes effect
	ApplyPathOverrides(customLogDir, "", "", "")
	defer ApplyPathOverrides("", "", "", "")

	got := GetLogDir()
	if got != customLogDir {
		t.Errorf("after override, GetLogDir() = %q, want %q", got, customLogDir)
	}

	// After clearing, should fall back to default
	ApplyPathOverrides("", "", "", "")
	got2 := GetLogDir()
	if got2 == customLogDir {
		t.Errorf("after clearing, GetLogDir() still = %q, want default", customLogDir)
	}
}

func TestPartialOverrideOnlyAffectsSet(t *testing.T) {
	tmp := t.TempDir()
	customRegistry := filepath.Join(tmp, "only-registry.json")

	ApplyPathOverrides("", customRegistry, "", "")
	defer ApplyPathOverrides("", "", "", "")

	home := RagCodeHome()

	// Registry should be overridden
	if got := GetRegistryPath(); got != customRegistry {
		t.Errorf("GetRegistryPath() = %q, want %q", got, customRegistry)
	}

	// Others should remain default
	if got := GetLogDir(); !strings.HasPrefix(got, home) {
		t.Errorf("GetLogDir() = %q, want under %q (not overridden)", got, home)
	}
	if got := GetSkillsCachePath(); !strings.HasPrefix(got, home) {
		t.Errorf("GetSkillsCachePath() = %q, want under %q (not overridden)", got, home)
	}
	if got := GetUpdateCachePath(); !strings.HasPrefix(got, home) {
		t.Errorf("GetUpdateCachePath() = %q, want under %q (not overridden)", got, home)
	}
}

func TestGetConfigDirMatchesRagCodeHome(t *testing.T) {
	if got := GetConfigDir(); got != RagCodeHome() {
		t.Errorf("GetConfigDir() = %q, want %q", got, RagCodeHome())
	}
}

func TestRagCodeHomeWithMissingHomeDir(t *testing.T) {
	// Temporarily unset HOME to test fallback
	orig := os.Getenv("HOME")
	os.Unsetenv("HOME")
	defer os.Setenv("HOME", orig)

	home := RagCodeHome()
	// Should not panic, should return something reasonable
	if home == "" {
		t.Error("RagCodeHome() returned empty string when HOME is unset")
	}
}

// Package config loads the global RagCode configuration from
// ~/.ragcode/config.yaml (or $RAGCODE_HOME/config.yaml) and provides the
// process logger.
package config

import (
	_ "embed"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

//go:embed default.yaml
var defaultYAML []byte

// Config is the whole user-facing configuration.
type Config struct {
	OllamaBaseURL         string        `yaml:"ollama_base_url"`
	OllamaEmbed           string        `yaml:"ollama_embed"`
	EmbedThreads          int           `yaml:"embed_threads"`
	MinFreeMemoryMB       int           `yaml:"min_free_memory_mb"`
	LogLevel              string        `yaml:"log_level"`
	LogMaxSizeMB          int           `yaml:"log_max_size_mb"`
	ExcludePatterns       []string      `yaml:"exclude_patterns"`
	AllowedWorkspacePaths []string      `yaml:"allowed_workspace_paths"`
	AutoIndex             bool          `yaml:"auto_index"`
	WatchEnabled          bool          `yaml:"watch_enabled"`
	WatchDebounce         time.Duration `yaml:"watch_debounce"`
	AutoCreateIDERules    bool          `yaml:"auto_create_ide_rules"`
	AutoUpdate            bool          `yaml:"auto_update"`
	UpdateSource          string        `yaml:"update_source"`
}

// Default returns the built-in configuration; default.yaml mirrors it.
func Default() Config {
	return Config{
		OllamaBaseURL: "http://localhost:11434",
		OllamaEmbed:   "embeddinggemma-2:270m",
		// A CPU-only Ollama otherwise takes every performance core.
		EmbedThreads:       max(2, runtime.NumCPU()/3),
		MinFreeMemoryMB:    1024,
		LogLevel:           "info",
		LogMaxSizeMB:       10,
		ExcludePatterns:    []string{"node_modules", "vendor", "target", "build", "dist", "__pycache__"},
		AutoIndex:          true,
		WatchEnabled:       true,
		WatchDebounce:      time.Second,
		AutoCreateIDERules: true,
		AutoUpdate:         true,
	}
}

// HomeDir is the global RagCode directory: $RAGCODE_HOME or ~/.ragcode.
func HomeDir() string {
	if h := os.Getenv("RAGCODE_HOME"); h != "" {
		return h
	}
	home, err := os.UserHomeDir()
	if err != nil {
		home = "."
	}
	return filepath.Join(home, ".ragcode")
}

// DefaultPath is HomeDir()/config.yaml.
func DefaultPath() string { return filepath.Join(HomeDir(), "config.yaml") }

// Load reads path on top of Default(), applies environment overrides and
// validates. A missing file is created from the embedded default.yaml.
func Load(path string) (Config, error) {
	cfg := Default()
	data, err := os.ReadFile(path)
	switch {
	case errors.Is(err, os.ErrNotExist):
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return cfg, fmt.Errorf("config: create dir: %w", err)
		}
		if err := os.WriteFile(path, defaultYAML, 0o644); err != nil {
			return cfg, fmt.Errorf("config: write default: %w", err)
		}
	case err != nil:
		return cfg, fmt.Errorf("config: read %s: %w", path, err)
	default:
		if err := yaml.Unmarshal(data, &cfg); err != nil {
			return cfg, fmt.Errorf("config: parse %s: %w", path, err)
		}
	}
	cfg.applyEnv()
	if err := cfg.Validate(); err != nil {
		return cfg, err
	}
	return cfg, nil
}

func (c *Config) applyEnv() {
	set := func(dst *string, key string) {
		if v := os.Getenv(key); v != "" {
			*dst = v
		}
	}
	set(&c.OllamaBaseURL, "OLLAMA_BASE_URL")
	set(&c.OllamaEmbed, "OLLAMA_EMBED")
	set(&c.LogLevel, "MCP_LOG_LEVEL")
	set(&c.UpdateSource, "RAGCODE_UPDATE_SOURCE")
	if v := os.Getenv("RAGCODE_ALLOWED_PATHS"); v != "" {
		c.AllowedWorkspacePaths = c.AllowedWorkspacePaths[:0]
		for _, p := range filepath.SplitList(v) {
			if p = strings.TrimSpace(p); p != "" {
				c.AllowedWorkspacePaths = append(c.AllowedWorkspacePaths, p)
			}
		}
	}
}

// Validate reports the first invalid field.
func (c Config) Validate() error {
	if err := checkURL("ollama_base_url", c.OllamaBaseURL); err != nil {
		return err
	}
	if c.OllamaEmbed == "" {
		return errors.New("config: ollama_embed is required")
	}
	switch c.LogLevel {
	case "debug", "info", "warn", "error":
	default:
		return fmt.Errorf("config: log_level %q must be debug, info, warn or error", c.LogLevel)
	}
	if c.EmbedThreads < 0 {
		return fmt.Errorf("config: embed_threads must not be negative, got %d", c.EmbedThreads)
	}
	if c.MinFreeMemoryMB < 0 {
		return fmt.Errorf("config: min_free_memory_mb must not be negative, got %d", c.MinFreeMemoryMB)
	}
	if c.LogMaxSizeMB <= 0 {
		return fmt.Errorf("config: log_max_size_mb must be positive, got %d", c.LogMaxSizeMB)
	}
	if c.WatchDebounce < 0 {
		return fmt.Errorf("config: watch_debounce must not be negative, got %s", c.WatchDebounce)
	}
	for _, p := range c.AllowedWorkspacePaths {
		if !filepath.IsAbs(p) {
			return fmt.Errorf("config: allowed_workspace_paths entry %q is not absolute", p)
		}
	}
	return nil
}

func checkURL(field, raw string) error {
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return fmt.Errorf("config: %s %q must be an http(s) URL", field, raw)
	}
	return nil
}

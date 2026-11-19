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
	EmbedModel            string        `yaml:"embed_model"`
	LlamaServer           string        `yaml:"llama_server"`
	EmbedRuntime          string        `yaml:"embed_runtime"`
	EmbedThreads          int           `yaml:"embed_threads"`
	EmbedMaxTokens        int           `yaml:"embed_max_tokens"`
	EmbedIdleStop         time.Duration `yaml:"embed_idle_stop"`
	EmbedWarmStart        bool          `yaml:"embed_warm_start"`
	MinFreeMemoryMB       int           `yaml:"min_free_memory_mb"`
	LogLevel              string        `yaml:"log_level"`
	LogMaxSizeMB          int           `yaml:"log_max_size_mb"`
	ExcludePatterns       []string      `yaml:"exclude_patterns"`
	MaxFiles              int           `yaml:"max_files"`
	AllowedWorkspacePaths []string      `yaml:"allowed_workspace_paths"`
	AutoIndex             bool          `yaml:"auto_index"`
	WatchEnabled          bool          `yaml:"watch_enabled"`
	WatchDebounce         time.Duration `yaml:"watch_debounce"`
	AutoCreateIDERules    bool          `yaml:"auto_create_ide_rules"`
	AutoUpdate            bool          `yaml:"auto_update"`
	UpdateSource          string        `yaml:"update_source"`
}

// UnmarshalYAML reads the file into c. yaml.v3 takes a duration from text
// only, so a bare 0 in embed_idle_stop (never stop the model) would be refused
// as a number: it is read as the text "0", which is a duration.
func (c *Config) UnmarshalYAML(n *yaml.Node) error {
	if n.Kind == yaml.MappingNode {
		for i := 0; i+1 < len(n.Content); i += 2 {
			if v := n.Content[i+1]; n.Content[i].Value == "embed_idle_stop" && v.Tag == "!!int" && v.Value == "0" {
				v.Tag = "!!str"
			}
		}
	}
	type plain Config // without the method, so that Decode does not call it again
	return n.Decode((*plain)(c))
}

// The bounds of embed_max_tokens. BuiltinModelTokens is the context window of
// the built-in model, EmbeddingGemma 2 (embed.DefaultTokens).
const (
	BuiltinModelTokens = 8192
	minEmbedTokens     = 256 // llama.cpp rounds a context up to a multiple of it
	maxEmbedTokens     = 32768
	minEmbedIdleStop   = time.Minute // a shorter one only reloads the model between two searches
)

// Default returns the built-in configuration; default.yaml mirrors it.
func Default() Config {
	return Config{
		EmbedRuntime: "auto",
		// llama.cpp otherwise takes every performance core.
		EmbedThreads:       max(2, runtime.NumCPU()/3),
		EmbedMaxTokens:     BuiltinModelTokens,
		EmbedIdleStop:      30 * time.Minute,
		MinFreeMemoryMB:    750,
		LogLevel:           "info",
		LogMaxSizeMB:       10,
		ExcludePatterns:    []string{"node_modules", "vendor", "target", "build", "dist", "__pycache__"},
		MaxFiles:           20000,
		AutoIndex:          true,
		WatchEnabled:       true,
		WatchDebounce:      time.Second,
		AutoCreateIDERules: true,
		AutoUpdate:         true,
	}
}

// HomeDir is the global RagCode directory: $RAGCODE_HOME or ~/.ragcode. The
// result is always absolute (a relative one is resolved against the working
// directory): llama-server runs with its own directory as working directory
// and would not find a relative binary or model path.
func HomeDir() string {
	h := os.Getenv("RAGCODE_HOME")
	if h == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			home = "."
		}
		h = filepath.Join(home, ".ragcode")
	}
	if a, err := filepath.Abs(h); err == nil {
		return a
	}
	return filepath.Clean(h)
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
	set(&c.EmbedModel, "RAGCODE_EMBED_MODEL")
	set(&c.LlamaServer, "RAGCODE_LLAMA_SERVER")
	set(&c.EmbedRuntime, "RAGCODE_EMBED_RUNTIME")
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
	if c.EmbedModel != "" && (!filepath.IsAbs(c.EmbedModel) || !strings.HasSuffix(c.EmbedModel, ".gguf")) {
		return fmt.Errorf("config: embed_model %q must be the absolute path of a .gguf file (leave it out for the built-in model)", c.EmbedModel)
	}
	switch c.EmbedRuntime {
	case "auto", "cpu", "vulkan":
	default:
		return fmt.Errorf("config: embed_runtime %q must be auto, cpu or vulkan", c.EmbedRuntime)
	}
	if c.LlamaServer != "" && !filepath.IsAbs(c.LlamaServer) {
		return fmt.Errorf("config: llama_server %q must be an absolute path (leave it out for the built-in runtime)", c.LlamaServer)
	}
	switch c.LogLevel {
	case "debug", "info", "warn", "error":
	default:
		return fmt.Errorf("config: log_level %q must be debug, info, warn or error", c.LogLevel)
	}
	if c.EmbedThreads < 0 {
		return fmt.Errorf("config: embed_threads must not be negative, got %d", c.EmbedThreads)
	}
	if c.EmbedMaxTokens < minEmbedTokens || c.EmbedMaxTokens > maxEmbedTokens {
		return fmt.Errorf("config: embed_max_tokens must be between %d and %d, got %d", minEmbedTokens, maxEmbedTokens, c.EmbedMaxTokens)
	}
	if c.EmbedModel == "" && c.EmbedMaxTokens > BuiltinModelTokens {
		return fmt.Errorf("config: embed_max_tokens %d is over the %d tokens the built-in model takes (a higher value needs your own embed_model)", c.EmbedMaxTokens, BuiltinModelTokens)
	}
	if c.EmbedIdleStop != 0 && c.EmbedIdleStop < minEmbedIdleStop {
		return fmt.Errorf("config: embed_idle_stop must be 0 (never stop) or at least %s, got %s", minEmbedIdleStop, c.EmbedIdleStop)
	}
	if c.MaxFiles < 0 {
		return fmt.Errorf("config: max_files must not be negative, got %d", c.MaxFiles)
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

// Redacted returns raw with the password of a user:password@ URL replaced by
// "xxxxx", for messages and logs; anything else is returned unchanged.
func Redacted(raw string) string {
	if u, err := url.Parse(raw); err == nil && u.User != nil {
		return u.Redacted()
	}
	// A mistyped URL (http:user:pw@host) has no user part to parse: what
	// stands before the last @ is hidden when it may hold user:password.
	if i := strings.LastIndex(raw, "@"); i >= 0 && strings.Contains(raw[:i], ":") {
		return "xxxxx@" + raw[i+1:]
	}
	return raw
}

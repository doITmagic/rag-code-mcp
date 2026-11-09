package config

import (
	"bytes"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

func TestDefaultMatchesEmbeddedYAML(t *testing.T) {
	fromFile := Config{EmbedThreads: Default().EmbedThreads} // machine-dependent: commented out in default.yaml
	require.NoError(t, yaml.Unmarshal(defaultYAML, &fromFile))
	assert.Equal(t, Default(), fromFile)
	assert.NoError(t, Default().Validate())
}

func TestLoadFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	require.NoError(t, os.WriteFile(path, []byte(`
ollama_embed: other-model
log_level: debug
watch_debounce: 250ms
exclude_patterns: [foo]
`), 0o644))
	cfg, err := Load(path)
	require.NoError(t, err)
	assert.Equal(t, "other-model", cfg.OllamaEmbed)
	assert.Equal(t, "http://localhost:11434", cfg.OllamaBaseURL, "untouched keys keep defaults")
	assert.Equal(t, "debug", cfg.LogLevel)
	assert.Equal(t, 250*time.Millisecond, cfg.WatchDebounce)
	assert.Equal(t, []string{"foo"}, cfg.ExcludePatterns)
}

func TestLoadMissingFileWritesDefault(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "dir", "config.yaml")
	cfg, err := Load(path)
	require.NoError(t, err)
	assert.Equal(t, Default(), cfg)
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, defaultYAML, data)
}

func TestLoadInvalidYAML(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	require.NoError(t, os.WriteFile(path, []byte("log_level: [\n"), 0o644))
	_, err := Load(path)
	assert.ErrorContains(t, err, "parse")
}

func TestEnvOverrides(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	require.NoError(t, os.WriteFile(path, []byte("ollama_embed: file-embed\nallowed_workspace_paths: [/from/file]\n"), 0o644))
	t.Setenv("OLLAMA_BASE_URL", "http://env:11434")
	t.Setenv("OLLAMA_EMBED", "env-embed")
	t.Setenv("MCP_LOG_LEVEL", "warn")
	t.Setenv("RAGCODE_ALLOWED_PATHS", strings.Join([]string{"/a", " /b ", ""}, string(os.PathListSeparator)))
	cfg, err := Load(path)
	require.NoError(t, err)
	assert.Equal(t, "http://env:11434", cfg.OllamaBaseURL)
	assert.Equal(t, "env-embed", cfg.OllamaEmbed, "env wins over file")
	assert.Equal(t, "warn", cfg.LogLevel)
	assert.Equal(t, []string{"/a", "/b"}, cfg.AllowedWorkspacePaths)
}

func TestValidate(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(*Config)
		wantErr string
	}{
		{"bad ollama url", func(c *Config) { c.OllamaBaseURL = "localhost:11434" }, "ollama_base_url"},
		{"empty embed", func(c *Config) { c.OllamaEmbed = "" }, "ollama_embed"},
		{"bad level", func(c *Config) { c.LogLevel = "verbose" }, "log_level"},
		{"zero log size", func(c *Config) { c.LogMaxSizeMB = 0 }, "log_max_size_mb"},
		{"negative debounce", func(c *Config) { c.WatchDebounce = -1 }, "watch_debounce"},
		{"relative allowed path", func(c *Config) { c.AllowedWorkspacePaths = []string{"rel"} }, "allowed_workspace_paths"},
		{"zero debounce ok", func(c *Config) { c.WatchDebounce = 0 }, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := Default()
			tt.mutate(&cfg)
			err := cfg.Validate()
			if tt.wantErr == "" {
				assert.NoError(t, err)
				return
			}
			assert.ErrorContains(t, err, tt.wantErr)
		})
	}
}

func TestLoadValidationError(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	require.NoError(t, os.WriteFile(path, []byte("log_level: loud\n"), 0o644))
	_, err := Load(path)
	assert.ErrorContains(t, err, "log_level")
}

func TestHomeDirAndDefaultPath(t *testing.T) {
	t.Setenv("RAGCODE_HOME", "/custom")
	assert.Equal(t, "/custom", HomeDir())
	assert.Equal(t, filepath.Join("/custom", "config.yaml"), DefaultPath())
	t.Setenv("RAGCODE_HOME", "")
	home, err := os.UserHomeDir()
	require.NoError(t, err)
	assert.Equal(t, filepath.Join(home, ".ragcode"), HomeDir())
}

func TestNewLogger(t *testing.T) {
	var buf bytes.Buffer
	log := NewLogger("warn", &buf)
	log.Info("hidden")
	log.Warn("shown", "k", "v")
	assert.NotContains(t, buf.String(), "hidden")
	assert.Contains(t, buf.String(), "level=WARN msg=shown k=v")

	buf.Reset()
	NewLogger("bogus", &buf).Info("info-at-fallback")
	assert.Contains(t, buf.String(), "info-at-fallback")
}

func TestOpenLogFileRotatesOnOpen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ragcode.log")
	old := bytes.Repeat([]byte("x"), 1<<20+1)
	require.NoError(t, os.WriteFile(path, old, 0o644))

	w, err := OpenLogFile(path, 1)
	require.NoError(t, err)
	_, err = w.Write([]byte("fresh\n"))
	require.NoError(t, err)
	require.NoError(t, w.Close())

	data, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, "fresh\n", string(data))
	rotated, err := os.ReadFile(path + ".1")
	require.NoError(t, err)
	assert.Equal(t, old, rotated)
}

func TestOpenLogFileRotatesEvery1000Writes(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ragcode.log")
	w, err := OpenLogFile(path, 1)
	require.NoError(t, err)
	defer w.Close()

	line := bytes.Repeat([]byte("y"), 1100) // 1000 lines > 1 MiB
	for i := 0; i < 999; i++ {
		_, err := w.Write(line)
		require.NoError(t, err)
	}
	st, err := os.Stat(path)
	require.NoError(t, err)
	assert.Equal(t, int64(999*1100), st.Size(), "no rotation before the 1000th write")

	_, err = w.Write(line) // 1000th write: over the cap, rotate first
	require.NoError(t, err)
	st, err = os.Stat(path)
	require.NoError(t, err)
	assert.Equal(t, int64(1100), st.Size())
	st, err = os.Stat(path + ".1")
	require.NoError(t, err)
	assert.Equal(t, int64(999*1100), st.Size())
}

func TestOpenLogFileKeepsWritingWhenRotationFails(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ragcode.log")
	w, err := OpenLogFile(path, 0)
	require.NoError(t, err)
	defer w.Close()
	// A non-empty directory at path+".1" makes the rename fail.
	require.NoError(t, os.MkdirAll(filepath.Join(path+".1", "x"), 0o755))

	for i := 0; i < 1001; i++ {
		_, err := w.Write([]byte("z"))
		require.NoError(t, err, "write %d", i+1)
	}
	st, err := os.Stat(path)
	require.NoError(t, err)
	assert.Equal(t, int64(1001), st.Size(), "no line lost when rotation fails")
}

// A v1 config.yaml still carries the Qdrant keys; they are
// ignored, not an error.
func TestLoadIgnoresOldQdrantKeys(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	require.NoError(t, os.WriteFile(path, []byte("qdrant_url: http://x:6333\nqdrant_api_key: k\n"), 0o644))
	_, err := Load(path)
	require.NoError(t, err)
}

// Two servers share the log. When one rotates it twice, the other's next
// line must not land in the overwritten (unlinked) file.
func TestOpenLogFileFollowsRotationByAnotherProcess(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ragcode.log")
	busy, err := OpenLogFile(path, 1)
	require.NoError(t, err)
	defer busy.Close()
	quiet, err := OpenLogFile(path, 1)
	require.NoError(t, err)
	defer quiet.Close()

	line := bytes.Repeat([]byte("y"), 1100)
	for i := 0; i < 2000; i++ { // rotates at writes 1000 and 2000
		_, err := busy.Write(line)
		require.NoError(t, err)
	}
	_, err = quiet.Write([]byte("quiet-line\n"))
	require.NoError(t, err)

	cur, _ := os.ReadFile(path)
	rot, _ := os.ReadFile(path + ".1")
	assert.True(t, bytes.Contains(cur, []byte("quiet-line")) || bytes.Contains(rot, []byte("quiet-line")), "line lost in a rotated-away file")
}

// embed_threads defaults to an eighth of the logical CPUs (at least 2), so a
// CPU-only Ollama does not take every core; 0 hands the choice to Ollama.
func TestEmbedThreads(t *testing.T) {
	assert.Equal(t, max(2, runtime.NumCPU()/8), Default().EmbedThreads)
	path := filepath.Join(t.TempDir(), "config.yaml")
	require.NoError(t, os.WriteFile(path, []byte("embed_threads: 0\n"), 0o644))
	cfg, err := Load(path)
	require.NoError(t, err)
	assert.Zero(t, cfg.EmbedThreads)
	require.NoError(t, os.WriteFile(path, []byte("embed_threads: -1\n"), 0o644))
	_, err = Load(path)
	assert.ErrorContains(t, err, "embed_threads")
}

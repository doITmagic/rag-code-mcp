package config

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Independent verification probes: odd inputs must error or load, never panic or hang.
func TestVerifyLoadOddInputs(t *testing.T) {
	tests := []struct {
		name    string
		data    []byte
		wantErr string
	}{
		{"empty file", nil, ""},
		{"whitespace only", []byte("\n\n  \n"), ""},
		{"one byte scalar", []byte("x"), "parse"},
		{"binary garbage", []byte{0xff, 0xfe, 0x00, 0x01, 0x80, 0x7f, 0x0a, 0x00}, "parse"},
		{"truncated mapping", []byte("ollama_embed: \"abc"), "parse"},
		{"wrong type", []byte("log_max_size_mb: ten\n"), "parse"},
		{"unknown keys ignored", []byte("llamafile_url: http://x\nollama_embed: m\n"), ""},
		{"empty exclude list wins", []byte("exclude_patterns: []\n"), ""},
		{"bad url from file", []byte("ollama_base_url: ':oops'\n"), "ollama_base_url"},
		{"negative size", []byte("log_max_size_mb: -3\n"), "log_max_size_mb"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "config.yaml")
			require.NoError(t, os.WriteFile(path, tt.data, 0o644))
			done := make(chan struct{})
			var cfg Config
			var err error
			go func() { defer close(done); cfg, err = Load(path) }()
			select {
			case <-done:
			case <-time.After(5 * time.Second):
				t.Fatal("Load hung")
			}
			if tt.wantErr == "" {
				require.NoError(t, err)
				assert.NoError(t, cfg.Validate())
				if tt.name == "empty exclude list wins" {
					assert.Empty(t, cfg.ExcludePatterns)
				}
				return
			}
			assert.ErrorContains(t, err, tt.wantErr)
		})
	}
}

func TestVerifyLoadPathIsDirectory(t *testing.T) {
	_, err := Load(t.TempDir())
	require.Error(t, err)
	assert.NotContains(t, err.Error(), "write default", "a directory must not be treated as a missing file")
}

func TestVerifyLoadMissingFileUnwritableParent(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root ignores permissions")
	}
	dir := t.TempDir()
	require.NoError(t, os.Chmod(dir, 0o500))
	t.Cleanup(func() { os.Chmod(dir, 0o700) })
	_, err := Load(filepath.Join(dir, "sub", "config.yaml"))
	assert.ErrorContains(t, err, "create dir")
}

func TestVerifyEnvAppliesOnMissingFileAndIsValidated(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	t.Setenv("OLLAMA_EMBED", "env-model")
	cfg, err := Load(path)
	require.NoError(t, err)
	assert.Equal(t, "env-model", cfg.OllamaEmbed)
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Contains(t, string(data), "embeddinggemma-2:270m", "written default must not carry env values")

	t.Setenv("MCP_LOG_LEVEL", "loud")
	_, err = Load(path)
	assert.ErrorContains(t, err, "log_level")

	t.Setenv("MCP_LOG_LEVEL", "")
	t.Setenv("RAGCODE_ALLOWED_PATHS", "relative/dir")
	_, err = Load(path)
	assert.ErrorContains(t, err, "allowed_workspace_paths")

	t.Setenv("RAGCODE_ALLOWED_PATHS", string(os.PathListSeparator))
	cfg, err = Load(path)
	require.NoError(t, err)
	assert.Empty(t, cfg.AllowedWorkspacePaths, "separator-only env clears the list")
}

func TestVerifyEnvEmptyValueDoesNotOverride(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	require.NoError(t, os.WriteFile(path, []byte("ollama_embed: fromfile\n"), 0o644))
	t.Setenv("OLLAMA_EMBED", "")
	cfg, err := Load(path)
	require.NoError(t, err)
	assert.Equal(t, "fromfile", cfg.OllamaEmbed)
}

func TestVerifyYAMLTagsSnakeCase(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	require.NoError(t, os.WriteFile(path, []byte(strings.Join([]string{
		"ollama_base_url: http://o:1",
		"ollama_embed: m",
		"embed_threads: 3",
		"min_free_memory_mb: 7",
		"log_level: error",
		"log_max_size_mb: 3",
		"exclude_patterns: [a, b]",
		"max_files: 5",
		"allowed_workspace_paths: [/x]",
		"auto_index: false",
		"watch_enabled: false",
		"watch_debounce: 2m",
		"auto_create_ide_rules: false",
		"auto_update: false",
		"update_source: /dist",
		"",
	}, "\n")), 0o644))
	cfg, err := Load(path)
	require.NoError(t, err)
	assert.Equal(t, Config{
		OllamaBaseURL: "http://o:1", OllamaEmbed: "m", EmbedThreads: 3, MinFreeMemoryMB: 7,
		LogLevel: "error", LogMaxSizeMB: 3, ExcludePatterns: []string{"a", "b"}, MaxFiles: 5,
		AllowedWorkspacePaths: []string{"/x"}, AutoIndex: false, WatchEnabled: false,
		WatchDebounce: 2 * time.Minute, AutoCreateIDERules: false,
		AutoUpdate: false, UpdateSource: "/dist",
	}, cfg)
}

func TestVerifyDefaultsExact(t *testing.T) {
	d := Default()
	assert.Equal(t, "http://localhost:11434", d.OllamaBaseURL)
	assert.Equal(t, "embeddinggemma-2:270m", d.OllamaEmbed)
	assert.Equal(t, "info", d.LogLevel)
	assert.Equal(t, 10, d.LogMaxSizeMB)
	assert.Equal(t, []string{"node_modules", "vendor", "target", "build", "dist", "__pycache__"}, d.ExcludePatterns)
	assert.Nil(t, d.AllowedWorkspacePaths)
	assert.True(t, d.AutoIndex)
	assert.True(t, d.WatchEnabled)
	assert.Equal(t, time.Second, d.WatchDebounce)
	assert.True(t, d.AutoCreateIDERules)
}

func TestVerifyLoggerLevels(t *testing.T) {
	for _, lvl := range []string{"debug", "info", "warn", "error"} {
		var buf bytes.Buffer
		l := NewLogger(lvl, &buf)
		l.Debug("d")
		l.Info("i")
		l.Warn("w")
		l.Error("e")
		out := buf.String()
		assert.Contains(t, out, "level=ERROR", lvl)
		assert.Equal(t, lvl != "error", strings.Contains(out, "level=WARN"), lvl)
		assert.Equal(t, lvl == "debug" || lvl == "info", strings.Contains(out, "level=INFO"), lvl)
		assert.Equal(t, lvl == "debug", strings.Contains(out, "level=DEBUG"), lvl)
	}
}

func TestVerifyOpenLogFileErrors(t *testing.T) {
	_, err := OpenLogFile(filepath.Join(t.TempDir(), "missing", "ragcode.log"), 1)
	assert.ErrorContains(t, err, "open log")

	// Existing path+".1" is replaced on rotation, not an error.
	dir := t.TempDir()
	path := filepath.Join(dir, "ragcode.log")
	require.NoError(t, os.WriteFile(path, bytes.Repeat([]byte("a"), 1<<20+1), 0o644))
	require.NoError(t, os.WriteFile(path+".1", []byte("old-rotated"), 0o644))
	w, err := OpenLogFile(path, 1)
	require.NoError(t, err)
	require.NoError(t, w.Close())
	rot, err := os.ReadFile(path + ".1")
	require.NoError(t, err)
	assert.Len(t, rot, 1<<20+1)

	// Exactly at the cap: no rotation.
	path2 := filepath.Join(dir, "exact.log")
	require.NoError(t, os.WriteFile(path2, bytes.Repeat([]byte("a"), 1<<20), 0o644))
	w, err = OpenLogFile(path2, 1)
	require.NoError(t, err)
	require.NoError(t, w.Close())
	_, err = os.Stat(path2 + ".1")
	assert.True(t, os.IsNotExist(err))

	// Write after Close errors, no panic.
	_, err = w.Write([]byte("x"))
	assert.Error(t, err)
}

func TestVerifyLogFileConcurrentWritesAcrossRotation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ragcode.log")
	w, err := OpenLogFile(path, 1)
	require.NoError(t, err)
	line := bytes.Repeat([]byte("z"), 600) // 1000 lines < 1 MiB, 2000 lines > 1 MiB: one rotation at write 2000
	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 250; i++ {
				_, err := w.Write(line)
				assert.NoError(t, err)
			}
		}()
	}
	wg.Wait()
	require.NoError(t, w.Close())
	cur, err := os.Stat(path)
	require.NoError(t, err)
	rot, err := os.Stat(path + ".1")
	require.NoError(t, err)
	assert.Equal(t, int64(2000*600), cur.Size()+rot.Size(), "no line lost across rotation")
	assert.Equal(t, int64(600), cur.Size(), "rotation happens before the 2000th write")
	assert.Greater(t, rot.Size(), int64(1<<20))
}

func TestVerifyLoggerThroughLogFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ragcode.log")
	w, err := OpenLogFile(path, 1)
	require.NoError(t, err)
	NewLogger("info", w).Info("hello", "n", 1)
	require.NoError(t, w.Close())
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Contains(t, string(data), "msg=hello n=1")
}

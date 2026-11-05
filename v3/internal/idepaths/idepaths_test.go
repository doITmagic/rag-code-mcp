package idepaths

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func tempHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("APPDATA", filepath.Join(home, "AppData", "Roaming"))
	t.Setenv("CODEX_HOME", "")
	return home
}

func byKey(t *testing.T, key string) Target {
	t.Helper()
	for _, tg := range Targets() {
		if tg.Key == key {
			return tg
		}
	}
	t.Fatalf("no target %q", key)
	return Target{}
}

func readFile(t *testing.T, path string) map[string]any {
	t.Helper()
	m, err := readJSON(path)
	require.NoError(t, err)
	return m
}

func TestTargetsPaths(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("linux paths")
	}
	home := tempHome(t)
	cases := map[string]string{
		"cursor":         ".cursor/mcp.json",
		"windsurf":       ".codeium/windsurf/mcp_config.json",
		"claude-code":    ".claude.json",
		"claude-desktop": ".config/Claude/claude_desktop_config.json",
		"vscode":         ".config/Code/User/mcp.json",
		"zed":            ".config/zed/settings.json",
		"cline":          ".config/Code/User/globalStorage/saoudrizwan.claude-dev/settings/cline_mcp_settings.json",
		"roo":            ".config/Code/User/globalStorage/rooveterinaryinc.roo-cline/settings/cline_mcp_settings.json",
		"continue":       ".continue/mcpServers/ragcode.json",
		"gemini":         ".gemini/settings.json",
		"codex":          ".codex/config.toml",
		"antigravity":    ".gemini/config/mcp_config.json",
		"copilot":        ".copilot/mcp-config.json",
		"openhands":      ".openhands/mcp.json",
	}
	assert.Len(t, Targets(), len(cases))
	for key, rel := range cases {
		assert.Equal(t, filepath.Join(home, rel), byKey(t, key).Path, key)
	}
	assert.Equal(t, filepath.Join(home, ".claude"), byKey(t, "claude-code").Dir)
	t.Setenv("CODEX_HOME", "/opt/codex")
	assert.Equal(t, "/opt/codex/config.toml", byKey(t, "codex").Path)
}

func TestWriteMergeAndRemove(t *testing.T) {
	tempHome(t)
	cases := []struct {
		name, key, existing string
		keys                []string
	}{
		{"new file", "cursor", "", []string{"mcpServers"}},
		{"empty file", "windsurf", "", []string{"mcpServers"}},
		{"bom and unknown keys", "claude-code", "\xef\xbb\xbf{\"theme\":\"dark\",\"mcpServers\":{\"other\":{\"command\":\"x\"},\"ragcode\":{\"url\":\"http://old\",\"env\":{\"A\":\"1\"}}},\"n\":12345678901234567890}", []string{"mcpServers"}},
		{"vscode servers key", "vscode", `{"servers":{"foo":{"command":"bar"}},"inputs":[]}`, []string{"servers"}},
		{"zed context_servers", "zed", `{"theme":"One Dark","context_servers":{"other":{}}}`, []string{"context_servers"}},
		{"cline", "cline", `{"mcpServers":{}}`, []string{"mcpServers"}},
		{"roo", "roo", "", []string{"mcpServers"}},
		{"continue", "continue", "", []string{"mcpServers"}},
		{"gemini", "gemini", `{"selectedAuthType":"oauth"}`, []string{"mcpServers"}},
		{"antigravity", "antigravity", "", []string{"mcpServers"}},
		{"copilot", "copilot", "", []string{"mcpServers"}},
		{"openhands", "openhands", "", []string{"mcpServers"}},
		{"claude desktop", "claude-desktop", "", []string{"mcpServers"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tg := byKey(t, tc.key)
			require.NoError(t, os.MkdirAll(tg.Dir, 0o755))
			if tc.existing != "" {
				require.NoError(t, os.MkdirAll(filepath.Dir(tg.Path), 0o755))
				require.NoError(t, os.WriteFile(tg.Path, []byte(tc.existing), 0o644))
			}
			require.NoError(t, tg.Write("/bin/ragcode", "/cfg.yaml"))

			got := readFile(t, tg.Path)
			node := got
			for _, k := range tc.keys {
				node = node[k].(map[string]any)
			}
			entry := node["ragcode"].(map[string]any)
			assert.Equal(t, "/bin/ragcode", entry["command"])
			assert.Equal(t, []any{"--config", "/cfg.yaml"}, entry["args"])
			_, hasURL := entry["url"]
			assert.False(t, hasURL)
			switch tc.key {
			case "claude-code":
				assert.Equal(t, "dark", got["theme"])
				assert.Equal(t, json.Number("12345678901234567890"), got["n"])
				assert.Equal(t, map[string]any{"A": "1"}, entry["env"], "unknown entry keys survive")
				assert.Contains(t, node, "other")
			case "vscode":
				assert.Contains(t, got, "inputs")
				assert.Contains(t, node, "foo")
			case "zed":
				assert.Equal(t, "custom", entry["source"])
				assert.Equal(t, "One Dark", got["theme"])
			}

			require.NoError(t, tg.Write("/bin/ragcode", "/cfg.yaml"), "idempotent")
			require.NoError(t, tg.Remove())
			got = readFile(t, tg.Path)
			node = got
			for _, k := range tc.keys {
				node = node[k].(map[string]any)
			}
			assert.NotContains(t, node, "ragcode")
			if tc.key == "claude-code" {
				assert.Equal(t, "dark", got["theme"])
			}
			require.NoError(t, tg.Remove(), "remove twice")
		})
	}
}

func TestWriteSkipsAndRefuses(t *testing.T) {
	tempHome(t)
	tg := byKey(t, "cursor")
	assert.ErrorIs(t, tg.Write("/b", "/c"), ErrNotInstalled)
	_, err := os.Stat(tg.Path)
	assert.True(t, os.IsNotExist(err))

	require.NoError(t, os.MkdirAll(tg.Dir, 0o755))
	require.NoError(t, os.WriteFile(tg.Path, []byte("{broken"), 0o644))
	assert.Error(t, tg.Write("/b", "/c"))
	data, _ := os.ReadFile(tg.Path)
	assert.Equal(t, "{broken", string(data), "invalid file left untouched")
	assert.Error(t, tg.Remove())

	require.NoError(t, os.WriteFile(tg.Path, []byte(`{"mcpServers":[]}`), 0o644))
	assert.Error(t, tg.Write("/b", "/c"), "servers key not an object")

	assert.NoError(t, byKey(t, "zed").Remove(), "missing file")
}

func TestCodex(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell script")
	}
	home := tempHome(t)
	bin := t.TempDir()
	log := filepath.Join(bin, "log")
	require.NoError(t, os.WriteFile(filepath.Join(bin, "codex"), []byte("#!/bin/sh\necho \"$CODEX_HOME $*\" >> "+log+"\n"), 0o755))
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))

	tg := byKey(t, "codex")
	assert.ErrorIs(t, tg.Write("/b", "/c"), ErrNotInstalled)
	require.NoError(t, os.MkdirAll(tg.Dir, 0o755))
	require.NoError(t, tg.Write("/b", "/c"))
	require.NoError(t, tg.Remove(), "no config.toml yet: nothing to do")
	require.NoError(t, os.WriteFile(tg.Path, nil, 0o644))
	require.NoError(t, tg.Remove())

	out, err := os.ReadFile(log)
	require.NoError(t, err)
	codexHome := filepath.Join(home, ".codex")
	assert.Equal(t, codexHome+" mcp add ragcode -- /b --config /c\n"+codexHome+" mcp remove ragcode\n", string(out))

	t.Setenv("PATH", t.TempDir())
	assert.Error(t, tg.Write("/b", "/c"), "cli missing")
}

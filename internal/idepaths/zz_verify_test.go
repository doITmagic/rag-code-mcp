package idepaths

// Independent verification probes: robustness of Write/Remove on odd files
// and side effects of Remove on files that never held a ragcode entry.

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestVerifyWriteOddInputs(t *testing.T) {
	tempHome(t)
	tg := byKey(t, "gemini")
	require.NoError(t, os.MkdirAll(tg.Dir, 0o755))
	cases := []struct {
		name, body string
		wantErr    bool
	}{
		{"one byte", "{", true},
		{"binary", "\x00\x01\x02\xff", true},
		{"truncated", `{"mcpServers":{"a":{"command":"x"`, true},
		{"top-level array", `[1,2]`, true},
		{"top-level null", `null`, true},
		{"whitespace only", "  \n\t", false},
		{"bom only", "\xef\xbb\xbf", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			require.NoError(t, os.WriteFile(tg.Path, []byte(tc.body), 0o644))
			var err error
			assert.NotPanics(t, func() { err = tg.Write("/b", "/c") })
			if tc.wantErr {
				assert.Error(t, err)
				data, _ := os.ReadFile(tg.Path)
				assert.Equal(t, tc.body, string(data), "bad file left untouched")
			} else {
				assert.NoError(t, err)
			}
			assert.NotPanics(t, func() { _ = tg.Remove() })
		})
	}
}

// Remove on a file that never had the servers key must not modify it.
func TestVerifyRemoveNoSideEffect(t *testing.T) {
	tempHome(t)
	tg := byKey(t, "gemini")
	require.NoError(t, os.MkdirAll(tg.Dir, 0o755))
	body := "{\n  \"selectedAuthType\": \"oauth\"\n}\n"
	require.NoError(t, os.WriteFile(tg.Path, []byte(body), 0o644))
	require.NoError(t, tg.Remove())
	data, _ := os.ReadFile(tg.Path)
	assert.Equal(t, body, string(data), "Remove rewrote a file with no ragcode entry")
}

// Zed keeps settings under ~/.config/zed on macOS too (v2 and Zed docs).
func TestVerifyZedDarwinPath(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("darwin only")
	}
	home := tempHome(t)
	assert.Equal(t, filepath.Join(home, ".config", "zed", "settings.json"), byKey(t, "zed").Path)
}

// Table sanity: 14 targets, unique keys, every field set; Dir as a plain file
// is "not installed"; an existing ragcode entry keeps IDE-side flags such as
// "disabled" and loses our transport fields.
func TestVerifyTableAndEntryMerge(t *testing.T) {
	home := tempHome(t)
	seen := map[string]bool{}
	for _, tg := range Targets() {
		assert.False(t, seen[tg.Key], tg.Key)
		seen[tg.Key] = true
		assert.NotEmpty(t, tg.Name, tg.Key)
		assert.True(t, filepath.IsAbs(tg.Path), tg.Key)
		assert.True(t, filepath.IsAbs(tg.Dir), tg.Key)
		if tg.Key != "codex" {
			assert.NotEmpty(t, tg.Keys, tg.Key)
		}
	}
	assert.Len(t, seen, 14)

	tg := byKey(t, "openhands")
	require.NoError(t, os.WriteFile(tg.Dir, nil, 0o644)) // a file where the dir should be
	assert.ErrorIs(t, tg.Write("/b", "/c"), ErrNotInstalled)
	require.NoError(t, os.Remove(tg.Dir))

	require.NoError(t, os.MkdirAll(tg.Dir, 0o755))
	require.NoError(t, os.WriteFile(tg.Path, []byte(`{"mcpServers":{"ragcode":{"type":"stdio","command":"old","args":["x"],"disabled":true}}}`), 0o644))
	require.NoError(t, tg.Write("/b", "/c"))
	m, err := readJSON(tg.Path)
	require.NoError(t, err)
	entry := m["mcpServers"].(map[string]any)["ragcode"].(map[string]any)
	assert.Equal(t, map[string]any{"command": "/b", "args": []any{"--config", "/c"}, "disabled": true}, entry)

	// "mcpServers": null counts as missing and is created.
	require.NoError(t, os.WriteFile(tg.Path, []byte(`{"mcpServers":null}`), 0o644))
	require.NoError(t, tg.Write("/b", "/c"))
	m, err = readJSON(tg.Path)
	require.NoError(t, err)
	assert.Contains(t, m["mcpServers"].(map[string]any), "ragcode")
	_ = home
}

// The config is replaced atomically: a symlinked config (dotfiles) stays a
// symlink to the updated file, and the file's mode is kept.
func TestWriteKeepsSymlinkAndMode(t *testing.T) {
	dir := t.TempDir()
	real := filepath.Join(dir, "dotfiles", "mcp.json")
	require.NoError(t, os.MkdirAll(filepath.Dir(real), 0o755))
	require.NoError(t, os.WriteFile(real, []byte(`{"mcpServers":{"other":{"command":"x"}}}`), 0o600))
	link := filepath.Join(dir, "ide", "mcp.json")
	require.NoError(t, os.MkdirAll(filepath.Dir(link), 0o755))
	require.NoError(t, os.Symlink(real, link))
	tg := Target{Path: link, Dir: filepath.Dir(link), Keys: []string{"mcpServers"}}
	require.NoError(t, tg.Write("/bin/rag", "/cfg.yaml"))

	info, err := os.Lstat(link)
	require.NoError(t, err)
	assert.True(t, info.Mode()&os.ModeSymlink != 0, "still a symlink")
	st, err := os.Stat(real)
	require.NoError(t, err)
	if runtime.GOOS != "windows" { // Windows has no Unix permission bits
		assert.Equal(t, os.FileMode(0o600), st.Mode().Perm())
	}
	data, _ := os.ReadFile(real)
	assert.Contains(t, string(data), `"other"`)
	assert.Contains(t, string(data), `"ragcode"`)
	left, _ := filepath.Glob(filepath.Join(filepath.Dir(real), ".*"))
	assert.Empty(t, left, "no temp file left behind")
}

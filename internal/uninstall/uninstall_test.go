package uninstall

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/doITmagic/rag-code-mcp/v2/engine"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type fixture struct {
	home, root, dockerLog string
}

func setup(t *testing.T) *fixture {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("shell scripts")
	}
	f := &fixture{home: t.TempDir(), root: t.TempDir()}
	t.Setenv("HOME", f.home)
	t.Setenv("RAGCODE_HOME", "")
	bin := t.TempDir()
	f.dockerLog = filepath.Join(bin, "docker.log")
	require.NoError(t, os.WriteFile(filepath.Join(bin, "docker"), []byte("#!/bin/sh\necho \"$*\" >> "+f.dockerLog+"\n"), 0o755))
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))

	rag := filepath.Join(f.home, ".ragcode")
	require.NoError(t, os.MkdirAll(filepath.Join(rag, "bin"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(rag, "config.yaml"), []byte("log_level: info\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(rag, "roots.json"), []byte(`["`+f.root+`","`+filepath.Join(f.root, "missing")+`"]`), 0o644))
	require.NoError(t, os.MkdirAll(filepath.Join(f.root, ".ragcode"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(f.root, "keep.go"), nil, 0o644))
	require.NoError(t, os.MkdirAll(filepath.Join(f.root, ".cursor", "rules"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(f.root, "CLAUDE.md"), []byte("# Project"), 0o644))
	require.NoError(t, engine.WriteIDERules(f.root))

	binDir := filepath.Join(rag, "bin")
	require.NoError(t, os.WriteFile(filepath.Join(f.home, ".bashrc"), []byte("alias ll='ls -l'\n\n# RagCode MCP\nexport PATH=\""+binDir+":$PATH\"\n# RagCode MCP\nexport FOO=1\n"), 0o644))
	require.NoError(t, os.MkdirAll(filepath.Join(f.home, ".cursor"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(f.home, ".cursor", "mcp.json"), []byte(`{"mcpServers":{"ragcode":{"command":"x"},"other":{"command":"y"}}}`), 0o644))
	return f
}

func TestRun(t *testing.T) {
	f := setup(t)
	// Earlier Windows installs wrote Cursor's entry here, a file Cursor never reads.
	appData := t.TempDir()
	t.Setenv("APPDATA", appData)
	oldCursor := filepath.Join(appData, "Cursor", "mcp.json")
	require.NoError(t, os.MkdirAll(filepath.Dir(oldCursor), 0o755))
	require.NoError(t, os.WriteFile(oldCursor, []byte(`{"mcpServers":{"ragcode":{"command":"x"},"other":{"command":"y"}}}`), 0o644))
	var out bytes.Buffer
	require.NoError(t, Run(context.Background(), Options{Yes: true, Docker: true, Out: &out}), out.String())

	old, _ := os.ReadFile(oldCursor)
	assert.NotContains(t, string(old), "ragcode", "entry in the old %APPDATA% Cursor file")
	assert.Contains(t, string(old), "other")
	assert.NoDirExists(t, filepath.Join(f.root, ".ragcode"))
	assert.FileExists(t, filepath.Join(f.root, "keep.go"))
	assert.NoDirExists(t, filepath.Join(f.home, ".ragcode"))
	claude, _ := os.ReadFile(filepath.Join(f.root, "CLAUDE.md"))
	assert.Equal(t, "# Project\n", string(claude))
	assert.NoFileExists(t, filepath.Join(f.root, ".cursor", "rules", "ragcode.mdc"))

	rc, _ := os.ReadFile(filepath.Join(f.home, ".bashrc"))
	assert.Equal(t, "alias ll='ls -l'\n\n# RagCode MCP\nexport FOO=1\n", string(rc), "only the installer's pair goes")

	cursor, _ := os.ReadFile(filepath.Join(f.home, ".cursor", "mcp.json"))
	assert.NotContains(t, string(cursor), "ragcode")
	assert.Contains(t, string(cursor), "other")

	log, _ := os.ReadFile(f.dockerLog)
	assert.Equal(t, "rm -f ragcode-qdrant\nvolume rm ragcode-qdrant\nrm -f ragcode-ollama\nvolume rm ragcode-ollama\n", string(log))
}

func TestRunAsksForWorkspaceData(t *testing.T) {
	cases := []struct {
		answer string
		kept   bool
	}{
		{"n\n", true},
		{"\n", true},
		{"y\n", false},
	}
	for _, tc := range cases {
		t.Run(strings.TrimSpace(tc.answer), func(t *testing.T) {
			f := setup(t)
			var out bytes.Buffer
			require.NoError(t, Run(context.Background(), Options{In: strings.NewReader(tc.answer), Out: &out}))
			assert.Equal(t, tc.kept, dirExists(filepath.Join(f.root, ".ragcode")))
			assert.Contains(t, out.String(), "Remove "+filepath.Join(f.root, ".ragcode")+"?")
			assert.NoFileExists(t, f.dockerLog, "no -docker")
		})
	}
}

func dirExists(p string) bool {
	st, err := os.Stat(p)
	return err == nil && st.IsDir()
}

// Every file WriteIDERules appends the pointer to gets it stripped again.
func TestRunStripsEveryPointerFile(t *testing.T) {
	f := setup(t)
	require.NoError(t, os.MkdirAll(filepath.Join(f.root, ".github"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(f.root, "AGENTS.md"), []byte("# Agents"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(f.root, ".github", "copilot-instructions.md"), []byte("# Copilot"), 0o644))
	require.NoError(t, engine.WriteIDERules(f.root))
	require.NoError(t, Run(context.Background(), Options{Yes: true, Out: &bytes.Buffer{}}))
	agents, _ := os.ReadFile(filepath.Join(f.root, "AGENTS.md"))
	assert.Equal(t, "# Agents\n", string(agents))
	copilot, _ := os.ReadFile(filepath.Join(f.root, ".github", "copilot-instructions.md"))
	assert.Equal(t, "# Copilot\n", string(copilot))
}

// The index lives in RAGCODE_HOME/index: removing the home removes it, and
// no Qdrant is asked to drop anything.
func TestUninstallRemovesIndexDir(t *testing.T) {
	f := setup(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("unexpected HTTP call %s %s", r.Method, r.URL.Path)
	}))
	defer srv.Close()
	rag := filepath.Join(f.home, ".ragcode")
	require.NoError(t, os.WriteFile(filepath.Join(rag, "config.yaml"), []byte("qdrant_url: "+srv.URL+"\n"), 0o644))
	db := filepath.Join(rag, "index", "ragcode-x", "index.db")
	require.NoError(t, os.MkdirAll(filepath.Dir(db), 0o755))
	require.NoError(t, os.WriteFile(db, []byte("db"), 0o644))
	require.NoError(t, Run(context.Background(), Options{Yes: true, Out: &bytes.Buffer{}}))
	assert.NoDirExists(t, rag)
}

// A rules directory that is a symlink out of the workspace is left alone, as the writer leaves it.
func TestUninstallKeepsRulesBehindDirSymlink(t *testing.T) {
	home, root, shared := t.TempDir(), t.TempDir(), t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("RAGCODE_HOME", filepath.Join(home, ".ragcode"))
	t.Setenv("CODEX_HOME", filepath.Join(home, ".codex"))
	rag := filepath.Join(home, ".ragcode")
	if err := os.MkdirAll(rag, 0755); err != nil {
		t.Fatal(err)
	}
	roots, _ := json.Marshal([]string{root})
	if err := os.WriteFile(filepath.Join(rag, "roots.json"), roots, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, ".cursor"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(shared, filepath.Join(root, ".cursor", "rules")); err != nil {
		t.Fatal(err)
	}
	foreign := filepath.Join(shared, "ragcode.mdc")
	content := []byte("---\nalwaysApply: true\n---\n" + engine.IDEPointer + "\n")
	if err := os.WriteFile(foreign, content, 0600); err != nil {
		t.Fatal(err)
	}
	if err := engine.WriteIDERules(root); err == nil {
		t.Fatal("repro precondition: writer should refuse the external rules directory")
	}
	if err := Run(context.Background(), Options{Yes: true, Out: io.Discard}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(foreign); err != nil {
		t.Fatal("uninstall deleted a shared rule outside the workspace through a parent-directory symlink")
	}
}

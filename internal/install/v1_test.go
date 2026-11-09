package install

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRunRemovesV1(t *testing.T) {
	home, dockerLog, _ := env(t)
	v1Bin := filepath.Join(home, ".local", "share", "ragcode", "bin")
	require.NoError(t, os.MkdirAll(v1Bin, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(v1Bin, "rag-code-mcp"), []byte("v1"), 0o755))
	require.NoError(t, os.MkdirAll(filepath.Join(home, ".local", "share", "qdrant", "collections"), 0o755))
	rc := "alias ll='ls -l'\n\nexport PATH=\"" + v1Bin + ":$PATH\"\nexport EDITOR=vim\n"
	require.NoError(t, os.WriteFile(filepath.Join(home, ".bashrc"), []byte(rc), 0o644))
	cursor := filepath.Join(home, ".cursor", "mcp.config.json")
	require.NoError(t, os.MkdirAll(filepath.Dir(cursor), 0o755))
	require.NoError(t, os.WriteFile(cursor, []byte(`{"mcpServers":{"ragcode":{"command":"`+v1Bin+`/rag-code-mcp"},"github":{"command":"gh"}}}`), 0o644))

	var out bytes.Buffer
	require.NoError(t, Run(context.Background(), Options{Ollama: "skip", Yes: true, SkipIDE: true, Out: &out, srcDir: src(t)}), out.String())

	assert.NoDirExists(t, filepath.Join(home, ".local", "share", "ragcode"))
	assert.NoDirExists(t, filepath.Join(home, ".local", "share", "qdrant"))
	data, _ := os.ReadFile(filepath.Join(home, ".bashrc"))
	assert.NotContains(t, string(data), v1Bin)
	assert.Contains(t, string(data), "alias ll='ls -l'\n\nexport EDITOR=vim\n")
	data, _ = os.ReadFile(cursor)
	assert.NotContains(t, string(data), "ragcode")
	assert.Contains(t, string(data), "github")
	log, _ := os.ReadFile(dockerLog)
	assert.Contains(t, string(log), "rm -f ragcode-qdrant ragcode-ollama")
	assert.FileExists(t, filepath.Join(home, ".ragcode", "bin", "rag-code-mcp"))
}

// Answering no to "Replace?" must leave everything as it was, v1 included.
func TestRunCancelKeepsV1(t *testing.T) {
	home, _, _ := env(t)
	v1Dir := filepath.Join(home, ".local", "share", "ragcode")
	require.NoError(t, os.MkdirAll(v1Dir, 0o755))
	good := src(t)
	require.NoError(t, os.MkdirAll(filepath.Join(home, ".ragcode", "bin"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(home, ".ragcode", "bin", "rag-code-mcp"), []byte("v2"), 0o755))
	err := Run(context.Background(), Options{Ollama: "skip", SkipIDE: true, In: strings.NewReader("n\n"), Out: &bytes.Buffer{}, srcDir: good})
	require.ErrorContains(t, err, "cancelled")
	assert.DirExists(t, v1Dir)
}

// ~/.local/share/qdrant is a generic path: a user's own Qdrant may keep its
// collections there too. Only v1's ragcode collections are deleted.
func TestRunRemovesV1KeepsForeignQdrantCollections(t *testing.T) {
	home, _, _ := env(t)
	require.NoError(t, os.MkdirAll(filepath.Join(home, ".local", "share", "ragcode", "bin"), 0o755))
	colls := filepath.Join(home, ".local", "share", "qdrant", "collections")
	require.NoError(t, os.MkdirAll(filepath.Join(colls, "ragcode-abc123"), 0o755))
	require.NoError(t, os.MkdirAll(filepath.Join(colls, "my-app"), 0o755))

	var out bytes.Buffer
	require.NoError(t, Run(context.Background(), Options{Ollama: "skip", Yes: true, SkipIDE: true, Out: &out, srcDir: src(t)}), out.String())

	assert.NoDirExists(t, filepath.Join(colls, "ragcode-abc123"))
	assert.DirExists(t, filepath.Join(colls, "my-app"), "a collection RagCode did not create is kept")
}

// An unreadable collections directory may hold anything: Qdrant is kept, not
// deleted through the root container.
func TestRunRemovesV1KeepsUnreadableQdrant(t *testing.T) {
	home, dockerLog, _ := env(t)
	require.NoError(t, os.MkdirAll(filepath.Join(home, ".local", "share", "ragcode", "bin"), 0o755))
	colls := filepath.Join(home, ".local", "share", "qdrant", "collections")
	require.NoError(t, os.MkdirAll(filepath.Join(colls, "my-app"), 0o755))
	require.NoError(t, os.Chmod(colls, 0o000))
	t.Cleanup(func() { _ = os.Chmod(colls, 0o755) })

	var out bytes.Buffer
	require.NoError(t, Run(context.Background(), Options{Ollama: "skip", Yes: true, SkipIDE: true, Out: &out, srcDir: src(t)}), out.String())

	require.NoError(t, os.Chmod(colls, 0o755))
	assert.DirExists(t, filepath.Join(colls, "my-app"))
	log, _ := os.ReadFile(dockerLog)
	assert.NotContains(t, string(log), "/p/qdrant")
}

// v1 goes only once v2 is in place: a failed copy or a broken config leaves
// the working v1 untouched.
func TestRunFailureKeepsV1(t *testing.T) {
	for name, breakIt := range map[string]func(t *testing.T, home string){
		"copy": func(t *testing.T, home string) {
			require.NoError(t, os.MkdirAll(filepath.Join(home, ".ragcode"), 0o755))
			require.NoError(t, os.WriteFile(filepath.Join(home, ".ragcode", "bin"), nil, 0o644)) // bin is a file
		},
		"config": func(t *testing.T, home string) {
			require.NoError(t, os.MkdirAll(filepath.Join(home, ".ragcode"), 0o755))
			require.NoError(t, os.WriteFile(filepath.Join(home, ".ragcode", "config.yaml"), []byte("log_level: loud\n"), 0o644))
		},
	} {
		t.Run(name, func(t *testing.T) {
			home, dockerLog, _ := env(t)
			v1Dir := filepath.Join(home, ".local", "share", "ragcode")
			require.NoError(t, os.MkdirAll(v1Dir, 0o755))
			breakIt(t, home)
			err := Run(context.Background(), Options{Ollama: "skip", Yes: true, SkipIDE: true, Out: &bytes.Buffer{}, srcDir: src(t)})
			require.Error(t, err)
			assert.DirExists(t, v1Dir)
			log, _ := os.ReadFile(dockerLog)
			assert.NotContains(t, string(log), "ragcode-qdrant")
		})
	}
}

// A v1 server still running would keep serving from deleted files with its
// Qdrant gone: the upgrade stops it.
func TestRunStopsV1Server(t *testing.T) {
	home, _, _ := env(t)
	v1Bin := filepath.Join(home, ".local", "share", "ragcode", "bin", "rag-code-mcp")
	require.NoError(t, os.MkdirAll(filepath.Dir(v1Bin), 0o755))
	sleep, err := exec.LookPath("sleep")
	require.NoError(t, err)
	data, err := os.ReadFile(sleep)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(v1Bin, data, 0o755))
	server := exec.Command(v1Bin, "30")
	require.NoError(t, server.Start())
	t.Cleanup(func() { _ = server.Process.Kill() })
	done := make(chan error, 1)
	go func() { done <- server.Wait() }()
	time.Sleep(100 * time.Millisecond)

	require.NoError(t, Run(context.Background(), Options{Ollama: "skip", Yes: true, SkipIDE: true, Out: &bytes.Buffer{}, srcDir: src(t)}))
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("v1 server still running")
	}
}

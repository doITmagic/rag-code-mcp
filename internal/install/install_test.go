package install

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// env sets up a temp HOME, a fake docker on PATH that logs its arguments and
// fails on --gpus, and an httptest Ollama. It returns the home, the
// docker log path and the pulled model names.
func env(t *testing.T) (home, dockerLog string, pulled *[]string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("shell scripts")
	}
	home = t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("RAGCODE_HOME", "")
	t.Setenv("SHELL", "/bin/bash")
	bin := t.TempDir()
	dockerLog = filepath.Join(bin, "docker.log")
	script := "#!/bin/sh\necho \"$*\" >> " + dockerLog + "\ncase \"$*\" in *--gpus*) exit 1;; esac\n"
	require.NoError(t, os.WriteFile(filepath.Join(bin, "docker"), []byte(script), 0o755))
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))

	pulled = &[]string{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/readyz":
		case "/api/tags":
			w.Write([]byte(`{"models":[]}`))
		case "/api/pull":
			var req struct{ Name string }
			json.NewDecoder(r.Body).Decode(&req)
			*pulled = append(*pulled, req.Name)
			w.Write([]byte("{\"status\":\"pulling manifest\"}\n{\"status\":\"pulling\",\"completed\":50,\"total\":100}\n{\"status\":\"success\"}\n"))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	t.Setenv("OLLAMA_BASE_URL", srv.URL)
	return home, dockerLog, pulled
}

func src(t *testing.T) string {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "rag-code-mcp"), []byte("#!/bin/sh\necho server\n"), 0o644))
	return dir
}

func TestRun(t *testing.T) {
	home, dockerLog, pulled := env(t)
	require.NoError(t, os.MkdirAll(filepath.Join(home, ".cursor"), 0o755))
	var out bytes.Buffer
	opts := Options{Ollama: "docker", GPU: true, Yes: true, Out: &out, srcDir: src(t)}
	require.NoError(t, Run(context.Background(), opts), out.String())

	binary := filepath.Join(home, ".ragcode", "bin", "rag-code-mcp")
	st, err := os.Stat(binary)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o755), st.Mode().Perm())
	cfgPath := filepath.Join(home, ".ragcode", "config.yaml")
	assert.FileExists(t, cfgPath)

	log, _ := os.ReadFile(dockerLog)
	lines := strings.Split(strings.TrimSpace(string(log)), "\n")
	assert.Equal(t, []string{
		"pull -q ollama/ollama",
		"rm -f ragcode-ollama",
		"run -d --name ragcode-ollama --restart always --gpus all -p 127.0.0.1:11434:11434 -v ragcode-ollama:/root/.ollama ollama/ollama",
		"rm -f ragcode-ollama",
		"rm -f ragcode-ollama",
		"run -d --name ragcode-ollama --restart always -p 127.0.0.1:11434:11434 -v ragcode-ollama:/root/.ollama ollama/ollama",
	}, lines)
	assert.Equal(t, []string{"embeddinggemma-2:270m"}, *pulled)
	assert.Contains(t, out.String(), "pulling 50%")

	rc, _ := os.ReadFile(filepath.Join(home, ".bashrc"))
	pathLine := "# RagCode MCP\nexport PATH=\"" + filepath.Dir(binary) + ":$PATH\"\n"
	assert.Equal(t, 1, strings.Count(string(rc), pathLine))

	var cursor struct {
		Servers map[string]struct {
			Command string
			Args    []string
		} `json:"mcpServers"`
	}
	data, err := os.ReadFile(filepath.Join(home, ".cursor", "mcp.json"))
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal(data, &cursor))
	assert.Equal(t, binary, cursor.Servers["ragcode"].Command)
	assert.Equal(t, []string{"--config", cfgPath}, cursor.Servers["ragcode"].Args)
	assert.Contains(t, out.String(), `"command": "`+binary+`"`, "summary snippet")

	// Second run: config kept, PATH line not duplicated, no docker/model work.
	require.NoError(t, os.WriteFile(cfgPath, []byte("# keep\nlog_level: debug\n"), 0o644))
	require.NoError(t, os.Remove(dockerLog))
	*pulled = nil
	out.Reset()
	opts.Ollama, opts.SkipIDE = "skip", true
	require.NoError(t, Run(context.Background(), opts), out.String())
	data, _ = os.ReadFile(cfgPath)
	assert.Equal(t, "# keep\nlog_level: debug\n", string(data))
	rc, _ = os.ReadFile(filepath.Join(home, ".bashrc"))
	assert.Equal(t, 1, strings.Count(string(rc), pathLine))
	assert.NoFileExists(t, dockerLog)
	assert.Empty(t, *pulled)
}

func TestRunLocalOllamaAndModelsDir(t *testing.T) {
	home, dockerLog, pulled := env(t)
	models := filepath.Join(home, "models")
	var out bytes.Buffer
	require.NoError(t, Run(context.Background(), Options{Ollama: "local", Yes: true, SkipIDE: true, Out: &out, srcDir: src(t)}))
	assert.NoFileExists(t, dockerLog)
	assert.Len(t, *pulled, 1)

	*pulled = nil
	require.NoError(t, Run(context.Background(), Options{Ollama: "docker", ModelsDir: models, Yes: true, SkipIDE: true, Out: &out, srcDir: src(t)}))
	log, _ := os.ReadFile(dockerLog)
	assert.Contains(t, string(log), "-v "+models+":/root/.ollama ollama/ollama")
	assert.NotContains(t, string(log), "ragcode-qdrant")
	assert.DirExists(t, models)
	assert.Len(t, *pulled, 1)
}

func TestRunErrors(t *testing.T) {
	env(t)
	good := src(t)
	cases := []struct {
		name string
		opts Options
		want string
	}{
		{"bad ollama", Options{Ollama: "remote", srcDir: good}, "-ollama"},
		{"no binary", Options{Ollama: "skip", srcDir: t.TempDir()}, "next to the installer"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tc.opts.Out = &bytes.Buffer{}
			err := Run(context.Background(), tc.opts)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.want)
		})
	}

	// Existing install, user answers no.
	require.NoError(t, Run(context.Background(), Options{Ollama: "skip", Yes: true, SkipIDE: true, Out: &bytes.Buffer{}, srcDir: good}))
	err := Run(context.Background(), Options{Ollama: "skip", SkipIDE: true, In: strings.NewReader("n\n"), Out: &bytes.Buffer{}, srcDir: good})
	assert.ErrorContains(t, err, "cancelled")
}

func TestPullModelErrors(t *testing.T) {
	cases := []struct{ name, body, want string }{
		{"error event", `{"status":"pulling"}` + "\n" + `{"error":"model not found"}` + "\n", "model not found"},
		{"no success", `{"status":"pulling"}` + "\n", "without success"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(tc.body)) }))
			defer srv.Close()
			err := pullModel(context.Background(), &bytes.Buffer{}, srv.URL, "m")
			assert.ErrorContains(t, err, tc.want)
		})
	}
}

// An Ollama older than the model answers 412 with no message; say what to do.
func TestPullModelOllamaTooOld(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusPreconditionFailed) }))
	defer srv.Close()
	err := pullModel(context.Background(), &bytes.Buffer{}, srv.URL, "m")
	assert.ErrorContains(t, err, "newer Ollama")
}

// A failed Ollama container (port held by a native Ollama) must still pull the model.
func TestOllamaContainerFailStillPulls(t *testing.T) {
	_, dockerLog, pulled := env(t)
	fail := "#!/bin/sh\necho \"$*\" >> " + dockerLog + "\ncase \"$*\" in run*) exit 125;; esac\n"
	require.NoError(t, os.WriteFile(filepath.Join(filepath.Dir(dockerLog), "docker"), []byte(fail), 0o755))
	var out bytes.Buffer
	require.NoError(t, Run(context.Background(), Options{Ollama: "docker", Yes: true, SkipIDE: true, Out: &out, srcDir: src(t)}), out.String())
	assert.Equal(t, []string{"embeddinggemma-2:270m"}, *pulled)
}

// docker run reuses a cached image however old (seen: Ollama 0.13 from a
// 10-month-old ollama/ollama:latest could not pull the model); a failed pull
// (offline) falls back to the local image.
func TestOllamaImagePulledFirst(t *testing.T) {
	_, dockerLog, pulled := env(t)
	fail := "#!/bin/sh\necho \"$*\" >> " + dockerLog + "\ncase \"$*\" in pull*) exit 1;; esac\n"
	require.NoError(t, os.WriteFile(filepath.Join(filepath.Dir(dockerLog), "docker"), []byte(fail), 0o755))
	var out bytes.Buffer
	require.NoError(t, Run(context.Background(), Options{Ollama: "docker", Yes: true, SkipIDE: true, Out: &out, srcDir: src(t)}), out.String())
	log, _ := os.ReadFile(dockerLog)
	assert.True(t, strings.HasPrefix(string(log), "pull -q ollama/ollama\n"), string(log))
	assert.Contains(t, string(log), "run -d --name ragcode-ollama")
	assert.Contains(t, out.String(), "using the local image")
	assert.Equal(t, []string{"embeddinggemma-2:270m"}, *pulled)
}

// v2 keeps its index in SQLite files: the installer starts no Qdrant.
func TestInstallStartsNoQdrant(t *testing.T) {
	_, dockerLog, _ := env(t)
	var out bytes.Buffer
	require.NoError(t, Run(context.Background(), Options{Ollama: "docker", Yes: true, SkipIDE: true, Out: &out, srcDir: src(t)}), out.String())
	log, _ := os.ReadFile(dockerLog)
	for _, line := range strings.Split(string(log), "\n") {
		if strings.HasPrefix(line, "run ") {
			assert.NotContains(t, line, "qdrant")
		}
	}
	assert.Contains(t, string(log), "ragcode-ollama")
}

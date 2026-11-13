package install

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

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

// A relative -models-dir is mounted as the host directory it creates, made
// absolute, not passed to docker -v where a bare name means a named volume.
func TestRunRelativeModelsDirMountsHostDir(t *testing.T) {
	_, dockerLog, _ := env(t)

	work := t.TempDir()
	wd, err := os.Getwd()
	require.NoError(t, err)
	require.NoError(t, os.Chdir(work))
	t.Cleanup(func() { _ = os.Chdir(wd) })

	var out bytes.Buffer
	opts := Options{
		Ollama:    "docker",
		ModelsDir: "ollama-models",
		SkipIDE:   true,
		Yes:       true,
		Out:       &out,
		srcDir:    src(t),
	}
	require.NoError(t, Run(context.Background(), opts), out.String())

	hostDir := filepath.Join(work, "ollama-models")
	require.DirExists(t, hostDir)

	rawLog, err := os.ReadFile(dockerLog)
	require.NoError(t, err)
	assert.Contains(t, string(rawLog), "-v "+hostDir+":/root/.ollama")
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

// RAGCODE_HOME set to the v1 directory: the v1 cleanup must not delete the new install.
func TestInstallIntoV1DirKeepsItself(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fake docker is a shell script")
	}
	home := t.TempDir()
	legacy := filepath.Join(home, ".local", "share", "ragcode")
	t.Setenv("HOME", home)
	t.Setenv("RAGCODE_HOME", legacy)
	t.Setenv("SHELL", "/bin/bash")
	t.Setenv("OLLAMA_BASE_URL", "http://localhost:11434")
	t.Setenv("MCP_LOG_LEVEL", "info")
	bin := t.TempDir()
	if err := os.WriteFile(filepath.Join(bin, "docker"), []byte("#!/bin/sh\nexit 0\n"), 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin)
	if err := Run(context.Background(), Options{Ollama: "skip", Yes: true, SkipIDE: true, Out: io.Discard, srcDir: src(t)}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(legacy, "bin", "rag-code-mcp")); err != nil {
		t.Fatal("installer returned success but v1 cleanup deleted the newly installed binary")
	}
}

// A password in OLLAMA_BASE_URL is not printed.
func TestInstallHidesOllamaPassword(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("RAGCODE_HOME", filepath.Join(home, ".ragcode"))
	t.Setenv("SHELL", "/bin/bash")
	t.Setenv("MCP_LOG_LEVEL", "info")
	t.Setenv("OLLAMA_BASE_URL", "https://alice:private-ollama-password@ollama.example.test")
	var output bytes.Buffer
	if err := Run(context.Background(), Options{Ollama: "skip", Yes: true, SkipIDE: true, Out: &output, srcDir: src(t)}); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(output.String(), "private-ollama-password") {
		t.Fatal("installer printed the Ollama URL password in its summary")
	}
}

// The wait for Ollama times out without printing the password of a
// basic-auth ollama_base_url.
func TestWaitHTTPHidesPassword(t *testing.T) {
	t.Setenv("RAGCODE_HOME", t.TempDir())
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	t.Cleanup(srv.Close)

	u, err := url.Parse(srv.URL)
	require.NoError(t, err)
	u.User = url.UserPassword("raguser", "superSecretPass99")

	err = waitHTTP(context.Background(), u.String()+"/api/tags", 1*time.Millisecond)
	require.Error(t, err)
	assert.NotContains(t, err.Error(), "superSecretPass99")
}

// The Windows PATH hint appends to the user Path only and works pasted in cmd
// or PowerShell: no setx (truncates at 1024 chars, copies the machine PATH)
// and no %PATH% (stored literally by PowerShell).
func TestWindowsPathHint(t *testing.T) {
	h := windowsPathHint(`C:\Users\O'Brien\.ragcode\bin`)
	assert.Equal(t, `powershell -NoProfile -Command "[Environment]::SetEnvironmentVariable('Path', [Environment]::GetEnvironmentVariable('Path','User') + ';C:\Users\O''Brien\.ragcode\bin', 'User')"`, h)
	assert.NotContains(t, h, "setx")
	assert.NotContains(t, h, "%PATH%")
}

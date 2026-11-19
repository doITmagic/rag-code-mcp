package install

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/doITmagic/rag-code-mcp/v2/internal/llama"
)

func TestMain(m *testing.M) {
	// Models dirs are mounted symlink-resolved; on macOS TMPDIR sits under the
	// /var -> /private/var symlink, so t.TempDir() must be resolved too.
	// On Windows it expands 8.3 short names (RUNNER~1) the same way.
	if d, err := filepath.EvalSymlinks(os.TempDir()); err == nil {
		for _, v := range []string{"TMPDIR", "TMP", "TEMP"} { // TMP, TEMP: Windows
			os.Setenv(v, d)
		}
	}
	os.Exit(m.Run())
}

// env sets up a temp HOME and a fake docker first on PATH that logs its
// arguments and keeps a ragcode-ollama container and volume as files next to
// it: "container" (listed by ps and inspect), "image" and "mounts" (what
// inspect reports; ollamaContainer sets them), "volume" (listed by volume ls);
// "volfail" makes `volume rm` fail; "pending-at-rm" is created when `rm -f`
// runs while cache/ollama-volume-pending exists. It returns the home and the docker log
// path. The real docker is never run.
func env(t *testing.T) (home, dockerLog string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("shell scripts")
	}
	home = t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("RAGCODE_HOME", "")
	t.Setenv("SHELL", "/bin/bash")
	t.Setenv("RAGCODE_EMBED_MODEL", "")
	t.Setenv("RAGCODE_LLAMA_SERVER", "")
	t.Setenv("RAGCODE_EMBED_RUNTIME", "")
	bin := t.TempDir()
	dockerLog = filepath.Join(bin, "docker.log")
	script := `#!/bin/sh
D=` + bin + `
echo "$*" >> $D/docker.log
case "$1 $2 $3" in
"ps -a "*) [ -f $D/container ] && echo ragcode-ollama; exit 0;;
"container inspect "*)
  if [ ! -f $D/container ]; then echo "Error: No such container: ragcode-ollama"; exit 1; fi
  echo "$(cat $D/image)|$(cat $D/mounts)"; exit 0;;
"rm -f ragcode-ollama") if [ -f $D/rmfail ]; then echo "cannot remove the container"; exit 1; fi; [ -f "$HOME/.ragcode/cache/ollama-volume-pending" ] && touch $D/pending-at-rm; rm -f $D/container; exit 0;;
"volume ls "*) [ -f $D/volume ] && echo ragcode-ollama; exit 0;;
"volume rm ragcode-ollama") if [ -f $D/volfail ]; then echo "volume is in use"; exit 1; fi; rm -f $D/volume; exit 0;;
esac
exit 0
`
	require.NoError(t, os.WriteFile(filepath.Join(bin, "docker"), []byte(script), 0o755))
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	return home, dockerLog
}

// ollamaContainer makes the fake docker list a v2 container that mounts the
// named volume ragcode-ollama, which exists.
func ollamaContainer(t *testing.T, dockerLog, image string) {
	t.Helper()
	bin := filepath.Dir(dockerLog)
	for name, content := range map[string]string{"container": "", "volume": "", "image": image, "mounts": "volume=ragcode-ollama=/root/.ollama;"} {
		require.NoError(t, os.WriteFile(filepath.Join(bin, name), []byte(content), 0o644))
	}
}

// countEnsure replaces the download with a counter that returns err.
func countEnsure(err error) (func(context.Context, llama.Options) error, *int) {
	n := new(int)
	return func(context.Context, llama.Options) error { *n++; return err }, n
}

// exe is the server binary suffix the installer expects next to itself.
var exe = map[bool]string{true: ".exe"}[runtime.GOOS == "windows"]

func src(t *testing.T) string {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "rag-code-mcp"+exe), []byte("#!/bin/sh\necho server\n"), 0o644))
	return dir
}

func TestRun(t *testing.T) {
	home, dockerLog := env(t)
	require.NoError(t, os.MkdirAll(filepath.Join(home, ".cursor"), 0o755))
	var out bytes.Buffer
	ensure, calls := countEnsure(nil)
	opts := Options{Yes: true, Out: &out, srcDir: src(t), ensure: ensure}
	require.NoError(t, Run(context.Background(), opts), out.String())
	assert.Equal(t, 1, *calls, "the runtime and model are fetched once")
	assert.Contains(t, out.String(), "Embedding runtime and model ready")
	assert.Contains(t, out.String(), "Embedder: ")

	binary := filepath.Join(home, ".ragcode", "bin", "rag-code-mcp")
	st, err := os.Stat(binary)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o755), st.Mode().Perm())
	cfgPath := filepath.Join(home, ".ragcode", "config.yaml")
	assert.FileExists(t, cfgPath)

	// Docker is only asked for a leftover container; nothing is started or pulled.
	log, _ := os.ReadFile(dockerLog)
	assert.Equal(t, "ps -a --filter name=^ragcode-ollama$ --format {{.Names}}", strings.TrimSpace(string(log)))

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

	// Second run: config kept, PATH line not duplicated.
	require.NoError(t, os.WriteFile(cfgPath, []byte("# keep\nlog_level: debug\n"), 0o644))
	out.Reset()
	opts.SkipIDE = true
	require.NoError(t, Run(context.Background(), opts), out.String())
	data, _ = os.ReadFile(cfgPath)
	assert.Equal(t, "# keep\nlog_level: debug\n", string(data))
	rc, _ = os.ReadFile(filepath.Join(home, ".bashrc"))
	assert.Equal(t, 1, strings.Count(string(rc), pathLine))
}

func TestRunEnsureHook(t *testing.T) {
	env(t)
	// Files missing: ensure runs once.
	var out bytes.Buffer
	ensure, calls := countEnsure(nil)
	require.NoError(t, Run(context.Background(), Options{Yes: true, SkipIDE: true, Out: &out, srcDir: src(t), ensure: ensure}), out.String())
	assert.Equal(t, 1, *calls)

	// SkipDownload: not at all.
	ensure, calls = countEnsure(nil)
	out.Reset()
	require.NoError(t, Run(context.Background(), Options{Yes: true, SkipIDE: true, SkipDownload: true, Out: &out, srcDir: src(t), ensure: ensure}), out.String())
	assert.Zero(t, *calls)
	assert.Contains(t, out.String(), "-skip-download")

	// A failing ensure is a warning: the server fetches on first use.
	ensure, calls = countEnsure(errors.New("network down"))
	out.Reset()
	require.NoError(t, Run(context.Background(), Options{Yes: true, SkipIDE: true, Out: &out, srcDir: src(t), ensure: ensure}), out.String())
	assert.Equal(t, 1, *calls)
	assert.Contains(t, out.String(), "network down")
	assert.Contains(t, out.String(), "tries again on first use")
	assert.NotContains(t, out.String(), "Embedding runtime and model ready")
	assert.Contains(t, out.String(), "RagCode MCP installed")
}

// With both files present (user-provided paths) nothing is fetched.
func TestRunEnsureNotCalledWhenInstalled(t *testing.T) {
	home, _ := env(t)
	d := t.TempDir()
	model, server := filepath.Join(d, "m.gguf"), filepath.Join(d, "llama-server")
	require.NoError(t, os.WriteFile(model, []byte("m"), 0o644))
	require.NoError(t, os.WriteFile(server, []byte("s"), 0o755))
	require.NoError(t, os.MkdirAll(filepath.Join(home, ".ragcode"), 0o755))
	cfg := "embed_model: " + model + "\nllama_server: " + server + "\n"
	require.NoError(t, os.WriteFile(filepath.Join(home, ".ragcode", "config.yaml"), []byte(cfg), 0o644))
	ensure, calls := countEnsure(nil)
	var out bytes.Buffer
	require.NoError(t, Run(context.Background(), Options{Yes: true, SkipIDE: true, Out: &out, srcDir: src(t), ensure: ensure}), out.String())
	assert.Zero(t, *calls)
	assert.Contains(t, out.String(), "already present")
	assert.Contains(t, out.String(), server)
}

// A ragcode-ollama container left by an earlier installer is removed, with its volume.
func TestRunRemovesOldOllamaContainer(t *testing.T) {
	_, dockerLog := env(t)
	ollamaContainer(t, dockerLog, "ollama/ollama")
	var out bytes.Buffer
	require.NoError(t, Run(context.Background(), Options{Yes: true, SkipIDE: true, SkipDownload: true, Out: &out, srcDir: src(t)}), out.String())
	log, _ := os.ReadFile(dockerLog)
	lines := strings.Split(strings.TrimSpace(string(log)), "\n")
	require.Len(t, lines, 4, string(log))
	assert.Equal(t, "ps -a --filter name=^ragcode-ollama$ --format {{.Names}}", lines[0])
	assert.True(t, strings.HasPrefix(lines[1], "container inspect "), lines[1])
	assert.Equal(t, "rm -f ragcode-ollama", lines[2])
	assert.Equal(t, "volume rm ragcode-ollama", lines[3])
	for _, l := range lines {
		assert.NotContains(t, l, "run", "no container is started")
		assert.NotContains(t, l, "pull")
	}
	assert.Contains(t, out.String(), "Removed the ragcode-ollama container")
}

// A container that only has the name is somebody else's: it stays, with a warning.
func TestRunKeepsForeignOllamaContainer(t *testing.T) {
	_, dockerLog := env(t)
	ollamaContainer(t, dockerLog, "nginx")
	var out bytes.Buffer
	require.NoError(t, Run(context.Background(), Options{Yes: true, SkipIDE: true, SkipDownload: true, Out: &out, srcDir: src(t)}), out.String())
	log, _ := os.ReadFile(dockerLog)
	assert.NotContains(t, string(log), "rm ")
	assert.Contains(t, out.String(), "container ragcode-ollama is not an Ollama of RagCode, left alone")
	assert.NotContains(t, out.String(), "Removed the ragcode-ollama container")
}

// A volume that cannot be removed is reported with Docker's answer and the
// commands to finish by hand; the install goes on.
func TestRunOllamaVolumeFails(t *testing.T) {
	_, dockerLog := env(t)
	ollamaContainer(t, dockerLog, "ollama/ollama")
	require.NoError(t, os.WriteFile(filepath.Join(filepath.Dir(dockerLog), "volfail"), nil, 0o644))
	var out bytes.Buffer
	require.NoError(t, Run(context.Background(), Options{Yes: true, SkipIDE: true, SkipDownload: true, Out: &out, srcDir: src(t)}), out.String())
	assert.Contains(t, out.String(), "volume is in use")
	assert.Contains(t, out.String(), "docker rm -f ragcode-ollama")
	assert.Contains(t, out.String(), "docker volume rm ragcode-ollama")
	assert.Contains(t, out.String(), "RagCode MCP installed")
}

// The container is gone but its volume is not: the installer leaves the proof
// that the volume was RagCode's, which the server's next start retries from,
// and takes back the marker of an earlier start so that it does look.
func TestRunOllamaVolumeFailsLeavesPending(t *testing.T) {
	home, dockerLog := env(t)
	ollamaContainer(t, dockerLog, "ollama/ollama")
	require.NoError(t, os.WriteFile(filepath.Join(filepath.Dir(dockerLog), "volfail"), nil, 0o644))
	cache := filepath.Join(home, ".ragcode", "cache")
	require.NoError(t, os.MkdirAll(cache, 0o755))
	marker := filepath.Join(cache, "ollama-removed")
	require.NoError(t, os.WriteFile(marker, nil, 0o644))
	var out bytes.Buffer
	require.NoError(t, Run(context.Background(), Options{Yes: true, SkipIDE: true, SkipDownload: true, Out: &out, srcDir: src(t)}), out.String())
	assert.FileExists(t, filepath.Join(filepath.Dir(dockerLog), "pending-at-rm"))
	assert.FileExists(t, filepath.Join(cache, "ollama-volume-pending"))
	assert.NoFileExists(t, marker)
}

// The container cannot be removed with the proof already written: the proof
// stays, the marker of an earlier start goes (or the server would never look
// again), and the user is told how to finish by hand.
func TestRunOllamaContainerFailsLeavesPending(t *testing.T) {
	home, dockerLog := env(t)
	ollamaContainer(t, dockerLog, "ollama/ollama")
	require.NoError(t, os.WriteFile(filepath.Join(filepath.Dir(dockerLog), "rmfail"), nil, 0o644))
	cache := filepath.Join(home, ".ragcode", "cache")
	require.NoError(t, os.MkdirAll(cache, 0o755))
	marker := filepath.Join(cache, "ollama-removed")
	require.NoError(t, os.WriteFile(marker, nil, 0o644))
	var out bytes.Buffer
	require.NoError(t, Run(context.Background(), Options{Yes: true, SkipIDE: true, SkipDownload: true, Out: &out, srcDir: src(t)}), out.String())
	assert.FileExists(t, filepath.Join(cache, "ollama-volume-pending"))
	assert.NoFileExists(t, marker)
	assert.Contains(t, out.String(), "docker rm -f ragcode-ollama")
	assert.Contains(t, out.String(), "RagCode MCP installed")
}

// The proof that the volume is RagCode's is written before the container is
// removed, so an installer that dies in between leaves it for the next start;
// once the volume is really gone the proof goes too.
func TestRunOllamaVolumeRemovedLeavesNoPending(t *testing.T) {
	home, dockerLog := env(t)
	ollamaContainer(t, dockerLog, "ollama/ollama")
	var out bytes.Buffer
	require.NoError(t, Run(context.Background(), Options{Yes: true, SkipIDE: true, SkipDownload: true, Out: &out, srcDir: src(t)}), out.String())
	assert.FileExists(t, filepath.Join(filepath.Dir(dockerLog), "pending-at-rm"), "pending file must exist when rm -f runs")
	assert.NoFileExists(t, filepath.Join(home, ".ragcode", "cache", "ollama-volume-pending"))
}

// A container without the named volume (a host directory, v1) needs no proof.
func TestRunOllamaHostDirWritesNoPending(t *testing.T) {
	home, dockerLog := env(t)
	ollamaContainer(t, dockerLog, "ollama/ollama")
	require.NoError(t, os.WriteFile(filepath.Join(filepath.Dir(dockerLog), "mounts"), []byte("bind=/home/u/models=/root/.ollama;"), 0o644))
	var out bytes.Buffer
	require.NoError(t, Run(context.Background(), Options{Yes: true, SkipIDE: true, SkipDownload: true, Out: &out, srcDir: src(t)}), out.String())
	assert.NoFileExists(t, filepath.Join(filepath.Dir(dockerLog), "pending-at-rm"))
	assert.NoFileExists(t, filepath.Join(home, ".ragcode", "cache", "ollama-volume-pending"))
}

// A proof left by an installer that died after removing the container: the
// volume is removed now and the proof with it.
func TestRunOllamaPendingVolumeRetried(t *testing.T) {
	home, dockerLog := env(t)
	cache := filepath.Join(home, ".ragcode", "cache")
	require.NoError(t, os.MkdirAll(cache, 0o755))
	pending := filepath.Join(cache, "ollama-volume-pending")
	require.NoError(t, os.WriteFile(pending, nil, 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(filepath.Dir(dockerLog), "volume"), nil, 0o644))
	var out bytes.Buffer
	require.NoError(t, Run(context.Background(), Options{Yes: true, SkipIDE: true, SkipDownload: true, Out: &out, srcDir: src(t)}), out.String())
	log, _ := os.ReadFile(dockerLog)
	assert.Contains(t, string(log), "volume rm ragcode-ollama")
	assert.NoFileExists(t, pending)
}

// A volume of that name without a container is not proven to be RagCode's: it is left.
func TestRunKeepsOrphanOllamaVolume(t *testing.T) {
	_, dockerLog := env(t)
	require.NoError(t, os.WriteFile(filepath.Join(filepath.Dir(dockerLog), "volume"), nil, 0o644))
	var out bytes.Buffer
	require.NoError(t, Run(context.Background(), Options{Yes: true, SkipIDE: true, SkipDownload: true, Out: &out, srcDir: src(t)}), out.String())
	log, _ := os.ReadFile(dockerLog)
	assert.NotContains(t, string(log), "volume rm")
}

// Without docker on PATH the leftover check is skipped silently.
func TestRunWithoutDocker(t *testing.T) {
	env(t)
	t.Setenv("PATH", t.TempDir())
	var out bytes.Buffer
	require.NoError(t, Run(context.Background(), Options{Yes: true, SkipIDE: true, SkipDownload: true, Out: &out, srcDir: src(t)}), out.String())
}

func TestRunErrors(t *testing.T) {
	env(t)
	good := src(t)
	cases := []struct {
		name string
		opts Options
		want string
	}{
		{"no binary", Options{SkipDownload: true, srcDir: t.TempDir()}, "next to the installer"},
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
	require.NoError(t, Run(context.Background(), Options{SkipDownload: true, Yes: true, SkipIDE: true, Out: &bytes.Buffer{}, srcDir: good}))
	err := Run(context.Background(), Options{SkipDownload: true, SkipIDE: true, In: strings.NewReader("n\n"), Out: &bytes.Buffer{}, srcDir: good})
	assert.ErrorContains(t, err, "cancelled")
}

// v2 keeps its index in SQLite files: the installer starts no Qdrant and no container.
func TestInstallStartsNoContainer(t *testing.T) {
	_, dockerLog := env(t)
	var out bytes.Buffer
	require.NoError(t, Run(context.Background(), Options{Yes: true, SkipIDE: true, SkipDownload: true, Out: &out, srcDir: src(t)}), out.String())
	log, _ := os.ReadFile(dockerLog)
	for _, line := range strings.Split(string(log), "\n") {
		assert.False(t, strings.HasPrefix(line, "run "), line)
		assert.NotContains(t, line, "qdrant")
	}
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
	t.Setenv("MCP_LOG_LEVEL", "info")
	bin := t.TempDir()
	if err := os.WriteFile(filepath.Join(bin, "docker"), []byte("#!/bin/sh\nexit 0\n"), 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin)
	if err := Run(context.Background(), Options{SkipDownload: true, Yes: true, SkipIDE: true, Out: io.Discard, srcDir: src(t)}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(legacy, "bin", "rag-code-mcp")); err != nil {
		t.Fatal("installer returned success but v1 cleanup deleted the newly installed binary")
	}
}

// The Windows PATH hint appends to the user Path only and works pasted in cmd
// or PowerShell: no setx (truncates at 1024 chars, copies the machine PATH)
// and no %PATH% (stored literally by PowerShell).
func TestWindowsPathHint(t *testing.T) {
	h := windowsPathHint(`C:\Users\O'Brien\.ragcode\bin`)
	assert.Equal(t, `powershell -NoProfile -Command "[Microsoft.Win32.Registry]::CurrentUser.CreateSubKey('Environment').SetValue('Path', [Microsoft.Win32.Registry]::CurrentUser.OpenSubKey('Environment').GetValue('Path', '', 'DoNotExpandEnvironmentNames') + ';C:\Users\O''Brien\.ragcode\bin', 'ExpandString'); [Environment]::SetEnvironmentVariable('RAGCODE_PATH_REFRESH', '1', 'User'); [Environment]::SetEnvironmentVariable('RAGCODE_PATH_REFRESH', '', 'User')"`, h)
	assert.NotContains(t, h, "setx")
	assert.NotContains(t, h, "%PATH%")
	assert.NotContains(t, h, "$", "an outer PowerShell would expand it")
}

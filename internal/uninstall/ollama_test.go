package uninstall

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ollamaMounts is what `docker container inspect` prints for the mounts of a
// v2 container started without -models-dir: the named volume.
const ollamaMounts = "volume=ragcode-ollama=/root/.ollama;"

// fakeDocker puts a docker on PATH that keeps the state of a ragcode-ollama
// container and volume in files next to it: "container" (listed by ps and
// inspect), "image" and "mounts" (what inspect reports), "volume" (listed by
// volume ls); "down" makes every command fail, "vanish" makes inspect find no
// container although ps listed one, "inspectfail" makes it fail, "rmfail" and "volfail" make `rm -f` and
// `volume rm` fail. The real docker is never run.
type fakeDocker struct {
	dir string
}

func newFakeDocker(t *testing.T) *fakeDocker {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("shell scripts")
	}
	d := &fakeDocker{dir: t.TempDir()}
	script := `#!/bin/sh
D=` + d.dir + `
echo "$*" >> $D/docker.log
[ -f $D/down ] && exit 1
case "$1 $2 $3" in
"ps -a "*) [ -f $D/container ] && echo ragcode-ollama; exit 0;;
"container inspect "*)
  if [ -f $D/inspectfail ]; then echo "Cannot connect to the Docker daemon"; exit 1; fi
  if [ ! -f $D/container ] || [ -f $D/vanish ]; then rm -f $D/container; echo "Error: No such container: ragcode-ollama"; exit 1; fi
  echo "$(cat $D/image)|$(cat $D/mounts)"; exit 0;;
"rm -f ragcode-ollama") if [ -f $D/rmfail ]; then echo "cannot remove"; exit 1; fi; rm -f $D/container; exit 0;;
"volume ls "*) [ -f $D/volume ] && echo ragcode-ollama; exit 0;;
"volume rm ragcode-ollama") if [ -f $D/volfail ]; then echo "volume is in use"; exit 1; fi; rm -f $D/volume; exit 0;;
esac
exit 0
`
	require.NoError(t, os.WriteFile(filepath.Join(d.dir, "docker"), []byte(script), 0o755))
	t.Setenv("PATH", d.dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return d
}

func (d *fakeDocker) set(t *testing.T, name string, content ...string) {
	t.Helper()
	require.NoError(t, os.WriteFile(filepath.Join(d.dir, name), []byte(strings.Join(content, "")), 0o644))
}

func (d *fakeDocker) unset(t *testing.T, name string) {
	t.Helper()
	require.NoError(t, os.Remove(filepath.Join(d.dir, name)))
}

func (d *fakeDocker) has(name string) bool {
	_, err := os.Stat(filepath.Join(d.dir, name))
	return err == nil
}

// ollama makes ps list a container with that image and these mounts.
func (d *fakeDocker) ollama(t *testing.T, image, mounts string) {
	d.set(t, "container")
	d.set(t, "image", image)
	d.set(t, "mounts", mounts)
}

// calls is the docker log with ps and inspect shortened to their verb.
func (d *fakeDocker) calls() []string {
	data, _ := os.ReadFile(filepath.Join(d.dir, "docker.log"))
	var out []string
	for _, l := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		switch {
		case l == "":
		case strings.HasPrefix(l, "ps "):
			out = append(out, "ps")
		case strings.HasPrefix(l, "container inspect "):
			out = append(out, "inspect")
		default:
			out = append(out, l)
		}
	}
	return out
}

func TestRemoveOllamaOurs(t *testing.T) {
	d := newFakeDocker(t)
	d.ollama(t, "ollama/ollama", ollamaMounts)
	d.set(t, "volume")
	out, err := RemoveOllama(context.Background())
	require.NoError(t, err)
	assert.Equal(t, Removed, out)
	assert.Equal(t, []string{"ps", "inspect", "rm -f ragcode-ollama", "volume rm ragcode-ollama"}, d.calls())
	assert.False(t, d.has("container"))
	assert.False(t, d.has("volume"))
}

// A host directory (-models-dir, v1) is the user's: the container goes, no volume is touched.
func TestRemoveOllamaHostDirMount(t *testing.T) {
	d := newFakeDocker(t)
	d.ollama(t, "ollama/ollama:latest", "bind==/root/.ollama;")
	d.set(t, "volume") // a volume of that name that this container never mounted
	out, err := RemoveOllama(context.Background())
	require.NoError(t, err)
	assert.Equal(t, Removed, out)
	assert.Equal(t, []string{"ps", "inspect", "rm -f ragcode-ollama"}, d.calls())
	assert.True(t, d.has("volume"))
}

// Only the named volume ragcode-ollama is ever removed, not another volume at /root/.ollama.
func TestRemoveOllamaOtherVolumeKept(t *testing.T) {
	d := newFakeDocker(t)
	d.ollama(t, "ollama/ollama", "volume=my-models=/root/.ollama;")
	out, err := RemoveOllama(context.Background())
	require.NoError(t, err)
	assert.Equal(t, Removed, out)
	assert.Equal(t, []string{"ps", "inspect", "rm -f ragcode-ollama"}, d.calls())
}

func TestRemoveOllamaImages(t *testing.T) {
	for image, want := range map[string]Outcome{
		"ollama/ollama":                   Removed,
		"ollama/ollama:latest":            Removed,
		"ollama/ollama:0.5.1":             Removed,
		"ollama/ollama@sha256:abc123":     Removed,
		"ollama/ollama:0.5@sha256:abc123": Removed,
		"docker.io/ollama/ollama:latest":  Removed,
		"localhost:5000/ollama/ollama":    Removed,
		"evil/ollama":                     Foreign,
		"ollama/ollama-fork":              Foreign,
		"ollama":                          Foreign,
		"nginx":                           Foreign,
		"notollama/ollama":                Foreign,
	} {
		t.Run(image, func(t *testing.T) {
			d := newFakeDocker(t)
			d.ollama(t, image, "bind==/root/.ollama;")
			out, err := RemoveOllama(context.Background())
			require.NoError(t, err)
			assert.Equal(t, want, out)
			assert.Equal(t, want == Foreign, d.has("container"), "a foreign container is not removed")
		})
	}
}

// The right image without the model directory is somebody else's container.
func TestRemoveOllamaForeignMounts(t *testing.T) {
	d := newFakeDocker(t)
	d.ollama(t, "ollama/ollama", "volume=ragcode-ollama=/data;bind==/srv;")
	out, err := RemoveOllama(context.Background())
	require.NoError(t, err)
	assert.Equal(t, Foreign, out)
	assert.Equal(t, []string{"ps", "inspect"}, d.calls())
	assert.True(t, d.has("container"))
}

func TestRemoveOllamaAbsent(t *testing.T) {
	d := newFakeDocker(t)
	d.set(t, "volume") // an orphan volume cannot be proven to be RagCode's
	out, err := RemoveOllama(context.Background())
	require.NoError(t, err)
	assert.Equal(t, Absent, out)
	assert.Equal(t, []string{"ps"}, d.calls())
	assert.True(t, d.has("volume"))
}

// The container disappears between ps and inspect: nothing is left to remove.
func TestRemoveOllamaVanished(t *testing.T) {
	d := newFakeDocker(t)
	d.ollama(t, "ollama/ollama", ollamaMounts)
	d.set(t, "vanish")
	out, err := RemoveOllama(context.Background())
	require.NoError(t, err)
	assert.Equal(t, Absent, out)
	assert.Equal(t, []string{"ps", "inspect"}, d.calls())
}

func TestRemoveOllamaNoDocker(t *testing.T) {
	d := newFakeDocker(t)
	d.ollama(t, "ollama/ollama", ollamaMounts)
	d.set(t, "down")
	_, err := RemoveOllama(context.Background())
	assert.ErrorIs(t, err, ErrNoDocker)
	assert.Equal(t, []string{"ps"}, d.calls())

	t.Setenv("PATH", t.TempDir())
	_, err = RemoveOllama(context.Background())
	assert.ErrorIs(t, err, ErrNoDocker)
}

// Docker answered ps but not inspect: that is not "absent" and not "foreign".
func TestRemoveOllamaInspectFails(t *testing.T) {
	d := newFakeDocker(t)
	d.ollama(t, "ollama/ollama", ollamaMounts)
	d.set(t, "inspectfail")
	out, err := RemoveOllama(context.Background())
	require.Error(t, err)
	assert.NotErrorIs(t, err, ErrNoDocker)
	assert.Equal(t, Absent, out)
	assert.ErrorContains(t, err, "Cannot connect")
	assert.Equal(t, []string{"ps", "inspect"}, d.calls())
	assert.True(t, d.has("container"))
}

// rm -f reports an error but the container is gone: that is a success.
func TestRemoveOllamaRmFailsButGone(t *testing.T) {
	d := newFakeDocker(t)
	d.ollama(t, "ollama/ollama", ollamaMounts)
	d.set(t, "rmfail")
	out, err := RemoveOllama(context.Background())
	require.Error(t, err)
	assert.Equal(t, Ours, out)
	assert.ErrorContains(t, err, "cannot remove")
	assert.True(t, d.has("container"))

	d.unset(t, "container") // removed by someone else while rm -f was failing
	require.NoError(t, DropOllamaContainer(context.Background()))
}

// A volume that cannot be removed is an error that says so, with the container already gone.
func TestRemoveOllamaVolumeFails(t *testing.T) {
	d := newFakeDocker(t)
	d.ollama(t, "ollama/ollama", ollamaMounts)
	d.set(t, "volume")
	d.set(t, "volfail")
	out, err := RemoveOllama(context.Background())
	assert.Equal(t, Removed, out)
	require.Error(t, err)
	assert.ErrorContains(t, err, "volume ragcode-ollama not removed")
	assert.ErrorContains(t, err, "volume is in use")
	assert.Equal(t, []string{"ps", "inspect", "rm -f ragcode-ollama", "volume rm ragcode-ollama", "volume ls -q --filter name=^ragcode-ollama$"}, d.calls())
}

// volume rm fails because the volume is already gone: success.
func TestRemoveOllamaVolumeGoneMeanwhile(t *testing.T) {
	d := newFakeDocker(t)
	d.set(t, "volfail")
	require.NoError(t, RemoveOllamaVolume(context.Background()))
}

func TestRemoveOllamaVolume(t *testing.T) {
	d := newFakeDocker(t)
	d.set(t, "volume")
	require.NoError(t, RemoveOllamaVolume(context.Background()))
	assert.False(t, d.has("volume"))
}

// FindOllama alone removes nothing and reports the volume the container mounts.
func TestFindOllama(t *testing.T) {
	d := newFakeDocker(t)
	d.ollama(t, "ollama/ollama", ollamaMounts)
	c, volume, err := FindOllama(context.Background())
	require.NoError(t, err)
	assert.Equal(t, Ours, c)
	assert.Equal(t, "ragcode-ollama", volume)
	assert.Equal(t, []string{"ps", "inspect"}, d.calls())
	assert.True(t, d.has("container"))
}

// -docker removes the Ollama container only when it is RagCode's, and its
// volume by name even without a container; failures are shown.
func TestRunDockerOllama(t *testing.T) {
	run := func(t *testing.T) string {
		var out bytes.Buffer
		require.NoError(t, Run(context.Background(), Options{Yes: true, Docker: true, Out: &out}), out.String())
		return out.String()
	}
	t.Run("foreign container is left", func(t *testing.T) {
		setup(t)
		d := newFakeDocker(t)
		d.ollama(t, "nginx", "")
		out := run(t)
		assert.Contains(t, out, "container ragcode-ollama is not an Ollama of RagCode, left alone")
		assert.True(t, d.has("container"))
	})
	t.Run("orphan volume is removed by name", func(t *testing.T) {
		setup(t)
		d := newFakeDocker(t)
		d.set(t, "volume")
		run(t)
		assert.False(t, d.has("volume"))
	})
	t.Run("volume that cannot be removed is warned", func(t *testing.T) {
		setup(t)
		d := newFakeDocker(t)
		d.set(t, "volume")
		d.set(t, "volfail")
		assert.Contains(t, run(t), "volume ragcode-ollama not removed")
	})
	t.Run("container and volume", func(t *testing.T) {
		setup(t)
		d := newFakeDocker(t)
		d.ollama(t, "ollama/ollama", ollamaMounts)
		d.set(t, "volume")
		out := run(t)
		assert.NotContains(t, out, "not removed")
		assert.NotContains(t, out, "left alone")
		assert.False(t, d.has("container"))
		assert.False(t, d.has("volume"))
	})
}

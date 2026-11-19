package llama

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/doITmagic/rag-code-mcp/v2/index"
)

type tarEntry struct {
	name, body, link string // link set: a symlink to it
}

func makeTarGz(t *testing.T, entries ...tarEntry) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for _, e := range entries {
		h := &tar.Header{Name: e.name, Mode: 0o755, Size: int64(len(e.body)), Typeflag: tar.TypeReg}
		if e.link != "" {
			h = &tar.Header{Name: e.name, Typeflag: tar.TypeSymlink, Linkname: e.link}
		}
		if strings.HasSuffix(e.name, "/") {
			h = &tar.Header{Name: e.name, Typeflag: tar.TypeDir, Mode: 0o755}
		}
		require.NoError(t, tw.WriteHeader(h))
		if e.link == "" && h.Typeflag == tar.TypeReg {
			_, err := tw.Write([]byte(e.body))
			require.NoError(t, err)
		}
	}
	require.NoError(t, tw.Close())
	require.NoError(t, gz.Close())
	return buf.Bytes()
}

func makeZip(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	names := make([]string, 0, len(files))
	for n := range files {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		w, err := zw.Create(n)
		require.NoError(t, err)
		_, err = w.Write([]byte(files[n]))
		require.NoError(t, err)
	}
	require.NoError(t, zw.Close())
	return buf.Bytes()
}

func sum(b []byte) string { h := sha256.Sum256(b); return hex.EncodeToString(h[:]) }

// serve points the download sources at a local server holding files, swaps
// the CPU runtime of this platform for archive name (pinned to sha), and
// returns a counter of the requests made.
func serve(t *testing.T, name, sha string, files map[string][]byte) *atomic.Int32 {
	t.Helper()
	var reqs atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reqs.Add(1)
		b, ok := files[filepath.Base(r.URL.Path)]
		if !ok {
			http.NotFound(w, r)
			return
		}
		w.Write(b)
	}))
	key := runtime.GOOS + "/" + runtime.GOARCH
	oldBase, oldAsset, had := runtimeBase, runtimes[CPU][key], false
	_, had = runtimes[CPU][key]
	runtimeBase = srv.URL + "/"
	runtimes[CPU][key] = asset{name, sha}
	t.Cleanup(func() {
		srv.Close()
		runtimeBase = oldBase
		if had {
			runtimes[CPU][key] = oldAsset
		} else {
			delete(runtimes[CPU], key)
		}
	})
	return &reqs
}

// goodArchive is a release archive as llama.cpp publishes it. The library
// link is left out on Windows, where making one needs a privilege (and the
// real Windows archives hold none).
func goodArchive(t *testing.T) []byte {
	ents := []tarEntry{
		{name: "llama-b1/", body: ""},
		{name: "llama-b1/" + exe("llama-server"), body: "server"},
		{name: "llama-b1/libggml.so.1.0.0", body: "lib"},
		{name: "llama-b1/LICENSE", body: "mit"},
		{name: "llama-b1/llama-bench", body: "tool"},
	}
	if runtime.GOOS != "windows" {
		ents = append(ents, tarEntry{name: "llama-b1/libggml.so.1", link: "libggml.so.1.0.0"})
	}
	return makeTarGz(t, ents...)
}

func names(t *testing.T, dir string) []string {
	t.Helper()
	ents, err := os.ReadDir(dir)
	require.NoError(t, err)
	var out []string
	for _, e := range ents {
		out = append(out, e.Name())
	}
	return out
}

func TestFetchRuntimeUnpacksFlatAndOnlyWanted(t *testing.T) {
	arc := goodArchive(t)
	serve(t, "rt.tar.gz", sum(arc), map[string][]byte{"rt.tar.gz": arc})
	home := t.TempDir()
	o := Options{Home: home, Variant: CPU}
	dir := filepath.Dir(o.ServerPath())
	require.NoError(t, o.fetchRuntime(context.Background(), dir))

	if runtime.GOOS == "windows" { // no link in the archive, no mode bits on disk
		assert.Equal(t, []string{"LICENSE", "libggml.so.1.0.0", "llama-server.exe"}, names(t, dir))
	} else {
		assert.Equal(t, []string{"LICENSE", "libggml.so.1", "libggml.so.1.0.0", "llama-server"}, names(t, dir))
		target, err := os.Readlink(filepath.Join(dir, "libggml.so.1"))
		require.NoError(t, err)
		assert.Equal(t, "libggml.so.1.0.0", target)
		fi, err := os.Stat(o.ServerPath())
		require.NoError(t, err)
		assert.NotZero(t, fi.Mode()&0o100, "the server is executable")
	}
	// Nothing but the finished directory is left in its parent.
	assert.Equal(t, []string{filepath.Base(dir)}, names(t, filepath.Dir(dir)))
}

func TestFetchRuntimeWrongChecksum(t *testing.T) {
	arc := goodArchive(t)
	serve(t, "rt.tar.gz", sum([]byte("something else")), map[string][]byte{"rt.tar.gz": arc})
	o := Options{Home: t.TempDir(), Variant: CPU}
	dir := filepath.Dir(o.ServerPath())
	err := o.fetchRuntime(context.Background(), dir)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "pinned checksum")
	assert.NoDirExists(t, dir)
	assert.Empty(t, names(t, filepath.Dir(dir)), "no partial download")
}

func TestFetchRuntimeHTTPError(t *testing.T) {
	serve(t, "missing.tar.gz", "x", map[string][]byte{})
	o := Options{Home: t.TempDir(), Variant: CPU}
	err := o.fetchRuntime(context.Background(), filepath.Dir(o.ServerPath()))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "404")
}

func TestFetchRuntimeRefusesLinkOutside(t *testing.T) {
	for _, link := range []string{"../../etc/passwd", "/etc/passwd", "sub/x"} {
		arc := makeTarGz(t,
			tarEntry{name: "d/" + exe("llama-server"), body: "s"},
			tarEntry{name: "d/libevil.so.1", link: link})
		serve(t, "rt.tar.gz", sum(arc), map[string][]byte{"rt.tar.gz": arc})
		o := Options{Home: t.TempDir(), Variant: CPU}
		dir := filepath.Dir(o.ServerPath())
		err := o.fetchRuntime(context.Background(), dir)
		require.Error(t, err, link)
		assert.Contains(t, err.Error(), "points outside", link)
		assert.NoDirExists(t, dir)
		assert.Empty(t, names(t, filepath.Dir(dir)), "temporary directory removed")
	}
}

func TestFetchRuntimeWithoutServer(t *testing.T) {
	arc := makeTarGz(t, tarEntry{name: "d/libggml.so", body: "x"})
	serve(t, "rt.tar.gz", sum(arc), map[string][]byte{"rt.tar.gz": arc})
	o := Options{Home: t.TempDir(), Variant: CPU}
	dir := filepath.Dir(o.ServerPath())
	err := o.fetchRuntime(context.Background(), dir)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "holds no llama-server")
	assert.NoDirExists(t, dir)
}

func TestFetchRuntimeZip(t *testing.T) {
	arc := makeZip(t, map[string]string{
		"bin/" + exe("llama-server"): "server",
		"bin/ggml.dll":               "dll",
		"bin/llama-bench":            "tool",
		"bin/sub/":                   "",
	})
	serve(t, "rt.zip", sum(arc), map[string][]byte{"rt.zip": arc})
	o := Options{Home: t.TempDir(), Variant: CPU}
	dir := filepath.Dir(o.ServerPath())
	require.NoError(t, o.fetchRuntime(context.Background(), dir))
	assert.Equal(t, []string{"ggml.dll", exe("llama-server")}, names(t, dir))
}

func TestUntarUnzipDirect(t *testing.T) {
	d := t.TempDir()
	arc := filepath.Join(d, "a.tar.gz")
	require.NoError(t, os.WriteFile(arc, []byte("not gzip"), 0o600))
	assert.Error(t, untar(arc, t.TempDir()))
	assert.Error(t, untar(filepath.Join(d, "none"), t.TempDir()))
	assert.Error(t, unzip(arc, t.TempDir()))
}

func TestEnsureBothPresentDownloadsNothing(t *testing.T) {
	reqs := serve(t, "rt.tar.gz", "x", nil)
	d := t.TempDir()
	srv, model := filepath.Join(d, "llama-server"), filepath.Join(d, "m.gguf")
	require.NoError(t, os.WriteFile(srv, []byte("s"), 0o755))
	require.NoError(t, os.WriteFile(model, []byte("m"), 0o644))
	o := Options{Home: t.TempDir(), Server: srv, Model: model}
	assert.True(t, o.Installed())
	gs, gm, err := o.Ensure(context.Background())
	require.NoError(t, err)
	assert.Equal(t, srv, gs)
	assert.Equal(t, model, gm)
	assert.Zero(t, reqs.Load())
	assert.NoDirExists(t, filepath.Join(o.Home, "cache"), "no lock taken either")

	_, _, err = Options{Home: d, Server: srv, Model: filepath.Join(d, "none.gguf")}.Ensure(context.Background())
	require.ErrorContains(t, err, "embed_model")
	_, _, err = Options{Home: d, Server: filepath.Join(d, "none"), Model: model}.Ensure(context.Background())
	require.ErrorContains(t, err, "llama_server")
}

func TestEnsureFetchesOnlyTheRuntime(t *testing.T) {
	arc := goodArchive(t)
	reqs := serve(t, "rt.tar.gz", sum(arc), map[string][]byte{"rt.tar.gz": arc})
	home := t.TempDir()
	model := filepath.Join(home, "my.gguf")
	require.NoError(t, os.WriteFile(model, []byte("m"), 0o644))
	var said []string
	o := Options{Home: home, Variant: CPU, Model: model, Progress: func(s string) { said = append(said, s) }}
	gs, gm, err := o.Ensure(context.Background())
	require.NoError(t, err)
	assert.Equal(t, o.ServerPath(), gs)
	assert.Equal(t, model, gm)
	assert.FileExists(t, gs)
	assert.Equal(t, int32(1), reqs.Load())
	assert.NotEmpty(t, said)
	assert.True(t, o.Installed())

	// Now installed: a second call downloads nothing.
	_, _, err = o.Ensure(context.Background())
	require.NoError(t, err)
	assert.Equal(t, int32(1), reqs.Load())
}

func touch(t *testing.T, ps ...string) {
	t.Helper()
	for _, p := range ps {
		require.NoError(t, os.MkdirAll(filepath.Dir(p), 0o755))
		require.NoError(t, os.WriteFile(p, []byte("x"), 0o644))
	}
}

// What a killed run left in the directories RagCode owns is removed under
// the lock; what the user provides, and other files, stay.
func TestEnsureRemovesStaleDownloads(t *testing.T) {
	arc := goodArchive(t)
	reqs := serve(t, "rt.tar.gz", sum(arc), map[string][]byte{"rt.tar.gz": arc, modelFile: []byte("not the pinned model")})
	oldURL := modelURL
	modelURL = runtimeBase + modelFile
	t.Cleanup(func() { modelURL = oldURL })

	// The runtime is downloaded, the model is the user's: llama/ is cleaned, models/ is not.
	home := t.TempDir()
	model := filepath.Join(home, "models", "my.gguf")
	staleDir := filepath.Join(home, "llama", ".llama-cpu-12345") // a killed unpack
	touch(t, model, filepath.Join(home, "models", ".download-111"), filepath.Join(home, "llama", ".download-222"),
		filepath.Join(staleDir, "llama-server"), filepath.Join(home, "llama", "note.txt"), filepath.Join(home, "models", "other.gguf"))
	_, _, err := Options{Home: home, Variant: CPU, Model: model}.Ensure(context.Background())
	require.NoError(t, err)
	assert.NoFileExists(t, filepath.Join(home, "llama", ".download-222"))
	assert.NoDirExists(t, staleDir)
	assert.FileExists(t, filepath.Join(home, "llama", "note.txt"))
	assert.FileExists(t, filepath.Join(home, "models", ".download-111"), "the model directory is not ours when the model is the user's")
	assert.FileExists(t, filepath.Join(home, "models", "other.gguf"))
	assert.Equal(t, int32(1), reqs.Load())

	// The model is downloaded (and fails its pinned checksum here), the server is the user's.
	home = t.TempDir()
	server := filepath.Join(t.TempDir(), "llama-server")
	touch(t, server, filepath.Join(home, "models", ".download-333"), filepath.Join(home, "models", "keep.gguf"), filepath.Join(home, "llama", ".download-444"))
	_, _, err = Options{Home: home, Variant: CPU, Server: server}.Ensure(context.Background())
	require.ErrorContains(t, err, "pinned checksum")
	assert.NoFileExists(t, filepath.Join(home, "models", ".download-333"))
	assert.FileExists(t, filepath.Join(home, "models", "keep.gguf"))
	assert.FileExists(t, filepath.Join(home, "llama", ".download-444"), "llama/ is not ours when the server is the user's")
	assert.Equal(t, []string{"keep.gguf"}, names(t, filepath.Join(home, "models")), "the failed download left nothing")
}

func TestEnsureWaitsForTheLock(t *testing.T) {
	arc := goodArchive(t)
	reqs := serve(t, "rt.tar.gz", sum(arc), map[string][]byte{"rt.tar.gz": arc})
	home := t.TempDir()
	model := filepath.Join(home, "my.gguf")
	require.NoError(t, os.WriteFile(model, []byte("m"), 0o644))
	require.NoError(t, os.MkdirAll(filepath.Join(home, "cache"), 0o755))
	unlock, err := index.LockFile(filepath.Join(home, "cache", "llama.lock"))
	require.NoError(t, err)

	var mu sync.Mutex
	var said []string
	o := Options{Home: home, Variant: CPU, Model: model, Progress: func(s string) { mu.Lock(); said = append(said, s); mu.Unlock() }}
	done := make(chan error, 1)
	go func() { _, _, err := o.Ensure(context.Background()); done <- err }()
	select {
	case err := <-done:
		t.Fatalf("Ensure did not wait for the lock: %v", err)
	case <-time.After(1200 * time.Millisecond):
	}
	assert.Zero(t, reqs.Load())
	mu.Lock()
	assert.Contains(t, said[0], "waiting")
	mu.Unlock()

	// A waiter whose context ends gives up.
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	_, _, err = o.Ensure(ctx)
	require.ErrorIs(t, err, context.DeadlineExceeded)

	unlock()
	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(5 * time.Second):
		t.Fatal("Ensure did not continue after the lock was released")
	}
	assert.Equal(t, int32(1), reqs.Load())
	assert.FileExists(t, o.ServerPath())
}

func TestOptionsNames(t *testing.T) {
	home := t.TempDir()
	cpu := Options{Home: home, Variant: CPU}
	assert.Equal(t, CPU, cpu.variant())
	assert.Equal(t, filepath.Join(home, "llama", Tag+"-cpu", exe("llama-server")), cpu.ServerPath())
	assert.Equal(t, "llama.cpp "+Tag+" (cpu)", cpu.RuntimeName())
	assert.Equal(t, filepath.Join(home, "models", modelFile), cpu.ModelPath())
	assert.Equal(t, ModelName, cpu.ModelLabel())

	if _, ok := runtimes[Vulkan][runtime.GOOS+"/"+runtime.GOARCH]; ok {
		v := Options{Home: home, Variant: Vulkan}
		assert.Equal(t, Vulkan, v.variant())
		assert.Equal(t, filepath.Join(home, "llama", Tag+"-vulkan", exe("llama-server")), v.ServerPath())
		assert.Contains(t, v.RuntimeName(), "(vulkan)")
	} else {
		assert.Equal(t, CPU, Options{Variant: Vulkan}.variant(), "no Vulkan build here: CPU")
	}
	assert.Contains(t, []string{CPU, Vulkan}, Options{Home: home}.variant())
	assert.Contains(t, []string{CPU, Vulkan}, Options{Home: home, Variant: Auto}.variant())

	custom := Options{Home: home, Server: "/opt/ll/llama-server", Model: "/opt/m/my-embed.gguf"}
	assert.Equal(t, "/opt/ll/llama-server", custom.ServerPath())
	assert.Equal(t, "/opt/ll/llama-server", custom.RuntimeName())
	assert.Equal(t, "/opt/m/my-embed.gguf", custom.ModelPath())
	assert.Equal(t, "my-embed", custom.ModelLabel(), "a file that cannot be read has only its name")

	// Another file under the same name is another model; the same bytes are
	// the same model wherever they lie.
	big := func(dir string, last byte) string {
		p := filepath.Join(dir, "my-embed.gguf")
		data := make([]byte, 3<<20)
		data[len(data)-1] = last
		require.NoError(t, os.WriteFile(p, data, 0o600))
		return p
	}
	a, b, c := big(t.TempDir(), 1), big(t.TempDir(), 2), big(t.TempDir(), 1)
	la, lb, lc := Options{Model: a}.ModelLabel(), Options{Model: b}.ModelLabel(), Options{Model: c}.ModelLabel()
	assert.Regexp(t, `^my-embed@[0-9a-f]{12}$`, la)
	assert.NotEqual(t, la, lb, "the last MiB differs")
	assert.Equal(t, la, lc)
	small := filepath.Join(t.TempDir(), "my-embed.gguf")
	require.NoError(t, os.WriteFile(small, []byte("tiny"), 0o600))
	assert.NotEqual(t, la, Options{Model: small}.ModelLabel())
	assert.Regexp(t, `^my-embed@[0-9a-f]{12}$`, Options{Model: small}.ModelLabel())

	assert.Equal(t, []string{cpu.ServerPath(), Options{Home: home, Variant: Vulkan}.ServerPath()}, ServerPaths(home))
}

func exe(n string) string {
	if runtime.GOOS == "windows" {
		return n + ".exe"
	}
	return n
}

// slowServer serves handler for download; the sources are set by the caller.
func slowServer(t *testing.T, handler http.HandlerFunc) string {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	return srv.URL + "/file"
}

// stallRule lowers the stall rule of download for one test.
func stallRule(t *testing.T, window time.Duration, minBytes int64) {
	t.Helper()
	oldW, oldB := stallWindow, stallMinBytes
	stallWindow, stallMinBytes = window, minBytes
	t.Cleanup(func() { stallWindow, stallMinBytes = oldW, oldB })
}

// A server that sends its headers and a few bytes and then says nothing: the
// download ends as stalled and leaves no temporary file.
func TestDownloadStalledAfterHeaders(t *testing.T) {
	stallRule(t, 300*time.Millisecond, 1<<10)
	url := slowServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("abc"))
		w.(http.Flusher).Flush()
		<-r.Context().Done()
	})
	dir := t.TempDir()
	start := time.Now()
	_, err := Options{}.download(context.Background(), url, sum([]byte("x")), 0, dir, "thing")
	require.ErrorIs(t, err, ErrStalled)
	assert.Contains(t, err.Error(), "thing")
	assert.Less(t, time.Since(start), 5*time.Second)
	assert.Empty(t, names(t, dir), "no temporary file")
}

// The wait for the response headers counts too: the timer starts before the request.
func TestDownloadStalledBeforeHeaders(t *testing.T) {
	stallRule(t, 300*time.Millisecond, 1<<10)
	url := slowServer(t, func(w http.ResponseWriter, r *http.Request) { <-r.Context().Done() })
	dir := t.TempDir()
	_, err := Options{}.download(context.Background(), url, sum([]byte("x")), 0, dir, "thing")
	require.ErrorIs(t, err, ErrStalled)
	assert.Empty(t, names(t, dir))
}

// Data that keeps coming but under the minimum is a stall as well: a trickle
// must not hold the embedder for hours.
func TestDownloadTrickleBelowTheMinimumStalls(t *testing.T) {
	stallRule(t, 300*time.Millisecond, 1<<10)
	url := slowServer(t, func(w http.ResponseWriter, r *http.Request) {
		for {
			w.Write([]byte("x"))
			w.(http.Flusher).Flush()
			select {
			case <-r.Context().Done():
				return
			case <-time.After(30 * time.Millisecond):
			}
		}
	})
	dir := t.TempDir()
	_, err := Options{}.download(context.Background(), url, sum([]byte("x")), 0, dir, "thing")
	require.ErrorIs(t, err, ErrStalled)
	assert.Empty(t, names(t, dir))
}

// A slow line that delivers the minimum in every window is not cut, though
// the whole download takes several windows.
func TestDownloadSlowButAboveTheMinimumFinishes(t *testing.T) {
	stallRule(t, 500*time.Millisecond, 100)
	payload := bytes.Repeat([]byte("0123456789"), 120) // 1200 bytes: 12 chunks of 100, 1.2 s
	url := slowServer(t, func(w http.ResponseWriter, r *http.Request) {
		for i := 0; i < len(payload); i += 100 {
			w.Write(payload[i : i+100])
			w.(http.Flusher).Flush()
			time.Sleep(100 * time.Millisecond)
		}
	})
	dir := t.TempDir()
	tmp, err := Options{}.download(context.Background(), url, sum(payload), 0, dir, "thing")
	require.NoError(t, err)
	got, err := os.ReadFile(tmp)
	require.NoError(t, err)
	assert.Equal(t, payload, got)
}

// A window that meets a write is not judged, however long the write lasts: a
// slow disk is not a stall. Once the write is over, a window with too little
// read ends the download.
func TestWatchStallDoesNotJudgeAWindowWithAWrite(t *testing.T) {
	ctx, cancel := context.WithCancelCause(context.Background())
	defer cancel(nil)
	var got atomic.Int64
	var busy atomic.Bool
	busy.Store(true) // a write that outlasts many windows, with nothing read meanwhile
	done := make(chan struct{})
	go func() {
		watchStall(ctx, cancel, 20*time.Millisecond, 1<<10, &got, &busy)
		close(done)
	}()
	select {
	case <-done:
		t.Fatal("a window with a write was judged as a stall")
	case <-time.After(300 * time.Millisecond):
	}
	assert.NoError(t, context.Cause(ctx))

	busy.Store(false)
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("the stall after the write was not seen")
	}
	assert.ErrorIs(t, context.Cause(ctx), ErrStalled)
}

// The caller giving up is its own error, not a stall.
func TestDownloadCallerCancelIsNotAStall(t *testing.T) {
	url := slowServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("abc"))
		w.(http.Flusher).Flush()
		<-r.Context().Done()
	})
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	dir := t.TempDir()
	_, err := Options{}.download(ctx, url, sum([]byte("x")), 0, dir, "thing")
	require.ErrorIs(t, err, context.DeadlineExceeded)
	assert.NotErrorIs(t, err, ErrStalled)
	assert.Empty(t, names(t, dir))
}

// Ensure reports a stalled download with the error of the download.
func TestEnsureStalledDownloadIsReported(t *testing.T) {
	stallRule(t, 300*time.Millisecond, 1<<10)
	serve(t, "rt.tar.gz", "x", nil)
	runtimeBase = slowServer(t, func(w http.ResponseWriter, r *http.Request) { <-r.Context().Done() })
	home := t.TempDir()
	model := filepath.Join(home, "my.gguf")
	require.NoError(t, os.WriteFile(model, []byte("m"), 0o644))
	_, _, err := Options{Home: home, Variant: CPU, Model: model}.Ensure(context.Background())
	require.ErrorIs(t, err, ErrStalled)
	assert.NoDirExists(t, filepath.Dir(Options{Home: home, Variant: CPU}.ServerPath()))
}

// The label is asked again every time and follows the file on disk, and the
// built-in model's label never reads a file.
func TestModelLabelFollowsFile(t *testing.T) {
	p := filepath.Join(t.TempDir(), "my-embed.gguf")
	write := func(last byte) {
		data := make([]byte, 3<<20)
		data[len(data)-1] = last
		require.NoError(t, os.WriteFile(p, data, 0o600))
	}
	o := Options{Model: p}
	write(1)
	l1 := o.ModelLabel()
	write(2)
	l2 := o.ModelLabel()
	assert.NotEqual(t, l1, l2, "the same Options value follows the file")
	write(1)
	assert.Equal(t, l1, o.ModelLabel(), "the old file back is the old label")

	assert.Equal(t, ModelName, Options{Home: t.TempDir()}.ModelLabel(), "no file read for the built-in model")
}

package embed

import (
	"context"
	"encoding/json"
	"fmt"
	"hash/fnv"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	fakeDim      = 6
	fakeMaxBytes = 5000 // the fake server refuses a longer input as "too large"
)

// TestMain lets the test binary act as the llama-server: the tests start it
// again with RAGCODE_FAKE_LLAMA=1 and the arguments Llama passes.
func TestMain(m *testing.M) {
	if os.Getenv("RAGCODE_FAKE_LLAMA") == "1" {
		fakeLlamaServer()
		return
	}
	// Only children started from here on inherit it; this process is past the check.
	os.Setenv("RAGCODE_FAKE_LLAMA", "1")
	os.Exit(m.Run())
}

// fakeVec is the vector the fake server returns for a text.
func fakeVec(text string) []float32 {
	h := fnv.New64a()
	h.Write([]byte(text))
	s := h.Sum64()
	v := make([]float32, fakeDim)
	for i := range v {
		v[i] = float32((s>>(uint(i)*8))&0xff) + float32(i)
	}
	return v
}

func fakeLog(line string) {
	p := os.Getenv("RAGCODE_FAKE_LOG")
	if p == "" {
		return
	}
	if f, err := os.OpenFile(p, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600); err == nil {
		fmt.Fprintln(f, line)
		f.Close()
	}
}

func fakeLlamaServer() {
	port, model := "", ""
	args := os.Args[1:]
	for i := 0; i+1 < len(args); i++ {
		switch args[i] {
		case "--port":
			port = args[i+1]
		case "-m":
			model = args[i+1]
		}
	}
	if os.Getenv("RAGCODE_FAKE_MODE") == "exit" {
		fmt.Fprintln(os.Stderr, "error: failed to load model "+model)
		os.Exit(1)
	}
	key := os.Getenv("LLAMA_API_KEY")
	fakeLog("start " + strconv.Itoa(os.Getpid()))
	auth := func(w http.ResponseWriter, r *http.Request) bool {
		if r.Header.Get("Authorization") != "Bearer "+key {
			http.Error(w, `{"error":{"message":"Invalid API Key"}}`, http.StatusUnauthorized)
			return false
		}
		return true
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		if os.Getenv("RAGCODE_FAKE_MODE") == "loading" { // a model that never finishes loading
			http.Error(w, `{"error":{"message":"Loading model"}}`, http.StatusServiceUnavailable)
			return
		}
		w.Write([]byte(`{"status":"ok"}`))
	})
	mux.HandleFunc("/props", func(w http.ResponseWriter, r *http.Request) {
		if auth(w, r) {
			w.Write([]byte(`{}`))
		}
	})
	mux.HandleFunc("/v1/embeddings", func(w http.ResponseWriter, r *http.Request) {
		if !auth(w, r) {
			return
		}
		var req struct {
			Input []string `json:"input"`
		}
		if json.NewDecoder(r.Body).Decode(&req) != nil {
			http.Error(w, "bad", http.StatusBadRequest)
			return
		}
		total := 0
		for _, t := range req.Input {
			total += len(t)
		}
		fakeLog(fmt.Sprintf("emb n=%d bytes=%d", len(req.Input), total))
		for _, t := range req.Input {
			if len(t) > fakeMaxBytes {
				w.WriteHeader(http.StatusInternalServerError)
				fmt.Fprintf(w, `{"error":{"message":"input (%d tokens) is too large to process"}}`, len(t))
				return
			}
		}
		type item struct {
			Index     int       `json:"index"`
			Embedding []float32 `json:"embedding"`
		}
		data := make([]item, len(req.Input))
		// Reverse order: the client must place vectors by index.
		for i, t := range req.Input {
			data[len(data)-1-i] = item{i, fakeVec(t)}
		}
		json.NewEncoder(w).Encode(map[string]any{"data": data})
	})
	go func() {
		c := make(chan os.Signal, 1)
		signal.Notify(c, os.Interrupt, syscall.SIGTERM)
		<-c
		os.Exit(0)
	}()
	if err := http.ListenAndServe("127.0.0.1:"+port, mux); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
}

type testRT struct {
	server, model string
	installed     atomic.Bool
	ensures       atomic.Int32
	name          string        // RuntimeName; empty means "test"
	block         chan struct{} // when set, Ensure waits for it to close
	fail          atomic.Bool   // Ensure fails, as a download that broke
}

func (r *testRT) Ensure(context.Context) (string, string, error) {
	r.ensures.Add(1)
	if r.block != nil {
		<-r.block
	}
	if r.fail.Load() {
		return "", "", fmt.Errorf("download broke")
	}
	r.installed.Store(true)
	return r.server, r.model, nil
}
func (r *testRT) Installed() bool { return r.installed.Load() }
func (r *testRT) RuntimeName() string {
	if r.name != "" {
		return r.name
	}
	return "test"
}

// newTestLlama returns an embedder over the fake server, and the path of the
// log the fake server writes.
func newTestLlama(t *testing.T) (*Llama, *testRT, string) {
	t.Helper()
	dir := t.TempDir()
	model := filepath.Join(dir, "m.gguf")
	require.NoError(t, os.WriteFile(model, []byte("x"), 0o600))
	log := filepath.Join(dir, "fake.log")
	t.Setenv("RAGCODE_FAKE_LOG", log)
	t.Setenv("RAGCODE_FAKE_MODE", "")
	rt := &testRT{server: os.Args[0], model: model}
	rt.installed.Store(true)
	l := NewLlama("test-model", rt, filepath.Join(dir, "run"))
	t.Cleanup(func() { l.Close() })
	return l, rt, log
}

func logLines(t *testing.T, path, prefix string) []string {
	t.Helper()
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil
	}
	require.NoError(t, err)
	var out []string
	for _, ln := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		if strings.HasPrefix(ln, prefix) {
			out = append(out, ln)
		}
	}
	return out
}

func serverPid(t *testing.T, l *Llama) int {
	t.Helper()
	l.sem <- struct{}{}
	defer func() { <-l.sem }()
	require.NotNil(t, l.srv)
	return l.srv.cmd.Process.Pid
}

func waitDead(t *testing.T, pid int) {
	t.Helper()
	require.Eventually(t, func() bool { return !pidAlive(pid) }, 5*time.Second, 20*time.Millisecond)
}

func TestLlamaEmbedOrderAndBatches(t *testing.T) {
	l, _, log := newTestLlama(t)
	ctx := context.Background()
	texts := make([]string, 40)
	for i := range texts {
		texts[i] = fmt.Sprintf("text %d", i)
	}
	vecs, err := l.Embed(ctx, texts)
	require.NoError(t, err)
	require.Len(t, vecs, 40)
	for i, s := range texts {
		assert.Equal(t, fakeVec(s), vecs[i], "text %d", i)
	}
	assert.Equal(t, []string{"emb n=16 bytes=" + strconv.Itoa(sumLen(texts[:16])), "emb n=16 bytes=" + strconv.Itoa(sumLen(texts[16:32])), "emb n=8 bytes=" + strconv.Itoa(sumLen(texts[32:]))},
		logLines(t, log, "emb"), "at most 16 texts per request")

	// 8 KiB per request: 3000-byte texts go two by two.
	big := make([]string, 5)
	for i := range big {
		big[i] = strings.Repeat(string(rune('a'+i)), 3000)
	}
	require.NoError(t, os.Remove(log))
	vecs, err = l.Embed(ctx, big)
	require.NoError(t, err)
	for i, s := range big {
		assert.Equal(t, fakeVec(s), vecs[i])
	}
	assert.Equal(t, []string{"emb n=2 bytes=6000", "emb n=2 bytes=6000", "emb n=1 bytes=3000"}, logLines(t, log, "emb"))

	vecs, err = l.Embed(ctx, nil)
	require.NoError(t, err)
	assert.Empty(t, vecs)
}

func sumLen(s []string) int {
	n := 0
	for _, x := range s {
		n += len(x)
	}
	return n
}

func TestLlamaDimensionCached(t *testing.T) {
	l, _, log := newTestLlama(t)
	for range 3 {
		d, err := l.Dimension(context.Background())
		require.NoError(t, err)
		assert.Equal(t, fakeDim, d)
	}
	assert.Len(t, logLines(t, log, "emb"), 1, "one probe")
	assert.Equal(t, "test-model", l.Model())
}

func TestLlamaStatus(t *testing.T) {
	l, rt, _ := newTestLlama(t)
	rt.installed.Store(false)
	assert.Equal(t, Status{Model: "test-model", Runtime: "test", State: "not_installed"}, l.Status())
	// The first call fetches what is missing, then starts the server.
	_, err := l.Embed(context.Background(), []string{"a"})
	require.NoError(t, err)
	assert.Equal(t, Status{Model: "test-model", Runtime: "test", State: "running"}, l.Status())
	require.NoError(t, l.Close())
	assert.Equal(t, "stopped", l.Status().State)
}

func TestLlamaStartedOnceForConcurrentCallers(t *testing.T) {
	l, rt, log := newTestLlama(t)
	var wg sync.WaitGroup
	for i := range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			vecs, err := l.Embed(context.Background(), []string{fmt.Sprint("c", i)})
			if assert.NoError(t, err) {
				assert.Equal(t, fakeVec(fmt.Sprint("c", i)), vecs[0])
			}
		}()
	}
	wg.Wait()
	assert.Len(t, logLines(t, log, "start"), 1)
	assert.Equal(t, int32(1), rt.ensures.Load())
}

func TestLlamaIdleStop(t *testing.T) {
	l, _, log := newTestLlama(t)
	l.IdleStop = 300 * time.Millisecond
	_, err := l.Embed(context.Background(), []string{"a"})
	require.NoError(t, err)
	first := serverPid(t, l)
	waitDead(t, first)
	assert.Equal(t, "stopped", l.Status().State)
	_, err = l.Embed(context.Background(), []string{"b"})
	require.NoError(t, err)
	assert.NotEqual(t, first, serverPid(t, l))
	assert.Len(t, logLines(t, log, "start"), 2)
}

func TestLlamaCloseStopsAndRecord(t *testing.T) {
	l, _, _ := newTestLlama(t)
	_, err := l.Embed(context.Background(), []string{"a"})
	require.NoError(t, err)
	pid := serverPid(t, l)
	rec := filepath.Join(l.runDir, "llama-"+strconv.Itoa(os.Getpid())+".json")
	data, err := os.ReadFile(rec)
	require.NoError(t, err, "record while running")
	var r record
	require.NoError(t, json.Unmarshal(data, &r))
	assert.Equal(t, os.Getpid(), r.Owner)
	assert.Equal(t, pid, r.Server)
	assert.NotEmpty(t, r.Key)

	require.NoError(t, l.Close())
	waitDead(t, pid)
	_, err = os.Stat(rec)
	assert.True(t, os.IsNotExist(err), "record removed")
	_, err = l.Embed(context.Background(), []string{"a"})
	require.ErrorIs(t, err, ErrUnavailable)
	_, err = l.Dimension(context.Background())
	require.ErrorIs(t, err, ErrUnavailable)
}

func TestLlamaRestartAfterKill(t *testing.T) {
	l, _, log := newTestLlama(t)
	_, err := l.Embed(context.Background(), []string{"a"})
	require.NoError(t, err)
	pid := serverPid(t, l)
	p, err := os.FindProcess(pid)
	require.NoError(t, err)
	require.NoError(t, p.Kill())
	waitDead(t, pid)
	vecs, err := l.Embed(context.Background(), []string{"b"})
	require.NoError(t, err)
	assert.Equal(t, fakeVec("b"), vecs[0])
	assert.NotEqual(t, pid, serverPid(t, l))
	assert.Len(t, logLines(t, log, "start"), 2)
}

func TestLlamaOversizedInputCut(t *testing.T) {
	l, _, _ := newTestLlama(t)
	long := strings.Repeat("x", 9000)
	vecs, err := l.Embed(context.Background(), []string{"short", long, "tail"})
	require.NoError(t, err)
	require.Len(t, vecs, 3)
	assert.Equal(t, fakeVec("short"), vecs[0])
	assert.Equal(t, fakeVec("tail"), vecs[2])
	// 9000 tokens counted: cut in proportion to fit the 4096-token context.
	assert.Equal(t, fakeVec(long[:len(long)*(ctxTokens-64)/9000]), vecs[1])
}

func TestLlamaServerExitsAtStart(t *testing.T) {
	l, _, _ := newTestLlama(t)
	t.Setenv("RAGCODE_FAKE_MODE", "exit")
	_, err := l.Embed(context.Background(), []string{"a"})
	require.ErrorIs(t, err, ErrUnavailable)
	assert.Contains(t, err.Error(), "failed to load model")
	st := l.Status()
	assert.Contains(t, st.Error, "failed to load model")
	// The next call starts a new server.
	t.Setenv("RAGCODE_FAKE_MODE", "")
	_, err = l.Embed(context.Background(), []string{"a"})
	require.NoError(t, err)
	assert.Empty(t, l.Status().Error)
}

func TestLlamaLowMemory(t *testing.T) {
	l, _, log := newTestLlama(t)
	free := 500
	old := freeMB
	freeMB = func() (int, bool) { return free, true }
	t.Cleanup(func() { freeMB = old })
	l.MinFreeMB = 1536
	_, err := l.Embed(context.Background(), []string{"a"})
	require.ErrorIs(t, err, ErrLowMemory)
	assert.Contains(t, err.Error(), "500 MB free, at least 1536 MB needed")
	_, err = l.Dimension(context.Background())
	require.ErrorIs(t, err, ErrLowMemory)
	assert.Empty(t, logLines(t, log, "start"), "no server started")

	free = 4000
	_, err = l.Embed(context.Background(), []string{"a"})
	require.NoError(t, err)
	free = 500 // the model is loaded now: no check while the server runs
	_, err = l.Embed(context.Background(), []string{"a"})
	require.NoError(t, err)

	l.MinFreeMB = 0
	require.NoError(t, l.Close())
}

// startFake runs the fake server by hand, as a server left behind by a dead
// RagCode process; the returned channel closes when it exits.
func startFake(t *testing.T, port int, key string) (*exec.Cmd, <-chan struct{}) {
	t.Helper()
	cmd := exec.Command(os.Args[0], "--port", strconv.Itoa(port))
	cmd.Env = append(os.Environ(), "RAGCODE_FAKE_LLAMA=1", "RAGCODE_FAKE_LOG=", "LLAMA_API_KEY="+key)
	require.NoError(t, cmd.Start())
	done := make(chan struct{})
	go func() { _ = cmd.Wait(); close(done) }()
	t.Cleanup(func() { _ = cmd.Process.Kill(); <-done })
	c := &http.Client{Timeout: time.Second}
	require.Eventually(t, func() bool {
		res, err := c.Get("http://127.0.0.1:" + strconv.Itoa(port) + "/health")
		if err != nil {
			return false
		}
		res.Body.Close()
		return res.StatusCode == 200
	}, 10*time.Second, 50*time.Millisecond)
	return cmd, done
}

func writeRecord(t *testing.T, dir string, owner, server, port int, key string) string {
	t.Helper()
	p := filepath.Join(dir, "llama-"+strconv.Itoa(owner)+".json")
	data, _ := json.Marshal(record{Owner: owner, Server: server, Port: port, Key: key})
	require.NoError(t, os.WriteFile(p, data, 0o600))
	return p
}

func TestSweepDeadOwner(t *testing.T) {
	dir := t.TempDir()
	port, err := freePort()
	require.NoError(t, err)
	cmd, done := startFake(t, port, "k1")

	// A pid that is gone: a process that ran and was waited for.
	dead := exec.Command(os.Args[0], "-test.run=^$")
	dead.Env = append(os.Environ(), "RAGCODE_FAKE_LLAMA=", "RAGCODE_FAKE_MODE=")
	require.NoError(t, dead.Run())
	rec := writeRecord(t, dir, dead.Process.Pid, cmd.Process.Pid, port, "k1")

	sweep(dir, &http.Client{})
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("the server of a dead owner is still running")
	}
	_, err = os.Stat(rec)
	assert.True(t, os.IsNotExist(err), "record removed")
}

func TestSweepLiveOwnerAndWrongKey(t *testing.T) {
	dir := t.TempDir()
	port, err := freePort()
	require.NoError(t, err)
	cmd, done := startFake(t, port, "k2")

	// The owner is alive (the parent of this test): hands off.
	rec := writeRecord(t, dir, os.Getppid(), cmd.Process.Pid, port, "k2")
	sweep(dir, &http.Client{})
	select {
	case <-done:
		t.Fatal("the server of a live owner was killed")
	case <-time.After(300 * time.Millisecond):
	}
	_, err = os.Stat(rec)
	assert.NoError(t, err, "record kept")
	os.Remove(rec)

	// Dead owner but a port that does not answer to the key: not our server.
	dead := exec.Command(os.Args[0], "-test.run=^$")
	dead.Env = append(os.Environ(), "RAGCODE_FAKE_LLAMA=", "RAGCODE_FAKE_MODE=")
	require.NoError(t, dead.Run())
	rec = writeRecord(t, dir, dead.Process.Pid, cmd.Process.Pid, port, "other")
	sweep(dir, &http.Client{})
	select {
	case <-done:
		t.Fatal("a server that did not answer to the record's key was killed")
	case <-time.After(300 * time.Millisecond):
	}
	_, err = os.Stat(rec)
	assert.True(t, os.IsNotExist(err), "stale record removed")

	// A record that is not JSON is removed.
	junk := filepath.Join(dir, "llama-1.json")
	require.NoError(t, os.WriteFile(junk, []byte("{"), 0o600))
	sweep(dir, &http.Client{})
	_, err = os.Stat(junk)
	assert.True(t, os.IsNotExist(err))
}

// Status answers at once while a call holds the start lock fetching files.
func TestLlamaStatusStartingWhileEnsureBlocks(t *testing.T) {
	l, rt, _ := newTestLlama(t)
	rt.installed.Store(false)
	rt.block = make(chan struct{})
	errc := make(chan error, 1)
	go func() {
		_, err := l.Embed(context.Background(), []string{"a"})
		errc <- err
	}()
	require.Eventually(t, func() bool { return rt.ensures.Load() == 1 }, 5*time.Second, 10*time.Millisecond)
	got := make(chan Status, 1)
	go func() { got <- l.Status() }()
	select {
	case st := <-got:
		assert.Equal(t, "starting", st.State)
	case <-time.After(2 * time.Second):
		t.Fatal("Status waits for the start")
	}
	close(rt.block)
	require.NoError(t, <-errc)
	assert.Equal(t, "running", l.Status().State)
}

// A download goes on after the call that began it gave up. When it ends with
// nobody waiting, the state stops saying "starting", a failure is reported,
// and the next call asks for the files again.
func TestLlamaStartingEndsWithAnUnwatchedDownload(t *testing.T) {
	l, rt, _ := newTestLlama(t)
	rt.installed.Store(false)
	giveUp := func() {
		t.Helper()
		n := rt.ensures.Load()
		ctx, cancel := context.WithCancel(context.Background())
		errc := make(chan error, 1)
		go func() {
			_, err := l.Embed(ctx, []string{"a"})
			errc <- err
		}()
		require.Eventually(t, func() bool { return rt.ensures.Load() == n+1 }, 5*time.Second, 10*time.Millisecond)
		cancel()
		require.ErrorIs(t, <-errc, context.Canceled)
		assert.Equal(t, "starting", l.Status().State, "the download goes on")
	}

	rt.block = make(chan struct{})
	rt.fail.Store(true)
	giveUp()
	close(rt.block)
	require.Eventually(t, func() bool { return l.Status().State == "not_installed" }, 5*time.Second, 10*time.Millisecond)
	assert.Contains(t, l.Status().Error, "download broke")

	rt.block = make(chan struct{})
	rt.fail.Store(false)
	giveUp()
	close(rt.block)
	require.Eventually(t, func() bool { return l.Status().State == "stopped" }, 5*time.Second, 10*time.Millisecond)
	assert.Empty(t, l.Status().Error, "the download that worked ends the failure of the one before")

	rt.block = nil
	_, err := l.Embed(context.Background(), []string{"a"})
	require.NoError(t, err)
	assert.Equal(t, Status{Model: "test-model", Runtime: "test", State: "running"}, l.Status())
	assert.Equal(t, int32(3), rt.ensures.Load(), "no result of an earlier download is kept for a later call")
}

// Close does not wait for a model that is still loading: the call that waits
// for it holds the lock, and ends with the embedder.
func TestLlamaCloseEndsACallWaitingForTheModel(t *testing.T) {
	l, _, log := newTestLlama(t)
	t.Setenv("RAGCODE_FAKE_MODE", "loading")
	errc := make(chan error, 1)
	go func() {
		_, err := l.Embed(context.Background(), []string{"a"})
		errc <- err
	}()
	require.Eventually(t, func() bool { return len(logLines(t, log, "start")) == 1 }, 5*time.Second, 10*time.Millisecond)
	assert.Equal(t, "starting", l.Status().State)
	// The waiting call holds the lock serverPid takes: the pid is in the record.
	// It is written after the process starts, so it may come after the log line.
	var rec record
	require.Eventually(t, func() bool {
		data, err := os.ReadFile(filepath.Join(l.runDir, "llama-"+strconv.Itoa(os.Getpid())+".json"))
		return err == nil && json.Unmarshal(data, &rec) == nil && rec.Server != 0
	}, 5*time.Second, 10*time.Millisecond)
	pid := rec.Server
	closed := make(chan struct{})
	go func() { l.Close(); close(closed) }()
	select {
	case <-closed:
	case <-time.After(10 * time.Second):
		t.Fatal("Close waits for the model load")
	}
	require.ErrorIs(t, <-errc, ErrUnavailable)
	waitDead(t, pid)
}

func TestLlamaVulkanExitHint(t *testing.T) {
	l, rt, _ := newTestLlama(t)
	rt.name = "llama.cpp b1 (vulkan)"
	t.Setenv("RAGCODE_FAKE_MODE", "exit")
	_, err := l.Embed(context.Background(), []string{"a"})
	require.ErrorIs(t, err, ErrUnavailable)
	assert.Contains(t, err.Error(), "failed to load model")
	assert.Contains(t, err.Error(), "embed_runtime: cpu")
	assert.Contains(t, l.Status().Error, "embed_runtime: cpu")

	rt.name = "llama.cpp b1 (cpu)"
	_, err = l.Embed(context.Background(), []string{"a"})
	require.Error(t, err)
	assert.NotContains(t, err.Error(), "embed_runtime")
}

// One idle timer serves every release, and the last use pushes it back.
func TestLlamaIdleTimerReused(t *testing.T) {
	l, _, _ := newTestLlama(t)
	l.IdleStop = 2 * time.Second
	_, err := l.Embed(context.Background(), []string{"a"})
	require.NoError(t, err)
	l.sem <- struct{}{}
	first := l.timer
	<-l.sem
	require.NotNil(t, first)
	time.Sleep(1200 * time.Millisecond)
	_, err = l.Embed(context.Background(), []string{"b"})
	require.NoError(t, err)
	l.sem <- struct{}{}
	assert.Same(t, first, l.timer)
	<-l.sem
	pid := serverPid(t, l)
	// 1.2 s + 1.2 s is past the first deadline: the reset kept the server up.
	time.Sleep(1200 * time.Millisecond)
	assert.True(t, pidAlive(pid), "the first deadline no longer applies")
	waitDead(t, pid)
}

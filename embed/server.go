package embed

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"
)

// orphanSleep makes a server whose RagCode process was killed drop the model
// from memory by itself; the next RagCode start removes the process (sweep).
const orphanSleep = 10 * time.Minute

// server is one running llama-server.
type server struct {
	cmd   *exec.Cmd
	base  string        // http://127.0.0.1:<port>
	key   string        // API key; only this process and the server's environment hold it
	done  chan struct{} // closed once the process has exited
	tail  *tailBuf      // the end of its output, for error messages
	ready bool          // /health has answered 200
	rec   string        // path of the record sweep reads
}

func (s *server) exited() bool {
	select {
	case <-s.done:
		return true
	default:
		return false
	}
}

// record is what a running server leaves on disk, so that another RagCode
// process can remove it if its owner was killed.
type record struct {
	Owner  int    `json:"owner"`  // pid of the RagCode process
	Server int    `json:"server"` // pid of llama-server
	Port   int    `json:"port"`
	Key    string `json:"key"`
}

func (l *Llama) lock(ctx context.Context) error {
	select {
	case l.sem <- struct{}{}:
		return nil
	case <-ctx.Done():
		return fmt.Errorf("%w: %w", ErrUnavailable, ctx.Err())
	}
}

// setState records what Status reports; a non-empty err replaces the last
// start failure, and reaching "running" clears it.
func (l *Llama) setState(state, err string) {
	l.stMu.Lock()
	defer l.stMu.Unlock()
	l.state = state
	if err != "" || state == "running" {
		l.lastErr = err
	}
}

// acquire returns the running server, starting it when needed, and counts
// the caller as a user until release.
func (l *Llama) acquire(ctx context.Context) (*server, error) {
	// Close ends the wait too: a model load has no clock, and Close needs the
	// lock this call holds while it waits.
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	defer context.AfterFunc(l.bg, cancel)()
	if err := l.lock(ctx); err != nil {
		return nil, err
	}
	defer func() { <-l.sem }()
	if l.closed {
		return nil, fmt.Errorf("%w: the embedder is closed", ErrUnavailable)
	}
	if l.srv != nil && l.srv.exited() {
		l.drop()
	}
	if l.srv == nil {
		l.setState("starting", "")
		if err := l.start(ctx); err != nil {
			if l.fetch != nil { // the caller gave up; the download goes on
				return nil, err
			}
			l.setState("", err.Error())
			return nil, err
		}
	}
	if !l.srv.ready {
		// A caller that gives up leaves the load running: the next one waits
		// for the same server instead of starting the load again.
		if err := l.srv.waitReady(ctx, l.client); err != nil {
			if l.srv.exited() {
				if strings.Contains(l.rt.RuntimeName(), "vulkan") {
					err = fmt.Errorf("%w (set embed_runtime: cpu in config.yaml to run without the GPU)", err)
				}
				l.setState("", err.Error())
				l.drop()
			} else if l.users == 0 {
				l.armIdle() // nobody may come back for a server that is still loading
			}
			return nil, err
		}
		l.srv.ready = true
		l.setState("running", "")
	}
	l.users++
	return l.srv, nil
}

// release ends one use; the last one arms the idle stop.
func (l *Llama) release() {
	l.sem <- struct{}{}
	defer func() { <-l.sem }()
	l.users--
	if l.users > 0 || l.srv == nil {
		return
	}
	l.armIdle()
}

// armIdle (re)starts the idle stop. The caller holds the lock.
func (l *Llama) armIdle() {
	l.lastUse = time.Now()
	idle := l.IdleStop
	if idle <= 0 {
		idle = defaultIdle
	}
	if l.timer != nil {
		l.timer.Reset(idle)
		return
	}
	l.timer = time.AfterFunc(idle, func() {
		l.sem <- struct{}{}
		defer func() { <-l.sem }()
		if l.users == 0 && l.srv != nil && time.Since(l.lastUse) >= idle {
			l.drop()
		}
	})
}

// drop stops the server, if any. The caller holds the lock.
func (l *Llama) drop() {
	if l.srv == nil {
		return
	}
	l.srv.stop()
	l.srv = nil
	l.setState("", "")
}

// fetch is one run of Runtime.Ensure, which may outlive the call that began it.
type fetch struct {
	done          chan struct{}
	server, model string
	err           error
}

// ensure returns the runtime files. A download runs under the embedder's own
// context, not the caller's: a search that gives up after seconds must not
// cancel minutes of download that the next call would have to start again.
// The caller holds the lock.
func (l *Llama) ensure(ctx context.Context) (string, string, error) {
	if l.fetch == nil {
		f := &fetch{done: make(chan struct{})}
		l.fetch = f
		go func() {
			f.server, f.model, f.err = l.rt.Ensure(l.bg)
			close(f.done)
			// A caller waiting for f holds the lock and takes the result. With
			// none left (it gave up), "starting" ends here, and the next call
			// asks again: the files are there, or the download is retried.
			l.sem <- struct{}{}
			defer func() { <-l.sem }()
			if l.fetch != f {
				return
			}
			l.fetch = nil
			if l.srv != nil || l.closed {
				return
			}
			// Not setState: a download that worked also ends the failure of
			// the one before it.
			l.stMu.Lock()
			l.state, l.lastErr = "", ""
			if f.err != nil {
				l.lastErr = fmt.Errorf("%w: %w", ErrUnavailable, f.err).Error()
			}
			l.stMu.Unlock()
		}()
	}
	f := l.fetch
	select {
	case <-f.done:
		l.fetch = nil
		return f.server, f.model, f.err
	case <-ctx.Done():
		return "", "", ctx.Err()
	}
}

// start launches llama-server on a free loopback port. It returns once the
// process runs; waitReady waits for the model.
func (l *Llama) start(ctx context.Context) error {
	if err := l.checkMemory(); err != nil {
		return err
	}
	bin, model, err := l.ensure(ctx)
	if err != nil {
		return fmt.Errorf("%w: %w", ErrUnavailable, err)
	}
	sweep(l.runDir, l.client)
	port, err := freePort()
	if err != nil {
		return fmt.Errorf("%w: %w", ErrUnavailable, err)
	}
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return fmt.Errorf("%w: %w", ErrUnavailable, err)
	}
	s := &server{base: "http://127.0.0.1:" + strconv.Itoa(port), key: hex.EncodeToString(raw), done: make(chan struct{}), tail: &tailBuf{}}
	n := strconv.Itoa(l.maxTokens())
	args := []string{"-m", model, "--embedding", "--host", "127.0.0.1", "--port", strconv.Itoa(port),
		"-c", n, "-b", n, "-ub", n, "-np", "1", "--no-ui", "--no-slots", "--offline",
		"--sleep-idle-seconds", strconv.Itoa(int(orphanSleep / time.Second))}
	if l.Threads > 0 {
		t := strconv.Itoa(l.Threads)
		args = append(args, "-t", t, "-tb", t)
	}
	s.cmd = exec.Command(bin, args...)
	s.cmd.Dir = filepath.Dir(bin)
	s.cmd.Env = append(os.Environ(), "LLAMA_API_KEY="+s.key) // not on the command line, where other users could read it
	s.cmd.Stdout, s.cmd.Stderr = s.tail, s.tail
	s.cmd.SysProcAttr = procAttr()
	started := make(chan error, 1)
	go func() {
		// Linux ties the child's "die with the parent" signal to the thread
		// that started it: this one stays locked until the child is gone.
		runtime.LockOSThread()
		defer runtime.UnlockOSThread()
		err := s.cmd.Start()
		started <- err
		if err != nil {
			return
		}
		_ = s.cmd.Wait()
		close(s.done)
	}()
	if err := <-started; err != nil {
		return fmt.Errorf("%w: start %s: %w", ErrUnavailable, bin, err)
	}
	if err := os.MkdirAll(l.runDir, 0o700); err == nil {
		s.rec = filepath.Join(l.runDir, "llama-"+strconv.Itoa(os.Getpid())+".json")
		data, _ := json.Marshal(record{Owner: os.Getpid(), Server: s.cmd.Process.Pid, Port: port, Key: s.key})
		if os.WriteFile(s.rec, data, 0o600) != nil {
			s.rec = ""
		}
	}
	l.srv = s
	return nil
}

// waitReady polls /health until the model is loaded. No clock: a load takes
// what the machine needs; the process ending or the caller giving up ends it.
func (s *server) waitReady(ctx context.Context, c *http.Client) error {
	for {
		if s.exited() {
			return fmt.Errorf("%w: llama-server stopped while starting: %s", ErrUnavailable, s.tail.last())
		}
		rctx, cancel := context.WithTimeout(ctx, 2*time.Second)
		req, err := http.NewRequestWithContext(rctx, http.MethodGet, s.base+"/health", nil)
		if err == nil {
			var res *http.Response
			if res, err = c.Do(req); err == nil {
				res.Body.Close()
				if res.StatusCode == http.StatusOK {
					cancel()
					return nil
				}
			}
		}
		cancel()
		select {
		case <-ctx.Done():
			return fmt.Errorf("%w: %w", ErrUnavailable, ctx.Err())
		case <-s.done:
		case <-time.After(100 * time.Millisecond):
		}
	}
}

// stop ends the process: an interrupt it handles cleanly, then a kill.
func (s *server) stop() {
	if !s.exited() {
		if runtime.GOOS == "windows" || s.cmd.Process.Signal(os.Interrupt) != nil {
			_ = s.cmd.Process.Kill()
		}
		select {
		case <-s.done:
		case <-time.After(3 * time.Second):
			_ = s.cmd.Process.Kill()
			<-s.done
		}
	}
	if s.rec != "" {
		_ = os.Remove(s.rec)
	}
}

// sweep removes servers left behind by RagCode processes that were killed
// before they could stop them. A record counts only when its owner is gone
// and its port still answers to its key: then the pid is that server.
func sweep(dir string, c *http.Client) {
	recs, _ := filepath.Glob(filepath.Join(dir, "llama-*.json"))
	for _, p := range recs {
		var r record
		data, err := os.ReadFile(p)
		if err != nil || json.Unmarshal(data, &r) != nil {
			_ = os.Remove(p)
			continue
		}
		if r.Owner != os.Getpid() && pidAlive(r.Owner) {
			continue
		}
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://127.0.0.1:"+strconv.Itoa(r.Port)+"/props", nil)
		if err == nil {
			req.Header.Set("Authorization", "Bearer "+r.Key)
			if res, err := c.Do(req); err == nil {
				res.Body.Close()
				if res.StatusCode == http.StatusOK && r.Server > 0 {
					if proc, err := os.FindProcess(r.Server); err == nil {
						_ = proc.Kill()
					}
				}
			}
		}
		cancel()
		_ = os.Remove(p)
	}
}

// freePort asks the system for an unused loopback port. Another process could
// take it before the server binds; the server then exits and the call fails
// once, and the next call picks a new one.
func freePort() (int, error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	defer ln.Close()
	return ln.Addr().(*net.TCPAddr).Port, nil
}

// tailBuf keeps the last bytes written to it.
type tailBuf struct {
	mu  sync.Mutex
	buf []byte
}

const tailMax = 4 << 10

func (t *tailBuf) Write(p []byte) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.buf = append(t.buf, p...)
	if len(t.buf) > 2*tailMax {
		t.buf = append(t.buf[:0], t.buf[len(t.buf)-tailMax:]...)
	}
	return len(p), nil
}

// last returns the final lines of the output, on one line.
func (t *tailBuf) last() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	lines := strings.Split(strings.TrimSpace(string(t.buf)), "\n")
	if len(lines) > 6 {
		lines = lines[len(lines)-6:]
	}
	for i := range lines {
		lines[i] = strings.TrimSpace(lines[i])
	}
	if s := strings.Join(lines, " | "); s != "" {
		return s
	}
	return "no output"
}

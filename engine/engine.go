// Package engine orchestrates workspaces: root validation, the known-roots
// file, one index job queue per root run through a single global slot, and a
// file watcher per root that feeds that queue.
package engine

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/fsnotify/fsnotify"
	"log/slog"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/doITmagic/rag-code-mcp/v2/embed"
	"github.com/doITmagic/rag-code-mcp/v2/index"
	"github.com/doITmagic/rag-code-mcp/v2/internal/config"
	"github.com/doITmagic/rag-code-mcp/v2/store"
)

const lockRetry = 30 * time.Second

// lockRetryDelay is the wait after the n-th consecutive ErrLocked: 2 s
// doubled up to lockRetry. Another server's files run on one save takes
// milliseconds, so the first retry comes soon; a long run elsewhere is polled
// less and less often.
func lockRetryDelay(n int) time.Duration { return min(2*time.Second<<min(n, 5), lockRetry) }

// retryDelay doubles lockRetry per consecutive failure, capped at 10 minutes
// so a workspace that fails every time does not take the slot every 30 s.
func retryDelay(fails int) time.Duration {
	return min(lockRetry<<min(fails, 5), 10*time.Minute)
}

var (
	// ErrNotIndexed is returned by Search and Scroll while the
	// workspace has no collection yet.
	ErrNotIndexed = errors.New("engine: workspace is not indexed yet; run rag_index_workspace or wait for the automatic index to finish")
	// ErrLocked wraps index.ErrLocked with the retry policy, for Status.
	ErrLocked = fmt.Errorf("engine: another process is indexing this workspace, retrying shortly (%w)", index.ErrLocked)
	// ErrClosed is returned by Workspace after Close.
	ErrClosed = errors.New("engine: closed")
)

// RootError says why a path cannot be a workspace root.
type RootError struct{ Root, Reason string }

func (e *RootError) Error() string {
	return fmt.Sprintf("engine: workspace root %q %s; pass the absolute path of the project directory as workspace_root", e.Root, e.Reason)
}

// UnsureRootError refuses a valid directory that does not look like the top of
// one project; the caller asks the user instead of guessing.
type UnsureRootError struct {
	Root, Why  string
	Candidates []string
}

func (e *UnsureRootError) Error() string {
	return fmt.Sprintf("engine: workspace root %q %s. Nothing was indexed. Do not guess: ask the user which directory is the project (candidates: %s), then call again with that path. Only if the user answers that they want exactly this directory, call rag_index_workspace with confirm: true. Never set confirm without the user's answer.",
		e.Root, e.Why, strings.Join(e.Candidates, ", "))
}

// projectFiles mark the top directory of a project (the list the tools name).
var projectFiles = []string{".git", "go.mod", "package.json", "composer.json", "pyproject.toml", "project.godot"}

func hasProjectFile(dir string) bool {
	return slices.ContainsFunc(projectFiles, func(f string) bool { _, err := os.Lstat(filepath.Join(dir, f)); return err == nil })
}

// unsureRoot says why real, a valid root no server has opened before, may not
// be the top directory of one project, and what to offer instead: a
// subdirectory of a git repository or of a known root, or a directory with no
// project file that holds several projects (looked for two levels down).
func unsureRoot(cfg config.Config, real string, known []string) (string, []string) {
	if out, err := exec.Command("git", "-C", real, "rev-parse", "--show-toplevel").Output(); err == nil {
		top, err := filepath.EvalSymlinks(filepath.FromSlash(strings.TrimSpace(string(out))))
		if err != nil || samePath(top, real) {
			return "", nil
		}
		// A repository at a refused directory (dotfiles in the home) is not the project.
		if _, err := validateRoot(cfg, top); err == nil {
			return "is a subdirectory of the git repository " + top, []string{top}
		}
	}
	if hasProjectFile(real) {
		return "", nil
	}
	for _, k := range known {
		if strings.HasPrefix(real, k+string(filepath.Separator)) {
			return "is inside the project " + k + ", which is already indexed", []string{k}
		}
	}
	var found []string
	subdirs := func(dir string) []string {
		var out []string
		es, _ := os.ReadDir(dir)
		for _, e := range es {
			if e.IsDir() && !strings.HasPrefix(e.Name(), ".") && !slices.Contains(cfg.ExcludePatterns, e.Name()) {
				out = append(out, filepath.Join(dir, e.Name()))
			}
		}
		return out
	}
	for _, d := range subdirs(real) {
		if hasProjectFile(d) {
			found = append(found, d)
			continue
		}
		for _, dd := range subdirs(d) {
			if hasProjectFile(dd) {
				found = append(found, dd)
			}
		}
	}
	if len(found) >= 2 {
		return "has no project file of its own and holds several projects", found[:min(len(found), 8)]
	}
	return "", nil
}

// ConfirmRoot records root as a known root: the user confirmed it, so
// Workspace no longer questions it.
func (e *Engine) ConfirmRoot(root string) error {
	real, err := validateRoot(e.cfg, root)
	if err != nil {
		return err
	}
	return e.registerRoot(real)
}

// Engine owns every workspace of the process and the one indexing slot.
type Engine struct {
	cfg   config.Config
	store store.Store
	embed embed.Embedder
	log   *slog.Logger
	run   func(context.Context, index.Options) (index.Status, error) // index.Run; tests swap it

	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup // job runners and watcher loops
	slot   chan struct{}  // capacity 1: workspaces index one at a time

	mu      sync.Mutex // guards ws, closed, fsw and every job field of a Workspace
	ws      map[string]*Workspace
	closed  bool
	fsw     *fsnotify.Watcher // one inotify instance for every root, started on first watch
	rootsMu sync.Mutex        // with roots.json.lock across processes: held across the roots.json rewrite
}

// New returns an engine. log may be nil.
func New(cfg config.Config, st store.Store, em embed.Embedder, log *slog.Logger) *Engine {
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	ctx, cancel := context.WithCancel(context.Background())
	return &Engine{cfg: cfg, store: st, embed: em, log: log, run: index.Run,
		ctx: ctx, cancel: cancel, slot: make(chan struct{}, 1), ws: map[string]*Workspace{}}
}

// Loaded returns the workspace this process opened for a resolved root, or
// nil; unlike Workspace it never validates, registers or starts anything.
// A nil Engine has none.
func (e *Engine) Loaded(root string) *Workspace {
	if e == nil {
		return nil
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.closed {
		return nil
	}
	return e.ws[root]
}

// Workspace validates root, registers it and returns its workspace. The
// returned error is a *RootError when the path is not usable. One call per
// root does the work; later calls return the same workspace.
func (e *Engine) Workspace(root string) (*Workspace, error) {
	real, err := validateRoot(e.cfg, root)
	if err != nil {
		return nil, err
	}
	e.mu.Lock()
	_, seen := e.ws[real]
	e.mu.Unlock()
	if !seen { // first contact in this process: a root no server knows is checked once
		known, _ := ReadRoots()
		if !slices.ContainsFunc(known, func(k string) bool { return samePath(k, real) }) {
			if why, cands := unsureRoot(e.cfg, real, known); why != "" {
				return nil, &UnsureRootError{Root: real, Why: why, Candidates: cands}
			}
		}
	}
	e.mu.Lock()
	w, ok := e.ws[real]
	if e.closed {
		w = nil
	} else if !ok {
		w = &Workspace{e: e, Root: real, Collection: store.CollectionName(real)}
		e.ws[real] = w
	}
	e.mu.Unlock()
	if w == nil {
		return nil, ErrClosed
	}
	if ok {
		e.mu.Lock()
		back := w.gone
		w.gone = false
		e.mu.Unlock()
		if back {
			if leader, err := e.startWatcher(w); errors.Is(err, ErrClosed) {
				return nil, err
			} else if err != nil {
				e.watchFailed(w, err)
			} else if leader {
				w.Index(false) // catch up on what changed while it was gone
			}
		}
		return w, nil
	}
	if err := e.registerRoot(real); err != nil {
		e.log.Warn("engine: cannot update roots.json", "root", real, "err", err)
	}
	// auto_index: false leaves indexing to an explicit rag_index_workspace, so
	// no watcher may start one.
	if e.cfg.WatchEnabled && e.cfg.AutoIndex {
		if fs := noNotify(real); fs != "" { // first contact: once per root per process
			e.log.Warn("engine: "+notWatched, "root", real, "fs", fs)
			e.mu.Lock()
			w.noEvents = "the " + fs + " file system reports no changes"
			e.mu.Unlock()
		}
		if _, err := e.startWatcher(w); errors.Is(err, ErrClosed) {
			return nil, err
		} else if err != nil {
			e.watchFailed(w, err)
		}
	}
	return w, nil
}

// notWatched tells the user how edits reach the index of a root without a
// working watcher.
const notWatched = "root not watched, edits are indexed only by rag_index_workspace or the catch-up run at the next server start"

// watchFailed marks w for a watcher retry on the next Workspace call (with a
// catch-up run) and warns once per distinct error: a file system that cannot
// be watched (\\wsl.localhost on Windows) fails the same way on every call.
func (e *Engine) watchFailed(w *Workspace, err error) {
	e.mu.Lock()
	w.gone = true
	again := w.watchErr == err.Error()
	w.watchErr = err.Error()
	e.mu.Unlock()
	if !again {
		e.log.Warn("engine: "+notWatched+"; retrying on later calls", "root", w.Root, "err", err)
	}
}

// broadDirs are refused only when given exactly: projects often live below
// them (/var/www/site, /opt/app).
// The /usr/* entries are where merged-/usr systems link /bin, /sbin, /lib.
var broadDirs = []string{"/etc", "/usr", "/var", "/bin", "/sbin", "/lib", "/lib64", "/boot", "/opt", "/srv", "/usr/bin", "/usr/sbin", "/usr/lib", "/usr/lib64"}

// windowsSystemDir reports whether p, a cleaned Windows path, is %SystemRoot%
// or below it (nothing there is a project), or exactly %ProgramFiles%,
// %ProgramFiles(x86)% or %ProgramData% (like broadDirs: apps live below them).
// A client may start the server with a scrubbed environment, so each falls
// back to the system drive, then C:.
func windowsSystemDir(p string, getenv func(string) string) bool {
	env := func(dflt string, keys ...string) string {
		for _, k := range keys {
			if v := strings.TrimRight(getenv(k), `\/`); v != "" {
				return v
			}
		}
		if d := getenv("SystemDrive"); d != "" {
			return d + dflt
		}
		return "C:" + dflt
	}
	sys := env(`\Windows`, "SystemRoot", "windir")
	if len(p) >= len(sys) && strings.EqualFold(p[:len(sys)], sys) && (len(p) == len(sys) || p[len(sys)] == '\\') {
		return true
	}
	return slices.ContainsFunc([]string{env(`\Program Files`, "ProgramFiles"), env(`\Program Files (x86)`, "ProgramFiles(x86)"), env(`\ProgramData`, "ProgramData")},
		func(d string) bool { return strings.EqualFold(p, d) })
}

// wslWindowsSystemDir is windowsSystemDir for a Windows drive mounted in WSL
// (/mnt/c/Windows): the Linux server meets the same directories there.
func wslWindowsSystemDir(p string) bool {
	if len(p) < 7 || !strings.HasPrefix(p, "/mnt/") || p[6] != '/' {
		return false
	}
	drive := strings.ToUpper(p[5:6]) + ":"
	return windowsSystemDir(drive+strings.ReplaceAll(p[6:], "/", `\`), func(k string) string {
		if k == "SystemDrive" {
			return drive
		}
		return ""
	})
}

// validateRoot returns the cleaned, symlink-resolved root or a *RootError.
func validateRoot(cfg config.Config, root string) (string, error) {
	root = strings.TrimSpace(root)
	real, err := filepath.EvalSymlinks(filepath.Clean(root))
	info, serr := os.Stat(real)
	home, _ := os.UserHomeDir()
	home, _ = filepath.EvalSymlinks(home)
	tmp, _ := filepath.EvalSymlinks(os.TempDir())
	var reason string
	switch {
	case root == "":
		reason = "is empty"
	case !filepath.IsAbs(root):
		reason = "is not absolute"
	case err != nil || serr != nil:
		reason = "does not exist"
	case !info.IsDir():
		reason = "is not a directory"
	case filepath.Dir(real) == real:
		reason = "is a filesystem root"
	case home != "" && samePath(real, home):
		reason = "is the home directory"
	case samePath(real, tmp):
		reason = "is the temp directory"
	case index.InOwnHome(real, index.OwnHome()):
		reason = "is inside RagCode's own data directory (" + index.OwnHome() + ")"
	case home != "" && strings.HasPrefix(home, real+string(filepath.Separator)):
		reason = "contains the home directory"
	case runtime.GOOS != "windows" && (slices.Contains(broadDirs, real) || slices.Contains(broadDirs, filepath.Clean(root))):
		reason = "is a system directory (" + real + ")"
	case runtime.GOOS != "windows" && slices.ContainsFunc([]string{"/proc", "/sys", "/dev"}, func(p string) bool {
		return real == p || strings.HasPrefix(real, p+"/")
	}):
		reason = "is a system directory (" + real + ")"
	case runtime.GOOS == "windows" && (windowsSystemDir(real, os.Getenv) || windowsSystemDir(filepath.Clean(root), os.Getenv)),
		runtime.GOOS == "linux" && wslWindowsSystemDir(real):
		reason = "is a system directory (" + real + ")"
	case len(cfg.AllowedWorkspacePaths) > 0 && !slices.ContainsFunc(cfg.AllowedWorkspacePaths, func(p string) bool {
		ap, err := filepath.EvalSymlinks(filepath.Clean(p))
		return err == nil && (samePath(real, ap) || strings.HasPrefix(real, ap+string(filepath.Separator)))
	}):
		reason = "is outside allowed_workspace_paths " + strings.Join(cfg.AllowedWorkspacePaths, ", ")
	}
	if reason != "" {
		return "", &RootError{root, reason}
	}
	return real, nil
}

func samePath(a, b string) bool {
	if runtime.GOOS == "windows" {
		return strings.EqualFold(filepath.Clean(a), filepath.Clean(b))
	}
	return filepath.Clean(a) == filepath.Clean(b)
}

// ---- roots.json: the known roots, used by Resume ----

func rootsPath() string { return filepath.Join(config.HomeDir(), "roots.json") }

// ReadRoots returns the roots in roots.json. A missing or blank file is no
// roots; a leading UTF-8 BOM (Windows PowerShell 5 writes one) is skipped;
// anything else that is not a JSON array of strings is an error.
func ReadRoots() ([]string, error) {
	data, err := os.ReadFile(rootsPath())
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	data = bytes.TrimSpace(bytes.TrimPrefix(data, []byte("\xef\xbb\xbf")))
	if len(data) == 0 {
		return nil, nil
	}
	var roots []string
	if err := json.Unmarshal(data, &roots); err != nil {
		return nil, fmt.Errorf("%s: %w", rootsPath(), err)
	}
	return roots, nil
}

func (e *Engine) registerRoot(root string) error {
	e.rootsMu.Lock()
	defer e.rootsMu.Unlock()
	// Other server processes rewrite the file too: without the file lock, two
	// first contacts at once keep only the last writer's root.
	if err := os.MkdirAll(filepath.Dir(rootsPath()), 0o755); err != nil {
		return err
	}
	var unlock func()
	var err error
	for i := 0; ; i++ {
		if unlock, err = index.LockFile(rootsPath() + ".lock"); !errors.Is(err, index.ErrLocked) || i == 100 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if err != nil {
		return err
	}
	defer unlock()
	roots, err := ReadRoots()
	if err != nil {
		// Rewriting it would drop every root it lists: keep it for the user.
		if rerr := os.Rename(rootsPath(), rootsPath()+".bad"); rerr != nil {
			return errors.Join(err, rerr)
		}
		e.log.Warn("engine: roots.json unreadable, kept as roots.json.bad", "err", err)
	}
	if slices.Contains(roots, root) {
		return nil
	}
	roots = append(roots, root)
	slices.Sort(roots)
	data, _ := json.MarshalIndent(roots, "", "  ")
	return index.WriteAtomic(rootsPath(), data)
}

// Resume opens every known root that still exists and, when auto_index is on,
// queues an incremental run for each root indexed before, so edits made while
// the server was down are picked up (the mtime/size diff keeps it cheap). A
// follower (another server watches the root) skips it.
func (e *Engine) Resume(ctx context.Context) {
	roots, err := ReadRoots()
	if err != nil {
		e.log.Warn("engine: roots.json unreadable, no known root resumed", "err", err)
	}
	for _, root := range roots {
		if ctx.Err() != nil {
			return
		}
		w, err := e.Workspace(root)
		if errors.Is(err, ErrClosed) {
			return
		}
		if err != nil {
			e.log.Info("engine: skipping known root", "root", root, "err", err)
			continue
		}
		e.mu.Lock()
		follower := w.takeover != nil // the leader keeps the index current
		e.mu.Unlock()
		if _, ok := index.ReadStatus(root); ok && e.cfg.AutoIndex && !follower && e.enqueue(w, kindFull, nil, nil) {
			e.log.Info("engine: catch-up index", "root", root)
		}
	}
}

// Close stops watchers and retry timers, cancels the running job and waits
// for every goroutine the engine started.
func (e *Engine) Close() {
	e.mu.Lock()
	e.closed = true
	e.cancel()
	var watchers []*watcher
	for _, w := range e.ws {
		if w.watch != nil {
			watchers = append(watchers, w.watch)
		}
		if w.retry != nil {
			w.retry.Stop()
		}
		if w.takeover != nil {
			w.takeover.Stop()
		}
	}
	fsw := e.fsw
	e.mu.Unlock()
	for _, w := range watchers {
		w.Stop()
	}
	if fsw != nil {
		fsw.Close() // ends the watch loop
	}
	e.wg.Wait()
}

// ---- job queue ----

type queueKind int

const (
	kindNone     queueKind = iota
	kindFiles              // IndexFiles: only the listed files
	kindFull               // walk the tree, mtime/size diff against the state
	kindRecreate           // drop the collection, then full
)

// enqueue merges a request into the workspace queue and starts its runner
// when idle; false once Close has begun.
func (e *Engine) enqueue(w *Workspace, kind queueKind, files, deleted []string) bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	if !e.closed {
		w.fails = 0 // a new request gets a fresh backoff
		e.merge(w, kind, files, deleted)
		e.kick(w)
	}
	return !e.closed
}

// merge adds a request to w's queue; a queued full run absorbs file runs.
// Caller holds e.mu.
func (e *Engine) merge(w *Workspace, kind queueKind, files, deleted []string) {
	w.queued = max(w.queued, kind)
	if w.queued != kindFiles {
		w.files, w.deleted = nil, nil
		return
	}
	if w.files == nil {
		w.files, w.deleted = map[string]bool{}, map[string]bool{}
	}
	for _, f := range files {
		w.files[f] = true
	}
	for _, f := range deleted {
		w.deleted[f] = true
	}
}

// kick starts the runner for w unless it is running, idle or waiting for the
// lock retry. Caller holds e.mu.
func (e *Engine) kick(w *Workspace) {
	if e.closed || w.running || w.retry != nil || w.queued == kindNone {
		return
	}
	w.running = true
	e.wg.Add(1)
	go e.runJobs(w)
}

// idleHook runs when the runner found its queue empty; tests use it.
var idleHook func()

// runJobs drains w's queue: a request made during a run starts right after
// it. Runs of different workspaces serialize on the slot.
func (e *Engine) runJobs(w *Workspace) {
	defer e.wg.Done()
	for {
		e.mu.Lock()
		kind := w.queued
		files, deleted := slices.Sorted(maps.Keys(w.files)), slices.Sorted(maps.Keys(w.deleted))
		w.queued, w.files, w.deleted = kindNone, nil, nil
		e.mu.Unlock()
		if kind != kindNone && e.ctx.Err() == nil {
			select {
			case e.slot <- struct{}{}:
			case <-e.ctx.Done():
				kind = kindNone
			}
		}
		if kind == kindNone || e.ctx.Err() != nil {
			if idleHook != nil {
				idleHook()
			}
			e.mu.Lock()
			if w.queued != kindNone && e.ctx.Err() == nil {
				// Queued after the read above: kick saw running and left it to us.
				e.mu.Unlock()
				continue
			}
			w.running = false
			e.mu.Unlock()
			if e.ctx.Err() == nil {
				// The runtime keeps a run's heap for minutes; an idle server would hold it.
				debug.FreeOSMemory()
			}
			return
		}
		e.mu.Lock()
		w.active, w.lastErr = true, nil // a new run: the last one's error is history
		e.mu.Unlock()
		st, err := e.runOne(w, kind, files, deleted)
		<-e.slot

		e.mu.Lock()
		w.active = false
		w.lastErr = err
		if errors.Is(err, index.ErrLocked) {
			w.lastErr = ErrLocked // nothing ran: the status stays
		} else if err == nil && st.Total == 0 && st.Deleted == 0 && st.Failed == 0 && !st.Recreated {
			w.status = index.Status{} // nothing to do: Status reads the last real run index kept on disk
		} else {
			w.status = st
		}
		if err == nil {
			w.fails = 0
		}
		if !errors.Is(err, index.ErrLocked) {
			w.lockFails = 0 // a run took place
		}
		switch {
		case errors.Is(err, index.ErrHeadChanged):
			w.queued = max(w.queued, kindFull)
		case err != nil && e.ctx.Err() == nil && !isDir(w.Root):
			// The root was deleted: a retry would fail forever. The next call
			// re-validates it, and catches up if it is back.
			e.log.Warn("engine: workspace root is gone, not retrying", "root", w.Root, "err", err)
			w.gone, w.running = e.cfg.WatchEnabled && e.cfg.AutoIndex, false // gone restarts the watcher
			wt := w.watch
			w.watch = nil
			if w.takeover != nil {
				w.takeover.Stop()
				w.takeover = nil
			}
			e.mu.Unlock()
			if wt != nil {
				// Not forget(): dirs belongs to the watch loop, which forgets on the
				// root's own Remove event; inotify drops a deleted tree's watches.
				wt.Stop()
			}
			return
		case err != nil && e.ctx.Err() == nil:
			// Keep the request and retry it after a pause, so neither another
			// process's lock nor an embedder or store outage drops its files.
			var delay time.Duration
			if errors.Is(err, index.ErrLocked) {
				// Normal with several clients on one root: not a failure, so
				// no failure backoff; the poll of a free flock costs nothing.
				delay = lockRetryDelay(w.lockFails)
				w.lockFails++
				e.log.Info("engine: another process is indexing this root, retrying", "root", w.Root, "in", delay)
			} else {
				delay = retryDelay(w.fails)
				w.fails++
				e.log.Warn("engine: index run failed, retrying", "root", w.Root, "in", delay, "err", err)
			}
			if kind == kindRecreate && st.Recreated {
				kind = kindFull // dropped already: resume, do not throw away what was re-indexed
			}
			e.merge(w, kind, files, deleted)
			w.running = false
			w.retry = time.AfterFunc(delay, func() {
				e.mu.Lock()
				w.retry = nil
				e.kick(w)
				e.mu.Unlock()
			})
			e.mu.Unlock()
			return
		}
		e.mu.Unlock()
	}
}

func (e *Engine) runOne(w *Workspace, kind queueKind, files, deleted []string) (index.Status, error) {
	o := index.Options{Root: w.Root, Collection: w.Collection, Store: e.store, Embedder: e.embed,
		Recreate: kind == kindRecreate, Exclude: e.cfg.ExcludePatterns, Log: e.log, Progress: func(s index.Status) {
			e.mu.Lock()
			w.status = s
			e.mu.Unlock()
		}}
	if kind == kindFiles {
		o.Files = append([]string{}, files...) // non-nil: a files run, even with only deletions
		o.Deleted = deleted
	}
	e.log.Info("engine: index run", "root", w.Root, "kind", kind, "files", len(files), "deleted", len(deleted))
	start := time.Now()
	st, err := e.run(e.ctx, o)
	if errors.Is(err, index.ErrHeadChanged) {
		e.log.Info("engine: index run stopped, git HEAD changed during the run; a full run follows", "root", w.Root,
			"done", st.Done, "deleted", st.Deleted, "duration", time.Since(start).Round(time.Millisecond))
	}
	if err == nil { // failures are logged by the caller, with the retry
		e.log.Info("engine: index run finished", "root", w.Root, "phase", st.Phase, "total", st.Total, "done", st.Done,
			"failed", st.Failed, "deleted", st.Deleted, "duration", time.Since(start).Round(time.Millisecond))
	}
	return st, err
}

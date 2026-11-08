// Package engine orchestrates workspaces: root validation, the known-roots
// file, one index job queue per root run through a single global slot, and a
// file watcher per root that feeds that queue.
package engine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/fsnotify/fsnotify"
	"log/slog"
	"maps"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/doITmagic/rag-code-mcp/v3/embed"
	"github.com/doITmagic/rag-code-mcp/v3/index"
	"github.com/doITmagic/rag-code-mcp/v3/internal/config"
	"github.com/doITmagic/rag-code-mcp/v3/store"
)

const lockRetry = 30 * time.Second

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
	ErrLocked = fmt.Errorf("engine: another process is indexing this workspace, retrying in %s (%w)", lockRetry, index.ErrLocked)
	// ErrClosed is returned by Workspace after Close.
	ErrClosed = errors.New("engine: closed")
)

// RootError says why a path cannot be a workspace root.
type RootError struct{ Root, Reason string }

func (e *RootError) Error() string {
	return fmt.Sprintf("engine: workspace root %q %s; pass the absolute path of the project directory as workspace_root", e.Root, e.Reason)
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
			if err := e.startWatcher(w); errors.Is(err, ErrClosed) {
				return nil, err
			} else if err != nil {
				e.log.Warn("engine: watcher not restarted", "root", real, "err", err)
				e.mu.Lock()
				w.gone = true // retry on the next call
				e.mu.Unlock()
			} else {
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
		if err := e.startWatcher(w); errors.Is(err, ErrClosed) {
			return nil, err
		} else if err != nil {
			e.log.Warn("engine: watcher not started, retrying on the next call", "root", real, "err", err)
			e.mu.Lock()
			w.gone = true // the next Workspace call retries, with a catch-up run
			e.mu.Unlock()
		}
	}
	return w, nil
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
	case runtime.GOOS != "windows" && slices.ContainsFunc([]string{"/proc", "/sys", "/dev"}, func(p string) bool {
		return real == p || strings.HasPrefix(real, p+"/")
	}):
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

func readRoots() []string {
	var roots []string
	if data, err := os.ReadFile(rootsPath()); err == nil {
		_ = json.Unmarshal(data, &roots)
	}
	return roots
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
	roots := readRoots()
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
// the server was down are picked up (the mtime/size diff keeps it cheap).
func (e *Engine) Resume(ctx context.Context) {
	for _, root := range readRoots() {
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
		if _, ok := index.ReadStatus(root); ok && e.cfg.AutoIndex && e.enqueue(w, kindFull, nil, nil) {
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
			delay := lockRetry
			if errors.Is(err, index.ErrLocked) {
				// Normal with several clients on one root: not a failure, so
				// no backoff, and the poll of a free flock costs nothing.
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

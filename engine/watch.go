package engine

import (
	"errors"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/doITmagic/rag-code-mcp/v2/index"
	"github.com/doITmagic/rag-code-mcp/v2/parser"
	"github.com/fsnotify/fsnotify"
)

// maxDebounces caps how many debounce periods a stream of events can hold a
// batch back; a file written without pause would otherwise block every edit.
const maxDebounces = 10

// watcher turns fsnotify events of one root into IndexFiles batches. A change
// to .git/HEAD, .git/refs or a .gitignore, an event overflow or a removed
// directory queue a full run instead.
type watcher struct {
	ws     *Workspace
	fsw    *fsnotify.Watcher // the engine's, shared by every root
	unlock func()            // releases watch.lock; called once, by Stop
	once   sync.Once

	dirs map[string]bool // watched directories; the engine's watch loop owns it

	mu      sync.Mutex
	timer   *time.Timer
	first   time.Time // first event of the pending batch
	changed map[string]bool
	deleted map[string]bool
}

// newFSWatcher is fsnotify.NewWatcher; tests swap it.
var newFSWatcher = fsnotify.NewWatcher

// watchFS returns the engine's one inotify instance, started on first use.
// Every root shares it: the per-user instance limit is small (128 on Linux)
// and every client starts its own server. Caller holds e.mu.
func (e *Engine) watchFS() (*fsnotify.Watcher, error) {
	if e.closed {
		return nil, ErrClosed
	}
	if e.fsw == nil {
		fsw, err := newFSWatcher()
		if err != nil {
			return nil, err
		}
		e.fsw = fsw
		e.wg.Add(1)
		go e.watchLoop(fsw)
	}
	return e.fsw, nil
}

// takeoverPoll is how often a follower retries the leader lock; tests shorten it.
var takeoverPoll = 5 * time.Second

// startWatcher makes this process the root's leader and watches it, and
// reports whether it did. Every client starts its own server: only the holder
// of index.Dir(root)/watch.lock watches and indexes on file events, so a save
// is not queued by every server. On ErrLocked the workspace is a follower: it
// answers queries from the shared index and polls the lock to take over.
func (e *Engine) startWatcher(w *Workspace) (leader bool, err error) {
	dir := index.Dir(w.Root)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return false, err
	}
	unlock, err := index.LockFile(filepath.Join(dir, "watch.lock"))
	if errors.Is(err, index.ErrLocked) {
		e.follow(w)
		return false, nil
	}
	if err != nil {
		return false, err
	}
	e.mu.Lock()
	fsw, err := e.watchFS()
	e.mu.Unlock()
	if err != nil {
		unlock()
		return false, err
	}
	wt := &watcher{ws: w, fsw: fsw, unlock: unlock, dirs: map[string]bool{}, changed: map[string]bool{}, deleted: map[string]bool{}}
	if err := wt.addTree(w.Root, false); err != nil {
		wt.Stop()
		wt.forget()
		if errors.Is(err, fsnotify.ErrClosed) {
			return false, ErrClosed // Close began after watchFS
		}
		return false, err
	}
	wt.watchGit()
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.closed {
		wt.Stop()
		return false, ErrClosed
	}
	if w.watch != nil { // a concurrent call won (only where locking is a no-op); its watches are the same
		wt.Stop()
		return true, nil
	}
	w.watch, w.watchErr = wt, "" // a later failure is a new episode: warned again
	return true, nil
}

// follow arms the takeover poll of a follower; idempotent.
func (e *Engine) follow(w *Workspace) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.closed || w.watch != nil || w.takeover != nil {
		return
	}
	w.takeover = time.AfterFunc(takeoverPoll, func() {
		e.mu.Lock()
		w.takeover = nil
		if e.closed || w.gone || w.watch != nil {
			e.mu.Unlock()
			return
		}
		e.wg.Add(1) // Close waits for a takeover in progress
		e.mu.Unlock()
		defer e.wg.Done()
		e.takeOver(w)
	})
}

// takeOver retries the leader lock; on success the catch-up run picks up
// what changed since the leader exited.
func (e *Engine) takeOver(w *Workspace) {
	if !isDir(w.Root) { // the next Workspace call re-validates it
		e.mu.Lock()
		w.gone = true
		e.mu.Unlock()
		return
	}
	leader, err := e.startWatcher(w)
	switch {
	case errors.Is(err, ErrClosed):
	case err != nil:
		e.watchFailed(w, err)
	case leader:
		e.log.Info("engine: watcher taken over", "root", w.Root)
		w.catchUp()
	}
}

// watchLoop hands each event to every watcher of a root containing it.
func (e *Engine) watchLoop(fsw *fsnotify.Watcher) {
	defer e.wg.Done()
	watchers := func() []*watcher {
		e.mu.Lock()
		defer e.mu.Unlock()
		var out []*watcher
		for _, w := range e.ws {
			if w.watch != nil {
				out = append(out, w.watch)
			}
		}
		return out
	}
	for {
		select {
		case ev, ok := <-fsw.Events:
			if !ok {
				return
			}
			for _, wt := range watchers() {
				wt.handle(ev)
			}
		case err, ok := <-fsw.Errors:
			if !ok {
				return
			}
			for _, wt := range watchers() {
				if errors.Is(err, fsnotify.ErrEventOverflow) {
					// Lost events may include the Create of a new directory.
					if err := wt.addTree(wt.ws.Root, false); err != nil {
						e.log.Warn("engine: cannot re-watch after overflow", "root", wt.ws.Root, "err", err)
					}
					wt.full("event overflow")
				} else {
					e.log.Warn("engine: watcher error", "root", wt.ws.Root, "err", err)
				}
			}
		}
	}
}

// addTree watches dir and every non-hidden, non-excluded directory below it.
// With collect, the files found are queued as changed: a populated directory
// moved into the tree emits no events for its contents.
func (wt *watcher) addTree(dir string, collect bool) error {
	home := index.OwnHome()
	if index.InOwnHome(dir, home) {
		return nil
	}
	return filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		switch {
		case err != nil:
			if path == dir {
				return err
			}
		case !d.IsDir():
			if collect {
				wt.add(path, false)
			}
		case path != dir && (wt.skipDir(d.Name()) || index.IsLinkedWorktree(path) || index.InOwnHome(path, home)):
			return fs.SkipDir
		default:
			if err := wt.fsw.Add(path); err != nil && (path == dir || errors.Is(err, fsnotify.ErrClosed)) {
				return err
			} else if err != nil {
				if !os.IsNotExist(err) {
					wt.ws.e.log.Warn("engine: directory not watched", "dir", path, "err", err)
				}
				return fs.SkipDir
			} else {
				wt.dirs[path] = true
			}
		}
		return nil
	})
}

// watchGit watches the root's .git and its refs tree, when they exist.
func (wt *watcher) watchGit() {
	if git := filepath.Join(wt.ws.Root, ".git"); wt.fsw.Add(git) == nil {
		wt.dirs[git] = true
		_ = wt.addTree(filepath.Join(git, "refs"), false)
	}
}

func (wt *watcher) skipDir(name string) bool {
	return strings.HasPrefix(name, ".") || slices.Contains(wt.ws.e.cfg.ExcludePatterns, name)
}

// handle processes one event of the shared instance; events outside the
// root are ignored.
func (wt *watcher) handle(ev fsnotify.Event) {
	root := wt.ws.Root
	rel, err := filepath.Rel(root, ev.Name)
	if ev.Op == fsnotify.Chmod || err != nil || strings.HasPrefix(rel, "..") {
		return
	}
	gone := ev.Has(fsnotify.Remove | fsnotify.Rename)
	if rel == "." {
		if gone {
			wt.ws.e.mu.Lock()
			wt.ws.lastErr = errors.New("engine: workspace root was removed or renamed; watcher stopped")
			wt.ws.gone = true
			if wt.ws.watch == wt {
				wt.ws.watch = nil
			}
			wt.ws.e.mu.Unlock()
			wt.Stop()
			wt.forget() // a renamed root keeps its watches under the new name
		}
		return
	}
	parts := strings.Split(rel, string(filepath.Separator))
	if parts[0] == ".git" {
		refs := len(parts) > 1 && parts[1] == "refs"
		switch {
		case rel == ".git" && ev.Has(fsnotify.Create): // git init in a root watched without a repository
			wt.watchGit()
			wt.full("git repository created")
		case refs && ev.Has(fsnotify.Create) && isDir(ev.Name): // refs, refs/heads made after .git was watched
			_ = wt.addTree(ev.Name, false)
			wt.full("git HEAD changed")
		case strings.HasSuffix(rel, ".lock"): // git writes <ref>.lock, then renames it over the ref: wait for the ref
		case rel == filepath.Join(".git", "HEAD") || (refs && len(parts) > 2):
			wt.full("git HEAD changed")
		}
		return
	}
	if slices.ContainsFunc(parts[:len(parts)-1], wt.skipDir) || index.InOwnHome(ev.Name, index.OwnHome()) {
		return
	}
	if dir := index.LinkedWorktreeOf(root, ev.Name, nil); dir != "" {
		// git writes a new worktree's .git after its directory was created
		// and watched; the walk purges whatever was indexed from it.
		wt.dropDir(dir)
		wt.full("linked worktree added")
		return
	}
	if parts[len(parts)-1] == ".gitignore" { // only a walk applies ignore rules to indexed files
		wt.full(".gitignore changed")
		return
	}
	switch {
	case gone && wt.dropDir(ev.Name):
		wt.full("directory removed") // the walk reconciles its files
	case ev.Has(fsnotify.Create) && isDir(ev.Name):
		if !wt.skipDir(filepath.Base(ev.Name)) && !index.IsLinkedWorktree(ev.Name) {
			if err := wt.addTree(ev.Name, true); err != nil {
				wt.ws.e.log.Warn("engine: cannot watch new directory", "dir", ev.Name, "err", err)
			}
		}
	case parser.ForFile(ev.Name) == nil: // never indexed (PDFs, images, rsync temp files): nothing to queue
	case gone:
		wt.add(ev.Name, true)
	default:
		wt.add(ev.Name, false)
	}
}

func isDir(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

// dropDir forgets the watches on and below path and reports whether path
// was a watched directory. It consults the watcher's own set: the inotify
// backend has already dropped a moved directory from WatchList() by the time
// the parent's Rename event arrives.
// forget removes every watch of this root from the shared instance.
func (wt *watcher) forget() {
	for p := range wt.dirs {
		_ = wt.fsw.Remove(p)
	}
	wt.dirs = map[string]bool{}
}

func (wt *watcher) dropDir(path string) bool {
	was := wt.dirs[path]
	for p := range wt.dirs {
		if p == path || strings.HasPrefix(p, path+string(filepath.Separator)) {
			_ = wt.fsw.Remove(p)
			delete(wt.dirs, p)
		}
	}
	return was
}

// add records a changed or deleted file and resets the single debounce timer,
// flushing at the latest maxDebounces periods after the batch's first event.
func (wt *watcher) add(path string, deleted bool) {
	wt.mu.Lock()
	defer wt.mu.Unlock()
	if deleted {
		wt.deleted[path] = true
	} else {
		wt.changed[path] = true
	}
	now := time.Now()
	if wt.first.IsZero() {
		wt.first = now
	}
	d := wt.ws.e.cfg.WatchDebounce
	if left := wt.first.Add(maxDebounces * d).Sub(now); left < d {
		d = max(left, 0)
	}
	if wt.timer == nil {
		wt.timer = time.AfterFunc(d, wt.flush)
	} else {
		wt.timer.Reset(d)
	}
}

func (wt *watcher) flush() {
	wt.mu.Lock()
	changed, deleted := slices.Collect(maps.Keys(wt.changed)), slices.Collect(maps.Keys(wt.deleted))
	wt.changed, wt.deleted = map[string]bool{}, map[string]bool{}
	wt.first = time.Time{}
	wt.mu.Unlock()
	wt.ws.IndexFiles(changed, deleted)
}

func (wt *watcher) full(reason string) {
	e := wt.ws.e
	e.mu.Lock()
	queued := wt.ws.queued >= kindFull // a checkout fires many events: log the first
	e.mu.Unlock()
	if !queued {
		e.log.Info("engine: watcher queues a full run", "root", wt.ws.Root, "reason", reason)
	}
	wt.ws.catchUp()
}

// Stop cancels the pending batch and releases the leader lock; idempotent. The engine stops handing
// events to a watcher once it is no longer its workspace's watch.
func (wt *watcher) Stop() {
	wt.once.Do(func() {
		wt.mu.Lock()
		if wt.timer != nil {
			wt.timer.Stop()
		}
		wt.mu.Unlock()
		wt.unlock()
	})
}

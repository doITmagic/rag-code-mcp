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

	"github.com/doITmagic/rag-code-mcp/v3/index"
	"github.com/fsnotify/fsnotify"
)

// maxDebounces caps how many debounce periods a stream of events can hold a
// batch back; a file written without pause would otherwise block every edit.
const maxDebounces = 10

// watcher turns fsnotify events of one root into IndexFiles batches. A change
// to .git/HEAD or .git/refs, an event overflow or a removed directory queue a
// full run instead.
type watcher struct {
	ws   *Workspace
	fsw  *fsnotify.Watcher
	stop chan struct{}
	once sync.Once

	dirs map[string]bool // watched directories; the loop goroutine owns it

	mu      sync.Mutex
	timer   *time.Timer
	first   time.Time // first event of the pending batch
	changed map[string]bool
	deleted map[string]bool
}

func (e *Engine) startWatcher(w *Workspace) error {
	fsw, err := fsnotify.NewWatcher()
	if err != nil {
		return err
	}
	wt := &watcher{ws: w, fsw: fsw, stop: make(chan struct{}), dirs: map[string]bool{}, changed: map[string]bool{}, deleted: map[string]bool{}}
	if err := wt.addTree(w.Root, false); err != nil {
		fsw.Close()
		return err
	}
	if git := filepath.Join(w.Root, ".git"); fsw.Add(git) == nil {
		_ = wt.addTree(filepath.Join(git, "refs"), false)
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.closed {
		fsw.Close()
		return ErrClosed
	}
	if w.watch != nil { // a concurrent call won
		fsw.Close()
		return nil
	}
	w.watch = wt
	e.wg.Add(1)
	go wt.loop()
	return nil
}

// addTree watches dir and every non-hidden, non-excluded directory below it.
// With collect, the files found are queued as changed: a populated directory
// moved into the tree emits no events for its contents.
func (wt *watcher) addTree(dir string, collect bool) error {
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
		case path != dir && (wt.skipDir(d.Name()) || index.IsLinkedWorktree(path)):
			return fs.SkipDir
		default:
			if err := wt.fsw.Add(path); err != nil && path == dir {
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

func (wt *watcher) skipDir(name string) bool {
	return strings.HasPrefix(name, ".") || slices.Contains(wt.ws.e.cfg.ExcludePatterns, name)
}

func (wt *watcher) loop() {
	defer wt.ws.e.wg.Done()
	defer wt.fsw.Close()
	for {
		select {
		case ev, ok := <-wt.fsw.Events:
			if !ok || wt.handle(ev) {
				return
			}
		case err, ok := <-wt.fsw.Errors:
			if !ok {
				return
			}
			if errors.Is(err, fsnotify.ErrEventOverflow) {
				wt.full("event overflow")
			} else {
				wt.ws.e.log.Warn("engine: watcher error", "root", wt.ws.Root, "err", err)
			}
		case <-wt.stop:
			return
		}
	}
}

// handle processes one event; it returns true when the watcher must stop.
func (wt *watcher) handle(ev fsnotify.Event) bool {
	root := wt.ws.Root
	rel, err := filepath.Rel(root, ev.Name)
	if ev.Op == fsnotify.Chmod || err != nil || strings.HasPrefix(rel, "..") {
		return false
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
		}
		return gone
	}
	parts := strings.Split(rel, string(filepath.Separator))
	if parts[0] == ".git" {
		if rel == filepath.Join(".git", "HEAD") || (len(parts) > 2 && parts[1] == "refs") {
			wt.full("git HEAD changed")
		}
		return false
	}
	if slices.ContainsFunc(parts[:len(parts)-1], wt.skipDir) {
		return false
	}
	switch {
	case gone && wt.dropDir(ev.Name):
		wt.full("directory removed") // the walk reconciles its files
	case gone:
		wt.add(ev.Name, true)
	case ev.Has(fsnotify.Create) && isDir(ev.Name):
		if !wt.skipDir(filepath.Base(ev.Name)) && !index.IsLinkedWorktree(ev.Name) {
			if err := wt.addTree(ev.Name, true); err != nil {
				wt.ws.e.log.Warn("engine: cannot watch new directory", "dir", ev.Name, "err", err)
			}
		}
	default:
		wt.add(ev.Name, false)
	}
	return false
}

func isDir(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

// dropDir forgets the watches on and below path and reports whether path
// was a watched directory. It consults the watcher's own set: the inotify
// backend has already dropped a moved directory from WatchList() by the time
// the parent's Rename event arrives.
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
	wt.ws.e.log.Info("engine: watcher queues a full run", "root", wt.ws.Root, "reason", reason)
	wt.ws.Index(false)
}

// Stop ends the loop; idempotent.
func (wt *watcher) Stop() {
	wt.once.Do(func() {
		wt.mu.Lock()
		if wt.timer != nil {
			wt.timer.Stop()
		}
		wt.mu.Unlock()
		close(wt.stop)
	})
}

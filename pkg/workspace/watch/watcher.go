package watch

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/doITmagic/rag-code-mcp/internal/logger"
	"github.com/fsnotify/fsnotify"
)

var defaultExcludeDirs = map[string]struct{}{
	".git":         {},
	".idea":        {},
	".vscode":      {},
	".vs":          {},
	".cursor":      {},
	".windsurf":    {},
	".ragcode":     {},
	"node_modules": {},
	"vendor":       {},
	"dist":         {},
	"build":        {},
	"target":       {},
	".venv":        {},
	"__pycache__":  {},
}

var defaultHiddenAllowlist = map[string]struct{}{
	".git":      {},
	".ragcode":  {},
	".agent":    {},
	".idea":     {},
	".vscode":   {},
	".vs":       {},
	".cursor":   {},
	".windsurf": {},
}

const defaultDebounce = 1 * time.Second

// Options configures watcher behavior.
type Options struct {
	Debounce        time.Duration
	ExcludePatterns []string
}

// FileWatcher handles file system notifications for a workspace.
type FileWatcher struct {
	watcher         *fsnotify.Watcher
	root            string
	stopChan        chan struct{}
	stopOnce        sync.Once
	lifecycleCtx    context.Context
	lifecycleCancel context.CancelFunc
	mu              sync.Mutex
	timer           *time.Timer
	debounce        time.Duration
	onChange        func(context.Context, string, []string) error
	exclude         map[string]struct{}
	changedFiles    map[string]struct{}
}

// NewFileWatcher creates a new file watcher for the given root directory.
func NewFileWatcher(root string, opts Options, onChange func(context.Context, string, []string) error) (*FileWatcher, error) {
	w, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, err
	}

	debounce := opts.Debounce
	if debounce <= 0 {
		debounce = defaultDebounce
	}

	lifecycleCtx, lifecycleCancel := context.WithCancel(context.Background())

	return &FileWatcher{
		watcher:         w,
		root:            root,
		stopChan:        make(chan struct{}),
		lifecycleCtx:    lifecycleCtx,
		lifecycleCancel: lifecycleCancel,
		debounce:        debounce,
		onChange:        onChange,
		exclude:         normalizeExclude(opts.ExcludePatterns),
		changedFiles:    make(map[string]struct{}),
	}, nil
}

// Start begins watching the directory tree.
func (fw *FileWatcher) Start() {
	if IsInvalidRoot(fw.root) {
		logger.Instance.Error("Cannot start watcher on invalid root directory: %s", fw.root)
		return
	}

	if err := fw.addDirectoryTree(fw.root); err != nil {
		logger.Instance.Warn("Error walking directory for watcher setup: %v", err)
	}

	logger.Instance.Info("👀 Watcher started for %s", fw.root)
	go fw.watchLoop()
}

// addDirectoryTree also covers a populated subtree moved into the workspace:
// its existing descendants will not emit separate creation events.
func (fw *FileWatcher) addDirectoryTree(root string) error {
	return filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if fw.shouldSkipDir(path, d.Name(), path == fw.root) {
				return filepath.SkipDir
			}
			if err := fw.watcher.Add(path); err != nil {
				logger.Instance.Warn("Unable to watch %s: %v", path, err)
			}
		}
		return nil
	})
}

// dropDirectoryTree forgets watches below a renamed directory. inotify keeps
// them keyed by the old path, so re-adding the new name would reuse the stale
// entries and report nested edits under the old path forever.
func (fw *FileWatcher) dropDirectoryTree(old string) {
	prefix := old + string(filepath.Separator)
	for _, p := range fw.watcher.WatchList() {
		if strings.HasPrefix(p, prefix) {
			_ = fw.watcher.Remove(p)
		}
	}
}

func (fw *FileWatcher) watchLoop() {
	defer fw.watcher.Close()

	for {
		select {
		case event, ok := <-fw.watcher.Events:
			if !ok {
				return
			}
			if event.Op&fsnotify.Chmod == fsnotify.Chmod {
				continue
			}
			// Events for excluded paths still arrive from watched parents: on
			// Windows, a write inside .ragcode reports ".ragcode" itself on the
			// root watch. Indexing writes there, so without this every index
			// run triggered the next one.
			if fw.isExcludedPath(event.Name) {
				continue
			}
			if event.Op&fsnotify.Rename == fsnotify.Rename {
				fw.dropDirectoryTree(event.Name)
			}
			if event.Op&fsnotify.Create == fsnotify.Create {
				info, err := os.Stat(event.Name)
				if err == nil && info.IsDir() {
					base := filepath.Base(event.Name)
					if fw.shouldSkipDir(event.Name, base, false) {
						continue
					}
					if err := fw.addDirectoryTree(event.Name); err != nil {
						logger.Instance.Warn("Unable to watch new directory tree %s: %v", event.Name, err)
					}
				}
			}

			fw.mu.Lock()
			fw.changedFiles[event.Name] = struct{}{}
			fw.mu.Unlock()

			fw.triggerDebouncedIndex()

		case err, ok := <-fw.watcher.Errors:
			if !ok {
				return
			}
			logger.Instance.Error("Watcher error: %v", err)

		case <-fw.stopChan:
			return
		}
	}
}

func (fw *FileWatcher) triggerDebouncedIndex() {
	fw.mu.Lock()
	defer fw.mu.Unlock()

	if fw.timer != nil {
		fw.timer.Stop()
	}

	fw.timer = time.AfterFunc(fw.debounce, func() {
		fw.mu.Lock()
		files := make([]string, 0, len(fw.changedFiles))
		for f := range fw.changedFiles {
			files = append(files, f)
		}
		// Clear properly inside lock
		fw.changedFiles = make(map[string]struct{})
		fw.mu.Unlock()

		if len(files) == 0 {
			return
		}

		logger.Instance.Info("♻️ File changes detected in %s (%d files) — triggering reindex...", fw.root, len(files))

		if fw.onChange == nil {
			return
		}

		ctx := fw.lifecycleCtx
		if ctx == nil {
			ctx = context.Background()
		}
		if err := fw.onChange(ctx, fw.root, files); err != nil {
			logger.Instance.Error("Auto-reindexing failed: %v", err)
		} else {
			logger.Instance.Info("Reindex request accepted for %s", fw.root)
		}
	})
}

// Stop terminates the watcher.
func (fw *FileWatcher) Stop() {
	fw.stopOnce.Do(func() {
		if fw.lifecycleCancel != nil {
			fw.lifecycleCancel()
		}
		fw.mu.Lock()
		if fw.timer != nil {
			fw.timer.Stop()
		}
		fw.mu.Unlock()
		close(fw.stopChan)
	})
}

// isExcludedPath reports whether path, relative to the root, lies under a
// directory the watcher skips, or names an excluded directory itself. The
// hidden-name rule applies to directory components only: the indexer skips
// hidden directories but does index hidden files (.goreleaser.yaml), so an
// edit to one must still trigger a reindex.
func (fw *FileWatcher) isExcludedPath(path string) bool {
	rel, err := filepath.Rel(fw.root, path)
	if err != nil || strings.HasPrefix(rel, "..") {
		return false
	}
	parts := strings.Split(rel, string(filepath.Separator))
	for _, part := range parts[:len(parts)-1] {
		if part != "." && fw.shouldSkipDir(path, part, false) {
			return true
		}
	}
	_, excluded := fw.exclude[strings.ToLower(parts[len(parts)-1])]
	return excluded
}

func (fw *FileWatcher) shouldSkipDir(path, base string, isRoot bool) bool {
	if isRoot {
		return false
	}
	if _, skip := fw.exclude[strings.ToLower(base)]; skip {
		return true
	}
	if strings.HasPrefix(base, ".") {
		_, allowed := defaultHiddenAllowlist[strings.ToLower(base)]
		return !allowed
	}
	return false
}

func normalizeExclude(patterns []string) map[string]struct{} {
	// Always start from defaultExcludeDirs so critical dirs like .ragcode
	// are never accidentally watched (would cause infinite reindex loops).
	result := make(map[string]struct{}, len(defaultExcludeDirs)+len(patterns))
	for k, v := range defaultExcludeDirs {
		result[k] = v
	}
	for _, pattern := range patterns {
		trimmed := strings.TrimSpace(pattern)
		if trimmed == "" {
			continue
		}
		result[strings.ToLower(trimmed)] = struct{}{}
	}
	return result
}

// IsInvalidRoot reports whether root is an unsafe or degenerate directory
// that must not be used as a workspace root for indexing or watching.
// It rejects the filesystem root (/), the resolved user home directory
// (as returned by os.UserHomeDir — note: literal "~" is NOT expanded),
// and the system temp directory.
func IsInvalidRoot(root string) bool {
	clean := filepath.Clean(strings.TrimSpace(root))
	if clean == "" || clean == "." {
		return true
	}
	// A volume root is its own parent: "/" on Unix, "C:\" on Windows.
	if clean == string(os.PathSeparator) || filepath.Dir(clean) == clean {
		return true
	}
	if home, err := os.UserHomeDir(); err == nil && samePath(clean, home) {
		return true
	}
	return samePath(clean, os.TempDir())
}

// samePath compares two paths, case-insensitively on Windows where
// "c:\users\x" and "C:\Users\x" name the same directory.
func samePath(a, b string) bool {
	a, b = filepath.Clean(a), filepath.Clean(b)
	if runtime.GOOS == "windows" {
		return strings.EqualFold(a, b)
	}
	return a == b
}

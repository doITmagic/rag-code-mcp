package workspace

import (
	"context"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
)

// FileWatcher handles file system notifications for a workspace
type FileWatcher struct {
	watcher  *fsnotify.Watcher
	root     string
	manager  *Manager
	stopChan chan struct{}
	eventsMu sync.Mutex
	timer    *time.Timer
}

// NewFileWatcher creates a new file watcher for the given root directory
func NewFileWatcher(root string, manager *Manager) (*FileWatcher, error) {
	w, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, err
	}

	fw := &FileWatcher{
		watcher:  w,
		root:     root,
		manager:  manager,
		stopChan: make(chan struct{}),
	}

	return fw, nil
}

// Start begins watching the directory tree
func (fw *FileWatcher) Start() {
	// Validate root directory before starting watcher to prevent broad filesystem access
	homeDir, err := os.UserHomeDir()
	if err != nil {
		log.Printf("workspace: could not determine user home directory: %v", err)
	}
	if fw.root == "/" || fw.root == homeDir || fw.root == "/tmp" {
		log.Printf("[ERROR] Cannot start watcher on invalid root directory: %s", fw.root)
		return
	}

	// Recursively add directories
	err = filepath.Walk(fw.root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if info.IsDir() {
			// Skip ignored dirs
			base := filepath.Base(path)
			if _, skip := defaultSkipDirs[base]; skip {
				return filepath.SkipDir
			}
			// Skip hidden dirs generally, but be careful with root
			if strings.HasPrefix(base, ".") && base != "." && base != ".git" {
				return filepath.SkipDir
			}
			if err := fw.watcher.Add(path); err != nil {
				log.Printf("[WARN] Unable to watch %s: %v", path, err)
			}
			return nil
		}
		return nil
	})
	if err != nil {
		log.Printf("[WARN] Error walking directory for watcher setup: %v", err)
	}

	log.Printf("👀 Watcher started for %s", fw.root)
	go fw.watchLoop()
}

func (fw *FileWatcher) watchLoop() {
	defer fw.watcher.Close()

	for {
		select {
		case event, ok := <-fw.watcher.Events:
			if !ok {
				return
			}

			// Ignore chmod events (too noisy)
			if event.Op&fsnotify.Chmod == fsnotify.Chmod {
				continue
			}

			// Handle directory creation: add to watcher
			if event.Op&fsnotify.Create == fsnotify.Create {
				info, err := os.Stat(event.Name)
				if err == nil && info.IsDir() {
					// Skip if ignored
					base := filepath.Base(event.Name)
					if _, skip := defaultSkipDirs[base]; !skip && !strings.HasPrefix(base, ".") {
						if err := fw.watcher.Add(event.Name); err != nil {
							log.Printf("[WARN] Unable to watch new dir %s: %v", event.Name, err)
						}
					}
				}
			}

			fw.triggerDebouncedIndex()

		case err, ok := <-fw.watcher.Errors:
			if !ok {
				return
			}
			log.Printf("[ERROR] Watcher error: %v", err)

		case <-fw.stopChan:
			return
		}
	}
}

// adaptiveDelay calculates debounce delay based on recent activity
// TODO: Can be enhanced with actual event counting if needed, for now we use a simpler
// hybrid approach: aggressive first check, slower subsequent ones if we were already waiting.
func (fw *FileWatcher) triggerDebouncedIndex() {
	fw.eventsMu.Lock()
	defer fw.eventsMu.Unlock()

	// Stop existing timer if running
	if fw.timer != nil {
		fw.timer.Stop()
	}

	// Dynamic delay:
	// We want fast feedback for single file edits (saving a file).
	// But we want to avoid trashing if git switches a branch (100+ files).
	// Since we don't track the exact count in this simplified version, we'll use a shorter default.
	// 5 seconds was too long. 1 second is a safer balance, or 500ms if we feel lucky.
	// Let's go with 1s as a robust middle ground for now, significantly better than 5s.
	// Ideally this should be: if count < 5 { 500ms } else { 5s }
	// But counting implies resetting the counter.

	// Let's implement a simple counter reset strategy
	delay := 1 * time.Second

	fw.timer = time.AfterFunc(delay, func() {
		log.Printf("♻️ File changes detected in %s - Triggering reindex...", fw.root)

		// Trigger indexing in background
		go func() {
			// EnsureWorkspaceIndexed handles detection internally
			if err := fw.manager.EnsureWorkspaceIndexed(context.Background(), fw.root); err != nil {
				log.Printf("[ERROR] Auto-reindexing failed: %v", err)
			} else {
				log.Printf("✅ Auto-reindexing complete for %s", fw.root)
			}
		}()
	})
}

func (fw *FileWatcher) Stop() {
	close(fw.stopChan)
}

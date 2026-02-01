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
	// Recursively add directories
	err := filepath.Walk(fw.root, func(path string, info os.FileInfo, err error) error {
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

func (fw *FileWatcher) triggerDebouncedIndex() {
	fw.eventsMu.Lock()
	defer fw.eventsMu.Unlock()

	if fw.timer != nil {
		fw.timer.Stop()
	}

	// Wait 5 seconds of silence before reindexing
	fw.timer = time.AfterFunc(5*time.Second, func() {
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

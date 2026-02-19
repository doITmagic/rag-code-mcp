package watch

import (
	"context"
	"sync"
	"testing"
)

type fakeWatcher struct {
	mu        sync.Mutex
	started   int
	stopped   int
	startChan chan struct{}
}

func (f *fakeWatcher) Start() {
	f.mu.Lock()
	f.started++
	if f.startChan != nil {
		close(f.startChan)
		f.startChan = nil
	}
	f.mu.Unlock()
}

func (f *fakeWatcher) Stop() {
	f.mu.Lock()
	f.stopped++
	f.mu.Unlock()
}

func TestManagerStartStop(t *testing.T) {
	mgr := NewManager(Options{})
	created := 0
	var watcher *fakeWatcher

	mgr.SetFactory(func(root string, opts Options, onChange OnChangeFunc) (Watcher, error) {
		created++
		watcher = &fakeWatcher{}
		return watcher, nil
	})

	if err := mgr.Start("/tmp/project", func(ctx context.Context, root string, files []string) error { return nil }); err != nil {
		t.Fatalf("start watcher: %v", err)
	}
	if err := mgr.Start("/tmp/project", func(ctx context.Context, root string, files []string) error { return nil }); err != nil {
		t.Fatalf("start watcher second time: %v", err)
	}

	if created != 1 {
		t.Fatalf("expected 1 watcher, got %d", created)
	}

	mgr.Stop("/tmp/project")
	if watcher == nil {
		t.Fatalf("expected watcher instance")
	}
	watcher.mu.Lock()
	stopped := watcher.stopped
	watcher.mu.Unlock()
	if stopped != 1 {
		t.Fatalf("expected Stop to be called once, got %d", stopped)
	}
}

func TestManagerStopAll(t *testing.T) {
	mgr := NewManager(Options{})
	watchers := make([]*fakeWatcher, 0, 2)

	mgr.SetFactory(func(root string, opts Options, onChange OnChangeFunc) (Watcher, error) {
		fw := &fakeWatcher{}
		watchers = append(watchers, fw)
		return fw, nil
	})

	if err := mgr.Start("/tmp/project-a", func(ctx context.Context, root string, files []string) error { return nil }); err != nil {
		t.Fatalf("start watcher a: %v", err)
	}
	if err := mgr.Start("/tmp/project-b", func(ctx context.Context, root string, files []string) error { return nil }); err != nil {
		t.Fatalf("start watcher b: %v", err)
	}

	mgr.StopAll()
	for i, fw := range watchers {
		fw.mu.Lock()
		stopped := fw.stopped
		fw.mu.Unlock()
		if stopped != 1 {
			t.Fatalf("watcher %d expected Stop once, got %d", i, stopped)
		}
	}
}

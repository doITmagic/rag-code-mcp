package watch

import (
	"context"
	"errors"
	"sync"
	"testing"
)

type fakeWatcher struct {
	mu        sync.Mutex
	started   int
	stopped   int
	startChan chan struct{}
	release   chan struct{} // when set, Start blocks until it is closed
	startErr  error
}

func (f *fakeWatcher) Start() error {
	f.mu.Lock()
	f.started++
	if f.startChan != nil {
		close(f.startChan)
		f.startChan = nil
	}
	release := f.release
	f.mu.Unlock()
	if release != nil {
		<-release
	}
	return f.startErr
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

// A watcher whose Start fails must not stay registered, so a later Start can
// retry and the engine sees the root as unwatched.
func TestManagerForgetsWatcherWhoseStartFails(t *testing.T) {
	mgr := NewManager(Options{})
	var fail bool
	mgr.SetFactory(func(root string, opts Options, onChange OnChangeFunc) (Watcher, error) {
		if fail {
			return &fakeWatcher{startErr: errors.New("max_user_watches reached")}, nil
		}
		return &fakeWatcher{}, nil
	})
	noop := func(context.Context, string, []string) error { return nil }

	fail = true
	if err := mgr.Start("/tmp/project", noop); err == nil {
		t.Fatal("expected Start to report the watcher failure")
	}
	if mgr.Watching("/tmp/project") {
		t.Fatal("failed watcher stayed registered")
	}
	fail = false
	if err := mgr.Start("/tmp/project", noop); err != nil {
		t.Fatalf("retry: %v", err)
	}
	if !mgr.Watching("/tmp/project") {
		t.Fatal("retried watcher not registered")
	}
}

// A root-gone callback from an old watcher must not stop a replacement
// watcher that was started at the same root after the old one was removed.
func TestManagerRootGoneIgnoresReplacedWatcher(t *testing.T) {
	mgr := NewManager(Options{})
	var gone []func()
	var created []*fakeWatcher
	mgr.SetFactory(func(root string, opts Options, onChange OnChangeFunc) (Watcher, error) {
		w := &fakeWatcher{}
		created = append(created, w)
		gone = append(gone, opts.rootGone)
		return w, nil
	})
	noop := func(context.Context, string, []string) error { return nil }

	if err := mgr.Start("/tmp/project", noop); err != nil {
		t.Fatal(err)
	}
	mgr.Stop("/tmp/project")
	if err := mgr.Start("/tmp/project", noop); err != nil {
		t.Fatal(err)
	}
	gone[0]() // delayed callback from the first watcher
	if !mgr.Watching("/tmp/project") {
		t.Fatal("stale rootGone removed the replacement watcher")
	}
	created[1].mu.Lock()
	stopped := created[1].stopped
	created[1].mu.Unlock()
	if stopped != 0 {
		t.Fatal("stale rootGone stopped the replacement watcher")
	}
	gone[1]()
	if mgr.Watching("/tmp/project") {
		t.Fatal("own rootGone did not remove the watcher")
	}
}

// A Start that fails after its watcher was replaced must not remove the
// replacement: Stop + Start may run while the old watch installation is busy.
func TestManagerFailedStartIgnoresReplacedWatcher(t *testing.T) {
	mgr := NewManager(Options{})
	entered, release := make(chan struct{}), make(chan struct{})
	old := &fakeWatcher{startChan: entered, release: release, startErr: errors.New("max_user_watches reached")}
	repl := &fakeWatcher{}
	next := []*fakeWatcher{old, repl}
	mgr.SetFactory(func(root string, opts Options, onChange OnChangeFunc) (Watcher, error) {
		w := next[0]
		next = next[1:]
		return w, nil
	})
	noop := func(context.Context, string, []string) error { return nil }

	done := make(chan error)
	go func() { done <- mgr.Start("/tmp/project", noop) }()
	<-entered
	mgr.Stop("/tmp/project")
	if err := mgr.Start("/tmp/project", noop); err != nil {
		t.Fatal(err)
	}
	close(release)
	if err := <-done; err == nil {
		t.Fatal("expected the old Start to fail")
	}
	if !mgr.Watching("/tmp/project") {
		t.Fatal("failed old Start removed the replacement watcher")
	}
	repl.mu.Lock()
	stopped := repl.stopped
	repl.mu.Unlock()
	if stopped != 0 {
		t.Fatal("failed old Start stopped the replacement watcher")
	}
}

package engine

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"
	"time"

	"github.com/doITmagic/rag-code-mcp/v3/embed"
	"github.com/doITmagic/rag-code-mcp/v3/index"
	"github.com/doITmagic/rag-code-mcp/v3/store"
)

// Workspace is one indexed root. Root is cleaned and symlink-resolved and
// Collection is "ragcode-" + 16 hex characters of sha1(Root).
type Workspace struct {
	e          *Engine
	Root       string
	Collection string

	// Job state, guarded by e.mu.
	running bool // a runner goroutine exists (it may be waiting for the slot)
	active  bool // the runner holds the slot: a run is in progress
	queued  queueKind
	files   map[string]bool
	deleted map[string]bool
	lastErr error
	retry   *time.Timer // pending lock retry
	fails   int         // consecutive failed runs; sets the retry delay
	status  index.Status
	watch   *watcher
	gone    bool // the root was removed or renamed; the next Workspace call re-watches it

	modelOK atomic.Bool   // the index was made with the embedder's model (fixed for the process)
	reopens atomic.Uint64 // store.SQLite.Reopens last seen; a change queues a resync
}

// Status is the index progress plus the queue state of the workspace.
type Status struct {
	index.Status
	Queued    bool   // a run is waiting
	Running   bool   // a run is in progress (or waiting for the slot)
	LastError string // error of the last run, "" when it succeeded
}

// Status returns the current status. While another process's run holds the
// index lock, and before any run in this process, it comes from
// index.Dir(root)/index_status.json; a recorded phase of a run that never
// ended, with no lock holder, reads as "interrupted".
func (w *Workspace) Status() Status {
	w.e.mu.Lock()
	s := Status{Status: w.status, Queued: w.queued != kindNone || (w.running && !w.active), Running: w.active}
	if w.lastErr != nil {
		s.LastError = w.lastErr.Error()
	}
	w.e.mu.Unlock()
	switch {
	case s.Running:
	case index.Running(w.Root): // another process's run holds the lock
		if disk, ok := index.ReadStatus(w.Root); ok {
			s.Status = disk
		}
		s.Running = true
	case s.StartedAt.IsZero():
		if disk, ok := index.ReadStatus(w.Root); ok {
			s.Status = disk
			if s.EndedAt.IsZero() && s.Phase != "" {
				s.Phase = "interrupted" // no lock holder: that run died
			}
		}
	}
	return s
}

// Index queues a walk of the whole tree (incremental against the state);
// recreate drops the collection first so everything is re-embedded.
func (w *Workspace) Index(recreate bool) {
	kind := kindFull
	if recreate {
		kind = kindRecreate
	}
	w.e.enqueue(w, kind, nil, nil)
}

// IndexFiles queues a run over the given files only.
func (w *Workspace) IndexFiles(changed, deleted []string) {
	if len(changed)+len(deleted) == 0 {
		return
	}
	w.e.enqueue(w, kindFiles, changed, deleted)
}

// Search embeds query once and returns the nearest symbols.
func (w *Workspace) Search(ctx context.Context, query string, f store.Filter, limit int) ([]store.Hit, error) {
	if !w.modelOK.Load() {
		// Vectors of another model, even of the same size, are not comparable;
		// index.Run re-embeds everything on the next run.
		switch m := index.StateModel(w.Root); m {
		case w.e.embed.Model():
			w.modelOK.Store(true)
		case "":
		default:
			if w.e.cfg.AutoIndex {
				w.Index(false)
			}
			return nil, ErrNotIndexed
		}
	}
	vecs, err := w.e.embed.Embed(ctx, []string{embed.QueryText(w.e.embed.Model(), query)})
	if err != nil {
		return nil, err
	}
	if len(vecs) != 1 {
		return nil, fmt.Errorf("engine: embedder returned %d vectors for one query", len(vecs))
	}
	hits, err := w.e.store.Search(ctx, w.Collection, vecs[0], f, limit)
	w.resyncIfReopened()
	return hits, w.mapErr(ctx, err)
}

// Scroll lists symbols matching f without a vector.
func (w *Workspace) Scroll(ctx context.Context, f store.Filter, limit int) ([]store.Hit, error) {
	hits, err := w.e.store.Scroll(ctx, w.Collection, f, limit)
	w.resyncIfReopened()
	return hits, w.mapErr(ctx, err)
}

// resyncIfReopened queues an incremental run when the store reopened the
// collection's file because it was deleted or replaced on disk (an older
// copy restored by hand): the state on disk is then unknown, and the run
// diffs state.json against the files.
func (w *Workspace) resyncIfReopened() {
	r, ok := w.e.store.(interface{ Reopens(string) uint64 })
	if !ok {
		return
	}
	if n := r.Reopens(w.Collection); w.reopens.Swap(n) != n && w.e.cfg.AutoIndex {
		w.Index(false)
	}
}

// mapErr turns a missing collection, or one built with another embedding
// model (vector size differs), into ErrNotIndexed and, with auto_index,
// queues the run that (re)builds it.
func (w *Workspace) mapErr(ctx context.Context, err error) error {
	if err == nil || ctx.Err() != nil {
		return err
	}
	if !errors.Is(err, store.ErrNotFound) && !w.dimChanged(ctx) {
		return err
	}
	if w.e.cfg.AutoIndex {
		w.Index(false)
	}
	return ErrNotIndexed
}

// dimChanged reports whether the collection's vector size differs from the
// embedder's; index.Run then recreates the collection.
func (w *Workspace) dimChanged(ctx context.Context) bool {
	dim, err := w.e.embed.Dimension(ctx)
	if err != nil {
		return false
	}
	size, err := w.e.store.VectorSize(ctx, w.Collection)
	return err == nil && size != dim
}

// Ready reports whether searches can be answered now; when not, the text
// says what to do about it.
func (w *Workspace) Ready(ctx context.Context) (bool, string) {
	if _, err := w.e.embed.Dimension(ctx); err != nil {
		return false, fmt.Sprintf("embedder %s unavailable at %s (%v): start Ollama and pull the model", w.e.embed.Model(), w.e.cfg.OllamaBaseURL, err)
	}
	exists, err := w.e.store.CollectionExists(ctx, w.Collection)
	if err != nil {
		return false, fmt.Sprintf("index store unavailable (%v)", err) // the error names the path
	}
	if !exists {
		return false, ErrNotIndexed.Error()
	}
	return true, ""
}

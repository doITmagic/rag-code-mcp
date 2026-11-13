// Package index runs the indexing pipeline for one workspace root: walk,
// skip rules, parse, workspace-wide relation resolution, embed, upsert.
// Progress and the per-file snapshot live under Dir(root).
package index

import (
	"bytes"
	"cmp"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"
	"unicode/utf8"

	"golang.org/x/sync/semaphore"

	"github.com/doITmagic/rag-code-mcp/v2/embed"
	"github.com/doITmagic/rag-code-mcp/v2/parser"
	"github.com/doITmagic/rag-code-mcp/v2/store"
)

var (
	// ErrLocked means another run holds Dir(root)/index.lock.
	ErrLocked = errors.New("index: another run holds the lock")
	// ErrHeadChanged means git HEAD moved while the run was indexing; the
	// caller should queue a full run.
	ErrHeadChanged = errors.New("index: git HEAD changed during the run")
	// ErrTooManyFiles means a full walk found more indexable files than
	// Options.MaxFiles: nothing was parsed or embedded.
	ErrTooManyFiles = errors.New("index: too many files")
	// ErrNewerFormat means the index was written by a newer RagCode: this
	// process must not re-index it down to its own Format, the newer servers
	// would re-index it back up at every change.
	ErrNewerFormat = errors.New("index: written by a newer RagCode")
)

const (
	embedBatch   = 16
	upsertBatch  = 64
	upsertBytes  = 8 << 20 // Content bytes per upsert, so one batch never holds a huge transaction
	maxEmbedText = 2000
	maxLookups   = 500 // store lookups per run while finding stale referrers
	lookupBatch  = 500 // values per lookup: ids, qualified names or names
)

// clock is time.Now; tests fix it.
var clock = time.Now

// Options configures one run.
type Options struct {
	Root       string
	Collection string
	Store      store.Store
	Embedder   embed.Embedder
	Exclude    []string     // directory names pruned at any depth
	Files      []string     // nil = full walk; otherwise only these files are checked
	Deleted    []string     // files to purge from the index
	MaxFiles   int          // a full walk with more indexable files fails with ErrTooManyFiles; 0 = no limit
	Recreate   bool         // drop the collection under the lock first: everything is re-embedded
	Log        *slog.Logger // nil discards
	Progress   func(Status) // called at most every 500 ms, from the run goroutine
}

// Status is the progress of a run, also persisted to
// Dir(root)/index_status.json.
type Status struct {
	Root      string    `json:"root"`
	Branch    string    `json:"branch,omitempty"`
	StartedAt time.Time `json:"started_at"`
	EndedAt   time.Time `json:"ended_at,omitzero"`
	Total     int       `json:"total"`   // files to (re)index
	Done      int       `json:"done"`    // files fully upserted
	Failed    int       `json:"failed"`  // files that did not parse
	Deleted   int       `json:"deleted"` // files purged
	Error     string    `json:"error,omitempty"`
	Phase     string    `json:"phase"`               // scanning|parsing|embedding|done|failed
	Recreated bool      `json:"recreated,omitempty"` // the collection was dropped by this run
	Warning   string    `json:"warning,omitempty"`   // the run went on with less than asked: git refused the repository, a file was locked
	Busy      []string  `json:"-"`                   // files another process held locked (counted in Failed); the engine retries them
}

func statusPath(root string) string { return filepath.Join(Dir(root), "index_status.json") }

// ReadStatus returns the last persisted status for root.
func ReadStatus(root string) (Status, bool) {
	var s Status
	data, err := readIndexFile(statusPath(root))
	if err != nil || json.Unmarshal(data, &s) != nil {
		return Status{}, false
	}
	return s, true
}

type parsed struct {
	path string
	info fs.FileInfo
	syms []parser.Symbol
	err  error
	hash string // of the bytes parsed, only when info's mtime was racy at the read
}

type run struct {
	Options
	root         string
	status       Status
	state        *State
	fresh        bool                // the collection was (re)created this run: nothing to look up
	named        map[string][]string // ids of the name pass, which callers are resolved against
	prev         Status              // the status of the last run, before this one wrote its own
	touched      bool                // the run deleted or upserted points
	vanished     []string            // files parse found deleted since the scan
	lastProgress time.Time
	lastSave     time.Time
}

// Run indexes one root and returns the final status. The returned error is
// also in Status.Error.
func Run(ctx context.Context, o Options) (Status, error) {
	root, err := filepath.Abs(o.Root)
	if err != nil {
		return Status{Root: o.Root, Phase: "failed", Error: err.Error()}, err
	}
	if o.Log == nil {
		o.Log = slog.New(slog.DiscardHandler)
	}
	r := &run{Options: o, root: root, status: Status{Root: root, StartedAt: time.Now(), Phase: "scanning"}}
	dir := Dir(root)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return r.fail(err)
	}
	unlock, err := lock(filepath.Join(dir, "index.lock"))
	if err != nil {
		return r.fail(err)
	}
	defer unlock()
	r.prev, _ = ReadStatus(root)
	if r.prev.Phase != "done" {
		r.Files = nil // the last run failed, was cut short or crashed (or none ran): the index is partial
	}
	defer func() {
		if r.Files == nil {
			parser.ReleaseParsers()
		}
	}()

	r.state, err = LoadState(root)
	if err != nil {
		o.Log.Warn("index: state.json unreadable, starting fresh", "root", root, "err", err)
	}
	if r.state.Format > Format {
		// Checked before the first status write: the status file belongs to the newer server too.
		return r.fail(fmt.Errorf("%w (index format %d, this server writes %d): restart this server to run the installed version", ErrNewerFormat, r.state.Format, Format))
	}
	var head string
	r.status.Branch, head, r.status.Warning = gitHead(ctx, root)
	if r.status.Warning != "" {
		o.Log.Warn("index: "+r.status.Warning, "root", root)
	}
	r.writeStatus()

	// Probed before the state or collection change: a model the embedder
	// cannot serve (a typo, not pulled) must not drop the index of the old one.
	dim, err := o.Embedder.Dimension(ctx)
	if err != nil {
		return r.finish(ctx, err)
	}
	model := o.Embedder.Model()
	if r.state.Model != "" && r.state.Model != model {
		// Same dimension or not, vectors of two models must not share a collection.
		o.Log.Info("index: embedding model changed, re-embedding everything", "root", root, "from", r.state.Model, "to", model)
		o.Recreate = true
	}
	if len(r.state.Files) > 0 && r.state.Format != Format {
		o.Log.Info("index: index format changed, re-indexing everything", "root", root, "from", r.state.Format, "to", Format)
		o.Recreate = true
	}
	r.state.Model, r.state.Format = model, Format
	if o.Recreate {
		r.touched = true
		// Saved before the drop: after a crash in between, a stale state.json
		// would match every file and incremental runs would never re-embed them.
		r.state = newState(root)
		r.state.Model, r.state.Format = model, Format
		if err := r.state.Save(); err != nil {
			return r.finish(ctx, err)
		}
		if err := o.Store.DeleteCollection(ctx, o.Collection); err != nil && !errors.Is(err, store.ErrNotFound) {
			return r.finish(ctx, err)
		}
		r.status.Recreated = true
	}
	exists, err := o.Store.CollectionExists(ctx, o.Collection)
	if err != nil {
		return r.finish(ctx, err)
	}
	recreated, err := o.Store.EnsureCollection(ctx, o.Collection, dim)
	if err != nil {
		return r.finish(ctx, err)
	}
	r.fresh = !exists || recreated
	hasPoints := false
	for _, e := range r.state.Files {
		if len(e.IDs) > 0 {
			hasPoints = true
			break
		}
	}
	if !r.fresh && (len(r.state.Files) == 0 || hasPoints) {
		// An empty collection is fresh: left so by an interrupted first run,
		// or emptied behind a state that lists points (a failed or crashed
		// recreate, a manual delete), which would otherwise stay empty and done.
		hits, err := o.Store.Scroll(ctx, o.Collection, store.Filter{}, 1)
		switch {
		case err != nil:
		case len(hits) == 0:
			r.fresh = true
		case len(r.state.Files) == 0:
			// Points no state lists (a crash between a recreate's state save and
			// its drop, an unreadable state.json): gone files would stay forever.
			o.Log.Info("index: the store holds points the state does not list, re-indexing everything", "root", root)
			if err := o.Store.DeleteCollection(ctx, o.Collection); err != nil {
				return r.finish(ctx, err)
			}
			if _, err := o.Store.EnsureCollection(ctx, o.Collection, dim); err != nil {
				return r.finish(ctx, err)
			}
			r.fresh, r.status.Recreated = true, true
		}
	}
	if r.fresh {
		// The store is empty: everything is new, and a files-only run would
		// leave a partial index that nothing ever completes.
		r.state = newState(root)
		r.state.Model, r.state.Format = model, Format
		r.Files = nil
	}

	changed, gone, err := r.scan(ctx)
	if errors.Is(err, ErrTooManyFiles) && r.fresh {
		// Not an empty index that answers "no results": the workspace is not indexed.
		_ = o.Store.DeleteCollection(ctx, o.Collection)
	}
	if err != nil {
		return r.finish(ctx, err)
	}
	var goneIDs []string
	purge := func(f string) error {
		// A files run after a branch switch lists paths already purged or never
		// indexed: count only those that had state or points.
		_, ok := r.state.Files[f]
		if !ok {
			hits, err := o.Store.Scroll(ctx, o.Collection, store.Filter{FilePath: f}, 1)
			if err != nil {
				return err
			}
			ok = len(hits) > 0
		}
		if err := o.Store.DeleteByFile(ctx, o.Collection, f); err != nil {
			return err
		}
		delete(r.state.Files, f)
		if ok {
			r.status.Deleted++
			r.touched = true
		}
		return nil
	}
	for _, f := range gone { // purged after staleReferrers, which reads their names
		goneIDs = append(goneIDs, r.state.Files[f].IDs...)
	}
	r.status.Total = len(changed)
	r.status.Phase = "parsing"
	r.tick(true)

	// Pass 1 keeps only the names of every symbol, all that staleReferrers
	// and the resolve index need; pass 2 parses again batch by batch, so the
	// memory of a run does not grow with the size of the workspace.
	files := names(r.parse(ctx, changed))
	for _, f := range r.vanished { // deleted since the scan (a checkout, a rebase): gone, not failed
		gone = append(gone, f)
		goneIDs = append(goneIDs, r.state.Files[f].IDs...)
	}
	r.vanished = nil
	extra := r.staleReferrers(ctx, files, goneIDs, changed, gone)
	for _, f := range gone {
		if err := purge(f); err != nil {
			return r.finish(ctx, err)
		}
	}
	if len(extra) > 0 {
		live := extra[:0]
		ex, known := set(r.Exclude), map[string]bool{}
		for _, f := range extra {
			if info, err := os.Lstat(f); vanishedErr(err) || err == nil && skipExplicit(r.root, ex, known, f, info) {
				// Deleted since its points were stored (a files-only run after a
				// branch switch), or no longer a file the index takes (replaced by
				// a symlink, which may point out of the root): purge it, it is
				// not a parse failure.
				if err := purge(f); err != nil {
					return r.finish(ctx, err)
				}
				continue
			}
			if e, ok := r.state.Files[f]; ok {
				// Until it is stored again: a failed run's retry re-parses it.
				e.ModTime = 0
				r.state.Files[f] = e
			}
			live = append(live, f)
		}
		r.status.Total += len(live)
		files = append(files, names(r.parse(ctx, live))...)
		for _, f := range r.vanished {
			if err := purge(f); err != nil {
				return r.finish(ctx, err)
			}
		}
	}
	r.vanished = nil // a file gone in pass 2 keeps its pass-1 ids: see record
	if ctx.Err() != nil {
		return r.finish(ctx, ctx.Err())
	}
	r.status.Phase = "embedding"
	idx := newNameIndex(files)
	r.named = make(map[string][]string, len(files))
	for _, p := range files {
		r.named[p.path] = symIDs(p)
	}
	for len(files) > 0 {
		n, size := 0, int64(0)
		for n < len(files) && (n == 0 || size < batchBytes) {
			size += files[n].info.Size()
			n++
		}
		paths := make([]string, n)
		for i := range paths {
			paths[i] = files[i].path
		}
		files = files[n:]
		batch := r.parse(ctx, paths)
		if ctx.Err() != nil {
			return r.finish(ctx, ctx.Err())
		}
		for _, f := range paths { // gone since the name pass: see record
			if !slices.ContainsFunc(batch, func(p parsed) bool { return p.path == f }) && len(r.named[f]) > 0 {
				r.state.Files[f] = Entry{IDs: append(slices.Clone(r.state.Files[f].IDs), r.named[f]...)}
			}
		}
		r.resolve(ctx, idx, batch)
		r.touched = true
		if err := r.embedUpsert(ctx, batch); err != nil {
			return r.finish(ctx, err)
		}
	}
	if _, now, _ := gitHead(ctx, root); now != head {
		return r.finish(ctx, ErrHeadChanged)
	}
	return r.finish(ctx, nil)
}

// fail ends a run that never got far enough to own the status file.
func (r *run) fail(err error) (Status, error) {
	r.status.Phase, r.status.Error, r.status.EndedAt = "failed", err.Error(), time.Now()
	return r.status, err
}

func (r *run) finish(ctx context.Context, err error) (Status, error) {
	r.status.EndedAt = time.Now()
	r.status.Phase = "done"
	if err != nil && ctx.Err() != nil {
		err = ctx.Err() // a store call cut short reports its own error, not ctx.Err()
	}
	if err != nil {
		r.status.Phase, r.status.Error = "failed", err.Error()
		if errors.Is(err, context.Canceled) {
			r.status.Phase = "interrupted" // stopped by shutdown; Resume restarts it
		}
	}
	if n := len(r.status.Busy); n > 0 {
		// A files run leaves the previous points of a file it could not read.
		w := fmt.Sprintf("%d file(s) locked by another process were not read (%s); their symbols may be out of date until they are indexed again", n, r.status.Busy[0])
		r.status.Warning = strings.TrimPrefix(r.status.Warning+"; "+w, "; ")
	}
	r.tick(true)
	// Cancelled before it changed the index (a catch-up cut short by the
	// session's end), or finished with nothing to do (a run queued behind a
	// full one): the index is still the one the last run finished, so are
	// its counters on disk. The caller still gets this run's own status.
	// A failed run that changed nothing (Ollama down) also keeps them, under
	// its own error. A run stopped by a HEAD move changed the index like a
	// finished one, so its counters are the last real ones.
	last := r.prev.Phase == "done" || r.prev.Phase == "failed"
	unchanged := last && !r.touched && !r.fresh && !r.status.Recreated
	idle := r.status.Phase == "done" && r.status.Failed == 0
	failed := r.status.Phase == "failed"
	if kept := r.prev; unchanged && (r.status.Phase == "interrupted" || idle || failed) {
		switch {
		case idle: // the index matches this run's HEAD: complete, on its branch
			kept.Phase, kept.Error, kept.Branch, kept.Warning = "done", "", r.status.Branch, r.status.Warning
		case failed:
			kept.Phase, kept.Error, kept.Branch, kept.Warning = "failed", r.status.Error, r.status.Branch, r.status.Warning
		}
		data, _ := json.Marshal(kept)
		if err := WriteAtomic(statusPath(r.root), data); err != nil {
			r.Log.Warn("index: cannot write status", "root", r.root, "err", err)
		}
	}
	return r.status, err
}

// tick reports progress (every 500 ms) and persists state and status
// (every 2 s); force does both now.
func (r *run) tick(force bool) {
	now := time.Now()
	if force || now.Sub(r.lastProgress) >= 500*time.Millisecond {
		r.lastProgress = now
		if r.Progress != nil {
			r.Progress(r.status)
		}
	}
	if force || now.Sub(r.lastSave) >= 2*time.Second {
		r.lastSave = now
		if r.state != nil {
			if err := r.state.Save(); err != nil {
				r.Log.Warn("index: cannot save state", "root", r.root, "err", err)
			}
		}
		r.writeStatus()
	}
}

func (r *run) writeStatus() {
	data, _ := json.Marshal(r.status)
	if err := WriteAtomic(statusPath(r.root), data); err != nil {
		r.Log.Warn("index: cannot write status", "root", r.root, "err", err)
	}
}

// gitHead returns the branch name and commit of HEAD, "" when root is not a
// repository or git does not answer in 5 s, and why git refused the
// repository ("" when it did not). A refused repository (owned by another
// user: safe.directory) also fails every check-ignore of GitIgnored, so no
// .gitignore rule applies; git's check is never overridden, since repository
// config can run programs.
// shortcut: only the root's repository is checked, check nested repositories when one owned by another user shows up.
func gitHead(ctx context.Context, root string) (branch, head, refused string) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", "-C", root, "rev-parse", "HEAD", "--abbrev-ref", "HEAD")
	cmd.Env = append(os.Environ(), "LC_ALL=C") // the refusal is recognised by git's English message
	out, err := cmd.Output()
	if err != nil {
		if ee := (*exec.ExitError)(nil); errors.As(err, &ee) && bytes.Contains(ee.Stderr, []byte("dubious ownership")) {
			return "", "", "git refused the repository (dubious ownership): .gitignore rules are not applied"
		}
		return "", "", ""
	}
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	if len(lines) != 2 {
		return "", "", ""
	}
	return lines[1], lines[0], ""
}

// scan returns the files to (re)index and the files to purge, both sorted.
func (r *run) scan(ctx context.Context) (changed, gone []string, err error) {
	var files []string
	if r.Files == nil {
		unchanged := func(f string, info fs.FileInfo) bool {
			e, ok := r.state.Files[f]
			// A racy entry (with a hash) may hold other bytes: the binary check opens it.
			return ok && e.ModTime == info.ModTime().UnixNano() && e.Size == info.Size() && e.Hash == ""
		}
		if files, err = walk(r.root, r.Exclude, unchanged); err != nil {
			return nil, nil, err
		}
		if r.MaxFiles > 0 && len(files) > r.MaxFiles {
			return nil, nil, fmt.Errorf("%w: %d indexable files under %s, more than max_files (%d). Nothing was indexed. Indexing this many files takes long: ask the user whether this is the directory they mean. A smaller project directory can be indexed as it is; to index this one, the user raises max_files in config.yaml (0 = no limit) and restarts the server",
				ErrTooManyFiles, len(files), r.root, r.MaxFiles)
		}
	} else {
		for _, f := range r.Files {
			files = append(files, r.abs(f))
		}
	}
	ex := set(r.Exclude)
	known := map[string]bool{} // IsLinkedWorktree per directory
	var ign map[string]bool    // Walk already dropped git-ignored files
	if r.Files != nil {
		ign = GitIgnored(r.root, files)
	}
	goneSet := map[string]bool{}
	seen := make(map[string]bool, len(files))
	for _, f := range files {
		if err := ctx.Err(); err != nil {
			return nil, nil, err
		}
		info, err := os.Lstat(f) // a symlink fails Skip, as in Walk
		if err != nil || ign[f] || (r.Files != nil && skipExplicit(r.root, ex, known, f, info)) {
			if _, ok := r.state.Files[f]; ok {
				goneSet[f] = true
			}
			continue
		}
		seen[f] = true
		e, ok := r.state.Files[f]
		switch {
		case !ok || e.ModTime != info.ModTime().UnixNano() || e.Size != info.Size():
			changed = append(changed, f)
		case e.Hash != "" && info.Mode().IsRegular():
			// Indexed within the racy window of its mtime: the same stat may
			// hide other bytes, compare them. Once the mtime is safely old, a
			// later write must change it, so the hash goes.
			at := clock()
			if src, err := os.ReadFile(f); err != nil || contentHash(src) != e.Hash {
				changed = append(changed, f)
			} else if !parser.Racy(info.ModTime(), at) {
				e.Hash = ""
				r.state.Files[f] = e
			}
		}
	}
	for _, f := range r.Deleted {
		// Purge even a path the state never recorded: an interrupted run
		// may have left its points in the store.
		if f = r.abs(f); !seen[f] {
			goneSet[f] = true
		}
	}
	if r.Files == nil {
		for f := range r.state.Files {
			if !seen[f] {
				goneSet[f] = true
			}
		}
	}
	for f := range goneSet {
		gone = append(gone, f)
	}
	sort.Strings(changed)
	sort.Strings(gone)
	return changed, gone, nil
}

// skipExplicit applies the Walk rules to a file named in Options.Files: it
// must lie under root, outside hidden and excluded directories, linked
// worktrees and RagCode's own home, and pass Skip.
func skipExplicit(root string, ex, known map[string]bool, f string, info fs.FileInfo) bool {
	rel, err := filepath.Rel(root, f)
	if err != nil || strings.HasPrefix(rel, "..") || LinkedWorktreeOf(root, f, known) != "" || InOwnHome(f, OwnHome()) {
		return true
	}
	at := root
	for _, d := range strings.Split(filepath.Dir(rel), string(filepath.Separator)) {
		if d == "." {
			continue
		}
		if strings.HasPrefix(d, ".") || ex[d] {
			return true
		}
		// Walk never enters a symlinked directory: it may lead out of the root.
		at = filepath.Join(at, d)
		if fi, err := os.Lstat(at); err != nil || !fi.IsDir() {
			return true
		}
	}
	var head []byte
	if info.Mode().IsRegular() { // never open a fifo or device
		head = readHead(f)
	}
	return Skip(f, info, head) != ""
}

func (r *run) abs(f string) string {
	if !filepath.IsAbs(f) {
		f = filepath.Join(r.root, f)
	}
	return filepath.Clean(f)
}

// parse analyzes files on parseWorkers workers, with at most parseBudget
// source bytes being parsed at once, and returns the results in the input
// order, minus the files that failed (counted in Failed).
func (r *run) parse(ctx context.Context, files []string) []parsed {
	budget := semaphore.NewWeighted(parseBudget)
	jobs := make(chan string)
	results := make(chan parsed)
	var wg sync.WaitGroup
	for range parseWorkers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for f := range jobs {
				results <- parseOne(r.root, f, budget)
			}
		}()
	}
	go func() {
		defer close(results)
		defer wg.Wait()
		defer close(jobs)
		for _, f := range files {
			select {
			case jobs <- f:
			case <-ctx.Done():
				return
			}
		}
	}()
	byPath := make(map[string]parsed, len(files))
	for p := range results {
		if vanishedErr(p.err) {
			// Deleted by git mid-run: the caller purges it, it is not a failure.
			r.Log.Debug("index: file vanished during the run", "file", p.path)
			r.vanished = append(r.vanished, p.path)
			r.status.Total--
		} else if p.err != nil {
			r.status.Failed++
			if isBusy(p.err) {
				r.status.Busy = append(r.status.Busy, p.path)
			}
			r.Log.Warn("index: parse failed", "file", p.path, "err", p.err)
		} else {
			byPath[p.path] = p
		}
		r.tick(false)
	}
	out := make([]parsed, 0, len(byPath))
	for _, f := range files {
		if p, ok := byPath[f]; ok {
			out = append(out, p)
		}
	}
	return out
}

func parseOne(root, path string, budget *semaphore.Weighted) (p parsed) {
	p.path = path
	defer func() {
		if v := recover(); v != nil {
			p.err = fmt.Errorf("panic: %v", v)
		}
	}()
	// Stat before reading: a write between the two leaves a newer mtime on
	// disk than in the state, so the next run picks the file up again.
	// Lstat: a file replaced by a symlink since the scan is not followed.
	if p.info, p.err = os.Lstat(path); p.err != nil {
		return p
	}
	if !p.info.Mode().IsRegular() {
		p.err = fmt.Errorf("%s is no longer a regular file", path)
		return p
	}
	if p.info.Size() > maxFileSize {
		return p // grew past the cap since the walk: indexed as empty
	}
	at := clock()
	src, err := os.ReadFile(path)
	if err != nil {
		p.err = err
		return p
	}
	if parser.Racy(p.info.ModTime(), at) {
		p.hash = contentHash(src)
	}
	// Never blocks for good: every holder releases after one file.
	w := min(max(int64(len(src)), 1), parseBudget)
	_ = budget.Acquire(context.Background(), w)
	defer budget.Release(w)
	p.syms, p.err = parser.Analyze(parser.File{Path: path, Root: root, Source: src})
	return p
}

// staleReferrers lists the files outside the run whose relations target an
// id the run removes (a symbol of a gone file, or one that moved or changed
// in a re-parsed file): ids hash the start line, so those target ids would go
// stale. It also lists the referrers that named a symbol the run adds before
// it existed: they hold no target id, or a unique one the new symbol makes
// ambiguous. Re-indexing the files re-resolves them.
func (r *run) staleReferrers(ctx context.Context, files []parsed, vanished []string, inRun ...[]string) []string {
	if r.fresh {
		return nil
	}
	oldRoutes := slices.Clone(vanished) // where a route may have been: deleted files, then the run's PHP files
	lookups := 0
	scroll := func(f store.Filter) ([]store.Hit, bool) {
		if lookups >= maxLookups {
			if lookups == maxLookups {
				r.Log.Warn("index: referrer lookup cap reached, remaining callers keep stale target ids", "cap", maxLookups)
				lookups++
			}
			return nil, false
		}
		lookups++
		hits, err := r.Store.Scroll(ctx, r.Collection, f, store.MaxScroll)
		if err != nil {
			if ctx.Err() == nil { // a cancelled run (server stopping) resumes next start
				r.Log.Warn("index: referrer lookup failed", "err", err)
			}
			lookups = maxLookups + 1
			return nil, false
		}
		return hits, true
	}
	// query looks vals up lookupBatch at a time, halving a batch whose hits
	// reach MaxScroll so that no match is cut off. False: stop looking.
	var query func(vals []string, mk func([]string) store.Filter, use func(store.Hit)) bool
	query = func(vals []string, mk func([]string) store.Filter, use func(store.Hit)) bool {
		for len(vals) > 0 {
			n := min(len(vals), lookupBatch)
			hits, ok := scroll(mk(vals[:n]))
			switch {
			case !ok:
				return false
			case len(hits) >= store.MaxScroll && n > 1:
				if !query(vals[:n/2], mk, use) || !query(vals[n/2:n], mk, use) {
					return false
				}
			default:
				if len(hits) >= store.MaxScroll {
					// shortcut: one key with more hits than a Scroll returns keeps the rest stale; page the store when a repo needs it
					r.Log.Warn("index: more referrers of one symbol than one lookup returns, the rest keep stale target ids until a recreate", "cap", store.MaxScroll)
				}
				for _, h := range hits {
					use(h)
				}
			}
			vals = vals[n:]
		}
		return true
	}
	now := map[string]bool{}
	var oldOfGained []string  // old ids of the files that gained an id
	var twins []string        // kept ids of twins: the chosen one may be another now
	var oldOfClasses []string // old ids of the files that hold a class now
	var owners []string       // names an owner-qualified target may start with, whose resolution the run changes
	addOwner := func(s parser.Symbol) {
		n := parser.Normalize(s.QualifiedName)
		// A function owns members too (a JS constructor function, Repo.save()).
		// shortcut: a function or method adds only its full qualified name, a
		// third of the lookups of every suffix; add its suffixes if callers
		// name a function owner by a short name.
		if s.Kind == parser.KindFunction || s.Kind == parser.KindMethod {
			owners = append(owners, n)
			return
		}
		// every suffix: a target may name its owner by a short name
		for i := 0; i < len(n); i++ {
			if i == 0 || n[i-1] == '.' {
				owners = append(owners, n[i:])
			}
		}
	}
	for _, p := range files {
		old := r.state.Files[p.path].IDs // the old ids until record()
		vanished = append(vanished, old...)
		gained, count := false, map[string]int{}
		for _, s := range p.syms {
			now[s.ID] = true
			gained = gained || !slices.Contains(old, s.ID)
			count[s.QualifiedName]++
		}
		for _, s := range p.syms {
			if count[s.QualifiedName] > 1 && slices.Contains(old, s.ID) {
				twins = append(twins, s.ID)
			}
		}
		if slices.ContainsFunc(p.syms, func(s parser.Symbol) bool { return s.Kind == parser.KindClass || s.Kind == parser.KindInterface }) {
			oldOfClasses = append(oldOfClasses, old...)
		}
		if gained {
			oldOfGained = append(oldOfGained, old...)
		}
	}
	had := map[[2]string]int{} // {file, qualified name} of the old points, not yet purged: how many
	query(oldOfGained, func(v []string) store.Filter { return store.Filter{IDs: v} }, func(h store.Hit) {
		had[[2]string{h.Symbol.FilePath, h.Symbol.QualifiedName}]++
	})
	byLang := map[string][]parser.Symbol{} // the symbols the run adds
	for _, p := range files {
		old := r.state.Files[p.path].IDs
		for _, s := range p.syms {
			if slices.Contains(old, s.ID) {
				had[[2]string{p.path, s.QualifiedName}]--
			}
		}
		for _, s := range p.syms {
			// a symbol that only moved keeps its name: not added; one more
			// twin than before (overload, accessor) is, it may become the target
			if k := [2]string{p.path, s.QualifiedName}; !slices.Contains(old, s.ID) {
				if had[k] > 0 {
					had[k]--
				} else {
					byLang[s.Language] = append(byLang[s.Language], s)
					addOwner(s)
				}
			}
		}
	}
	skip := map[string]bool{}
	for _, l := range inRun {
		for _, f := range l {
			skip[f] = true
		}
	}
	var out []string
	add := func(keep func(parser.Symbol) bool) func(store.Hit) {
		return func(h store.Hit) {
			if f := h.Symbol.FilePath; !skip[f] && keep(h.Symbol) {
				skip[f] = true
				out = append(out, f)
			}
		}
	}
	all := func(parser.Symbol) bool { return true }
	var gone []string
	for _, id := range vanished {
		if !now[id] {
			gone = append(gone, id)
		}
	}
	ok := query(append(twins, gone...), func(v []string) store.Filter { return store.Filter{TargetIDs: v} }, add(all))
	// A candidate the run removes for good can turn an ambiguous call unique.
	// Kept means still in its own file under its qualified name (it only
	// moved there): the same name in another file of the run, or on another
	// owner in the same file (Alpha.Close for a deleted Beta.Close), does not
	// replace a deleted candidate.
	kept := map[[4]string]bool{}
	for _, p := range files {
		for _, s := range p.syms {
			kept[[4]string{p.path, s.Language, s.Name, parser.Normalize(s.QualifiedName)}] = true
		}
	}
	// callerOf reports whether h, which names a symbol of language lang,
	// may resolve to it: same language, or html and docs, which link into
	// code of any language (resolve's keep)
	callerOf := func(lang string, h parser.Symbol) bool {
		return h.Language == lang || h.Language == "html" || h.Language == "docs"
	}
	lost := map[string]map[string]bool{} // language -> names
	ok = ok && query(gone, func(v []string) store.Filter { return store.Filter{IDs: v} }, func(h store.Hit) {
		if l, n := h.Symbol.Language, h.Symbol.Name; !kept[[4]string{h.Symbol.FilePath, l, n, parser.Normalize(h.Symbol.QualifiedName)}] {
			if lost[l] == nil {
				lost[l] = map[string]bool{}
			}
			lost[l][n] = true
			addOwner(h.Symbol)
		}
	})
	for lang, names := range lost {
		if !ok {
			break
		}
		ambiguous := func(h parser.Symbol) bool {
			return callerOf(lang, h) && slices.ContainsFunc(h.Relations, func(rel parser.Relation) bool {
				return names[rel.TargetName] && rel.Resolution == parser.ResolutionAmbiguous
			})
		}
		ok = query(slices.Collect(maps.Keys(names)), func(v []string) store.Filter { return store.Filter{TargetNames: v} }, add(ambiguous))
	}
	for lang, syms := range byLang {
		if !ok {
			break
		}
		qns, names := make([]string, len(syms)), map[string]bool{}
		var nameList, mods []string
		for i, s := range syms {
			qns[i] = s.QualifiedName
			if !names[s.Name] {
				names[s.Name] = true
				nameList = append(nameList, s.Name)
			}
			if s.Kind == parser.KindClass && s.Owner == "" && s.Module != "" && !slices.Contains(mods, s.Module) {
				mods = append(mods, s.Module)
			}
		}
		// extends "res://x.gd" (GDScript) and a scene node's attached script
		// name a whole module: resolve takes its one top-level class
		script := func(h parser.Symbol) bool {
			return slices.ContainsFunc(h.Relations, func(rel parser.Relation) bool {
				return slices.Contains(mods, rel.TargetQualifiedName) && (rel.Kind == parser.RelExtends || rel.Kind == parser.RelUsesType && h.Language == "gdscript")
			})
		}
		// a relation resolved by name, or not at all, or by a qualified suffix
		// one of the added symbols also has; an ambiguous one only with an
		// owner (self.m()), which may now find m on the owner's base
		guessed := func(h parser.Symbol) bool {
			return callerOf(lang, h) && slices.ContainsFunc(h.Relations, func(rel parser.Relation) bool {
				if !names[rel.TargetName] {
					return false
				}
				if rel.Resolution == parser.ResolutionAmbiguous {
					return strings.Contains(parser.Normalize(rel.TargetQualifiedName), ".")
				}
				if rel.Resolution != parser.ResolutionExact {
					return true
				}
				key := parser.Normalize(rel.TargetQualifiedName)
				return key != "" && slices.ContainsFunc(syms, func(s parser.Symbol) bool {
					return parser.Normalize(s.QualifiedName) != key && parser.MatchQualified(s.QualifiedName, key)
				})
			})
		}
		ok = query(qns, func(v []string) store.Filter { return store.Filter{TargetQualifiedNames: v} }, add(all)) &&
			query(mods, func(v []string) store.Filter { return store.Filter{TargetQualifiedNames: v} }, add(script)) &&
			query(nameList, func(v []string) store.Filter { return store.Filter{TargetNames: v} }, add(guessed))
	}
	// An html inline handler (onclick="f()") links only to a browser global:
	// a kept JavaScript function that became one, or stopped being one,
	// changes the handlers that name it.
	global := map[string]parser.Symbol{} // kept id -> the function now
	for _, p := range files {
		for _, s := range p.syms {
			if s.Language == "javascript" && s.Kind == parser.KindFunction && slices.Contains(r.state.Files[p.path].IDs, s.ID) {
				global[s.ID] = s
			}
		}
	}
	var flipped []string
	ok = ok && query(slices.Collect(maps.Keys(global)), func(v []string) store.Filter { return store.Filter{IDs: v} }, func(h store.Hit) {
		if s := global[h.Symbol.ID]; h.Symbol.Metadata["global"] != s.Metadata["global"] && !slices.Contains(flipped, s.Name) {
			flipped = append(flipped, s.Name)
		}
	})
	ok = ok && query(flipped, func(v []string) store.Filter { return store.Filter{TargetNames: v} }, add(func(h parser.Symbol) bool { return h.Language == "html" }))
	// route('x') links to the Laravel routes named x (metadata name): a name
	// whose route ids the run changes (a name given, dropped, a route moved)
	// changes the callers that name it.
	routeIDs := map[string][]string{} // name -> ids now, then the old ones removed
	for _, p := range files {
		if strings.HasSuffix(p.path, ".php") {
			oldRoutes = append(oldRoutes, r.state.Files[p.path].IDs...)
		}
		for _, s := range p.syms {
			if n := s.Metadata["name"]; s.Kind == parser.KindRoute && n != "" {
				routeIDs[n] = append(routeIDs[n], s.ID)
			}
		}
	}
	renamed := map[string]bool{}
	ok = ok && query(oldRoutes, func(v []string) store.Filter { return store.Filter{IDs: v, Kinds: []parser.Kind{parser.KindRoute}} }, func(h store.Hit) {
		n := h.Symbol.Metadata["name"]
		if i := slices.Index(routeIDs[n], h.Symbol.ID); i >= 0 {
			routeIDs[n] = slices.Delete(routeIDs[n], i, i+1)
		} else if n != "" {
			renamed[n] = true
		}
	})
	for n, ids := range routeIDs {
		if len(ids) > 0 {
			renamed[n] = true
		}
	}
	ok = ok && query(slices.Collect(maps.Keys(renamed)), func(v []string) store.Filter { return store.Filter{TargetNames: v} }, add(func(h parser.Symbol) bool {
		return slices.ContainsFunc(h.Relations, func(rel parser.Relation) bool {
			n, ok := strings.CutPrefix(rel.TargetQualifiedName, "route:")
			return ok && renamed[n]
		})
	}))
	// X.m() on an owner X resolves through X: by name when X exists without
	// m, through X's bases when it inherits m. A class the run adds, removes
	// or gives other bases changes that for callers that name it as owner.
	oldBases := map[[2]string]string{}
	ok = ok && query(oldOfClasses, func(v []string) store.Filter {
		return store.Filter{IDs: v, Kinds: []parser.Kind{parser.KindClass, parser.KindInterface}}
	}, func(h store.Hit) {
		oldBases[[2]string{h.Symbol.FilePath, h.Symbol.QualifiedName}] = basesOf(h.Symbol)
	})
	for _, p := range files {
		for _, s := range p.syms {
			if was, had := oldBases[[2]string{p.path, s.QualifiedName}]; ok && had && was != basesOf(s) {
				addOwner(s)
			}
		}
	}
	slices.Sort(owners)
	_ = ok && query(slices.Compact(owners), func(v []string) store.Filter { return store.Filter{TargetOwners: v} }, add(all))
	sort.Strings(out)
	return out
}

// basesOf is what a class extends, implements or uses, as one comparable
// string: the relations names() keeps.
func basesOf(s parser.Symbol) string {
	var b []string
	for _, r := range s.Relations {
		if (r.TargetQualifiedName != "" || r.TargetName != "") && (r.Kind == parser.RelExtends || r.Kind == parser.RelImplements || r.Kind == parser.RelUsesType) {
			b = append(b, string(r.Kind)+" "+cmp.Or(r.TargetQualifiedName, r.TargetName))
		}
	}
	slices.Sort(b)
	return strings.Join(slices.Compact(b), "\n")
}

// names drops everything but the names (and kind, module, owner) of each
// symbol, its place, plus a class's bases: what staleReferrers and
// newNameIndex read.
func names(files []parsed) []parsed {
	for i := range files {
		for j, s := range files[i].syms {
			n := parser.Symbol{ID: s.ID, QualifiedName: s.QualifiedName, Name: s.Name, Language: s.Language, Kind: s.Kind, Module: s.Module, Owner: s.Owner,
				FilePath: s.FilePath, StartLine: s.StartLine, EndLine: s.EndLine, Metadata: s.Metadata}
			if s.Kind == parser.KindClass || s.Kind == parser.KindInterface {
				for _, r := range s.Relations {
					if (r.TargetQualifiedName != "" || r.TargetName != "") && (r.Kind == parser.RelExtends || r.Kind == parser.RelImplements || r.Kind == parser.RelUsesType) {
						n.Relations = append(n.Relations, parser.Relation{Kind: r.Kind, TargetName: r.TargetName, TargetQualifiedName: r.TargetQualifiedName})
					}
				}
			}
			files[i].syms[j] = n
		}
	}
	return files
}

// nameIndex maps the names of every symbol of the run to their ids.
type nameIndex struct {
	byQN      map[string][]string
	bySuffix  map[string][]string    // every dot-boundary suffix of a qualified name
	byName    map[[2]string][]string // language, name: a call never crosses languages
	byModule  map[string][]string    // top-level classes per module
	byModName map[[2]string][]string // module, name of top-level symbols
	lang      map[string]string      // symbol id -> language
	kind      map[string]parser.Kind // symbol id -> kind, for fit
	place     map[string]place       // symbol id -> where it is, for twins
	global    map[string]bool        // symbol id -> a browser global an inline html handler reaches (metadata global)
	routes    map[string][]string    // Laravel route name (metadata name) -> route ids of the run, for route('x')
	route     map[string]string      // route id -> its Laravel route name, store hits included
	bases     map[string][]string    // class id -> what it extends, implements or uses (traits): qualified name, else bare name
	changed   map[string]bool        // files of the run: their stored points are stale
}

// place groups twins: symbols sharing file and qualified name (overloads,
// getter/setter pairs, merged declarations) are one target. A function or
// method and another kind of one name (a PHP property and its accessor
// method) are not twins.
type place struct {
	file, qn   string
	fn         bool
	start, end int
}

func placeOf(s parser.Symbol) place {
	return place{s.FilePath, parser.Normalize(s.QualifiedName), s.Kind == parser.KindFunction || s.Kind == parser.KindMethod, s.StartLine, s.EndLine}
}

// addBases records what class or interface s extends, implements or uses.
func (idx nameIndex) addBases(s parser.Symbol) {
	if s.Kind != parser.KindClass && s.Kind != parser.KindInterface {
		return
	}
	for _, r := range s.Relations {
		// Go promotes only an embedded type's methods (extends), not a named field's.
		if b := cmp.Or(r.TargetQualifiedName, r.TargetName); b != "" && (r.Kind == parser.RelExtends || s.Language != "go" && (r.Kind == parser.RelImplements || r.Kind == parser.RelUsesType)) {
			idx.bases[s.ID] = append(idx.bases[s.ID], b)
		}
	}
}

func newNameIndex(files []parsed) nameIndex {
	idx := nameIndex{byQN: map[string][]string{}, bySuffix: map[string][]string{}, byName: map[[2]string][]string{}, byModule: map[string][]string{},
		byModName: map[[2]string][]string{}, lang: map[string]string{}, kind: map[string]parser.Kind{}, place: map[string]place{}, global: map[string]bool{}, routes: map[string][]string{}, route: map[string]string{}, bases: map[string][]string{}, changed: make(map[string]bool, len(files))}
	for i := range files {
		idx.changed[files[i].path] = true
		for _, s := range files[i].syms {
			qn := parser.Normalize(s.QualifiedName)
			idx.lang[s.ID] = s.Language
			idx.kind[s.ID] = s.Kind
			idx.place[s.ID] = placeOf(s)
			idx.global[s.ID] = s.Metadata["global"] == "true"
			if n := s.Metadata["name"]; s.Kind == parser.KindRoute && n != "" {
				idx.routes[n] = append(idx.routes[n], s.ID)
			}
			idx.addBases(s)
			idx.byQN[qn] = append(idx.byQN[qn], s.ID)
			for j := 0; j < len(qn); j++ {
				if j == 0 || qn[j-1] == '.' {
					idx.bySuffix[qn[j:]] = append(idx.bySuffix[qn[j:]], s.ID)
				}
			}
			idx.byName[[2]string{s.Language, s.Name}] = append(idx.byName[[2]string{s.Language, s.Name}], s.ID)
			if s.Kind == parser.KindClass && s.Owner == "" {
				idx.byModule[s.Module] = append(idx.byModule[s.Module], s.ID)
			}
			if s.Owner == "" {
				idx.byModName[[2]string{s.Module, s.Name}] = append(idx.byModName[[2]string{s.Module, s.Name}], s.ID)
			}
		}
	}
	return idx
}

// resolve fills Relation.TargetID and Resolution over the run's symbols plus,
// when the store already holds the rest of the workspace, store lookups for
// the targets the run does not define.
func (r *run) resolve(ctx context.Context, idx nameIndex, files []parsed) {
	byQN, bySuffix, byName, byModule, langOf, kindOf, changed := idx.byQN, idx.bySuffix, idx.byName, idx.byModule, idx.lang, idx.kind, idx.changed
	// No lookup cap: a target left unresolved here stays so until its caller
	// file changes. One store error stops the lookups for the run.
	failed := false
	cache := map[string][]string{}
	fromStore := func(key string, f store.Filter, limit int, match func(parser.Symbol) bool) []string {
		if r.fresh || failed {
			return nil
		}
		if ids, ok := cache[key]; ok {
			return ids
		}
		hits, err := r.Store.Scroll(ctx, r.Collection, f, limit)
		if err != nil {
			if ctx.Err() == nil { // a cancelled run (server stopping) resumes next start
				r.Log.Warn("index: relation lookup failed, remaining targets stay unresolved", "err", err)
			}
			failed = true
			return nil
		}
		var ids []string
		for _, h := range hits {
			// Points of files in this run are stale until their upsert.
			if !changed[h.Symbol.FilePath] && match(h.Symbol) {
				ids = append(ids, h.Symbol.ID)
				langOf[h.Symbol.ID] = h.Symbol.Language
				kindOf[h.Symbol.ID] = h.Symbol.Kind
				idx.place[h.Symbol.ID] = placeOf(h.Symbol)
				idx.global[h.Symbol.ID] = h.Symbol.Metadata["global"] == "true"
				idx.route[h.Symbol.ID] = h.Symbol.Metadata["name"]
				if _, ok := idx.bases[h.Symbol.ID]; !ok { // an unchanged owner's bases, as a full run has them
					idx.addBases(h.Symbol)
				}
			}
		}
		cache[key] = ids
		return ids
	}
	// keep drops the candidates of another language: code never calls across
	// languages, while docs and html links point into code on purpose.
	keep := func(lang string, ids []string) []string {
		out := make([]string, 0, len(ids))
		for _, id := range ids {
			if lang == "docs" || lang == "html" || langOf[id] == lang {
				out = append(out, id)
			}
		}
		return out
	}
	// collapse keeps one id per twin group: the widest, then the first. A
	// group lies in one file, so a full and a files run pick the same one.
	collapse := func(ids []string) []string {
		best := map[place]string{}
		out := ids[:0:0]
		for _, id := range ids {
			p := idx.place[id]
			k := place{file: p.file, qn: p.qn, fn: p.fn}
			b, ok := best[k]
			if !ok {
				out = append(out, id)
			}
			if q := idx.place[b]; !ok || p.end-p.start > q.end-q.start || p.end-p.start == q.end-q.start && (p.start < q.start || p.start == q.start && id < b) {
				best[k] = id
			}
		}
		for i, id := range out {
			p := idx.place[id]
			out[i] = best[place{file: p.file, qn: p.qn, fn: p.fn}]
		}
		return out
	}
	// lookup takes run and store candidates together, so a files run sees
	// what a full run sees; two in the run already make it ambiguous.
	lookup := func(lang string, run []string, key string, f store.Filter, match func(parser.Symbol) bool) []string {
		ids := keep(lang, run)
		if len(collapse(ids)) < 2 {
			ids = append(ids, keep(lang, fromStore(key, f, 20, match))...)
		}
		return ids
	}
	exact := func(lang, tqn string) []string {
		key := parser.Normalize(tqn)
		return lookup(lang, byQN[key], "q:"+key, store.Filter{QualifiedName: tqn}, func(s parser.Symbol) bool {
			return parser.Normalize(s.QualifiedName) == key
		})
	}
	// Python imports are relative to a source root (src/), never to a
	// directory inside a package: json.dumps is not src.flask.json.dumps.
	pkgDir := map[string]bool{} // root-relative dotted path -> it is a Python package
	rootCut := func(lang, qn, key string) bool {
		if lang != "python" || len(qn) <= len(key) {
			return true
		}
		cut := qn[:len(qn)-len(key)-1]
		is, ok := pkgDir[cut]
		if !ok {
			_, err := os.Stat(filepath.Join(r.root, filepath.FromSlash(strings.ReplaceAll(cut, ".", "/")), "__init__.py"))
			is = err == nil
			pkgDir[cut] = is
		}
		return !is
	}
	suffix := func(lang, tqn string) []string {
		key := parser.Normalize(tqn)
		var run []string
		for _, id := range bySuffix[key] {
			if rootCut(lang, idx.place[id].qn, key) {
				run = append(run, id)
			}
		}
		return lookup(lang, run, "s:"+key, store.Filter{QualifiedSuffix: tqn}, func(s parser.Symbol) bool {
			return parser.MatchQualified(s.QualifiedName, key) && rootCut(s.Language, parser.Normalize(s.QualifiedName), key)
		})
	}
	// qualified also takes a short name, such as GDScript's BaseTrigger.run
	// for res://trigger.BaseTrigger.run.
	qualified := func(lang, tqn string) []string {
		if ids := exact(lang, tqn); len(ids) > 0 {
			return ids
		}
		return suffix(lang, tqn)
	}
	named := func(lang, name string) []string {
		ids := byName[[2]string{lang, name}]
		// Two candidates of the run are ambiguous without the store, unless fit
		// may drop them (variables, for a call): then a stored function decides.
		callable := slices.DeleteFunc(slices.Clone(ids), func(id string) bool {
			return kindOf[id] == parser.KindVariable || kindOf[id] == parser.KindConstant
		})
		if len(collapse(callable)) < 2 {
			ids = append(ids, fromStore("n:"+lang+"\x00"+name, store.Filter{Name: name, Language: lang}, 20, func(s parser.Symbol) bool {
				return s.Name == name && s.Language == lang
			})...)
		}
		return ids
	}
	// fit drops the candidates a relation cannot point at: only imports reach
	// templates, scene nodes, doc chunks, routes and hooks, and a call matched
	// by name alone, or naming a variable that has a function or method of
	// its qualified name in its file (a PHP property and its method), binds
	// only to something callable
	// (an exact match alone may name a variable holding a function).
	fit := func(rk parser.RelationKind, nameOnly bool, ids []string) []string {
		if rk == parser.RelImports {
			return ids
		}
		// the property and method of one class share file and qualified name; a
		// var holding a function in another file (a build-tagged variant) stays
		callable := map[[2]string]bool{}
		for _, id := range ids {
			if p := idx.place[id]; p.fn {
				callable[[2]string{p.file, p.qn}] = true
			}
		}
		out := ids[:0:0]
		for _, id := range ids {
			switch kindOf[id] {
			case parser.KindTemplate, parser.KindScene, parser.KindChunk, parser.KindRoute, parser.KindHook:
				continue
			case parser.KindVariable, parser.KindConstant:
				if p := idx.place[id]; rk == parser.RelCall && (nameOnly || callable[[2]string{p.file, p.qn}]) {
					continue
				}
			}
			out = append(out, id)
		}
		return out
	}
	for i := range files {
		for j := range files[i].syms {
			lang := files[i].syms[j].Language
			for k := range files[i].syms[j].Relations {
				rel := &files[i].syms[j].Relations[k]
				var ids []string
				nameOnly := false
				resolution := parser.ResolutionUnique
				if name, ok := strings.CutPrefix(rel.TargetQualifiedName, "window."); ok && lang == "html" {
					// An html inline handler (onclick="f()") calls the browser
					// global window.f: a JavaScript function of that name that
					// is a global (top level of a classic script, or window.f),
					// never a method, a function of an ES module, nor a symbol
					// whose qualified name is window.f (a module called window).
					ids, nameOnly = slices.DeleteFunc(slices.Clone(named("javascript", name)), func(id string) bool { return !idx.global[id] }), true
				} else if name, ok := strings.CutPrefix(rel.TargetQualifiedName, "route:"); ok && lang == "php" {
					// route('x') in PHP or Blade names the Laravel route whose
					// name (metadata name) is x; its qualified name is its URI.
					resolution, ids = parser.ResolutionExact, slices.Clone(idx.routes[name])
					if len(collapse(ids)) < 2 {
						// shortcut: one scroll of the named routes, more than MaxScroll of them in the store miss the rest; a store filter on the name if a repo needs it
						for _, id := range fromStore("routes", store.Filter{Language: "php", Kinds: []parser.Kind{parser.KindRoute}}, store.MaxScroll, func(s parser.Symbol) bool {
							return s.Metadata["name"] != ""
						}) {
							if idx.route[id] == name {
								ids = append(ids, id)
							}
						}
					}
				} else if tqn := rel.TargetQualifiedName; tqn != "" {
					resolution = parser.ResolutionExact
					ids = exact(lang, tqn)
					// A TQN that normalizes to a bare name ("..delta") is
					// only a name: matched by name, never exact.
					if key := parser.Normalize(tqn); len(ids) == 0 && !strings.Contains(key, ".") {
						ids, nameOnly, resolution = named(lang, cmp.Or(rel.TargetName, key)), true, parser.ResolutionUnique
					}
					// A TQN written relative to a source root other than the
					// workspace root (Python src/ layouts, GDScript's short
					// <Class>.m) names a qualified suffix: exact there. In other
					// languages it is a guess (PHP Status::run of a global
					// \Status), so unique.
					if len(ids) == 0 && !nameOnly && !rel.Value {
						if ids = suffix(lang, tqn); len(ids) > 0 && lang != "python" && lang != "gdscript" {
							resolution = parser.ResolutionUnique
						}
					}
					// extends "res://x.gd" (GDScript) and a scene node's attached
					// script name a whole script: its one top-level class,
					// whatever that class is called.
					if len(ids) == 0 && (rel.Kind == parser.RelExtends || rel.Kind == parser.RelUsesType && lang == "gdscript") {
						ids = lookup(lang, byModule[tqn], "m:"+tqn, store.Filter{Module: tqn, Kinds: []parser.Kind{parser.KindClass}}, func(s parser.Symbol) bool {
							return s.Module == tqn && s.Kind == parser.KindClass && s.Owner == ""
						})
					}
					// import { sleep } of a JS declaration both named and
					// default-exported, so qualified "<module>.default": the
					// module's top-level symbol of that name.
					if dot := strings.LastIndex(tqn, "."); len(ids) == 0 && dot > 0 && lang == "javascript" {
						m, n := tqn[:dot], tqn[dot+1:]
						ids = lookup(lang, idx.byModName[[2]string{m, n}], "mn:"+m+"\x00"+n, store.Filter{Module: m, Name: n}, func(s parser.Symbol) bool {
							return s.Module == m && s.Name == n && s.Owner == ""
						})
					}
					// $this->m(), self.m(), this.m() on a member the owner
					// inherits name the owner, which exists but does not
					// define it: first the owner's declared bases (parent
					// class, trait, embedded type), one level, of an owner in
					// this run or in the store. Owner and base may be short
					// names (GDScript class_name).
					if dot := strings.LastIndex(tqn, "."); len(ids) == 0 && dot > 0 && rel.TargetName != "" {
						seen := map[string]bool{}
						for _, o := range qualified(lang, tqn[:dot]) {
							for _, b := range idx.bases[o] {
								for _, id := range qualified(lang, b+"."+rel.TargetName) {
									if !seen[id] {
										seen[id] = true
										ids = append(ids, id)
									}
								}
							}
						}
					}
					// Otherwise fall back to the name. An unknown owner is
					// external (vendor, stdlib), and so is, most likely, the
					// member of a Go owner that embeds an external type (a
					// testify suite's s.T()): no workspace method of its name.
					// A route's handler names its controller: another class's
					// method of that name is not it.
					if dot := strings.LastIndex(tqn, "."); len(ids) == 0 && dot > 0 && rel.TargetName != "" && !rel.Value && files[i].syms[j].Kind != parser.KindRoute {
						owners := qualified(lang, tqn[:dot])
						external := lang == "go" && slices.ContainsFunc(owners, func(o string) bool {
							return slices.ContainsFunc(idx.bases[o], func(b string) bool { return len(qualified(lang, b)) == 0 })
						})
						if len(owners) > 0 && !external {
							ids, nameOnly, resolution = named(lang, rel.TargetName), true, parser.ResolutionUnique
						}
					}
				} else if name := rel.TargetName; name != "" {
					ids, nameOnly = named(lang, name), true
				}
				ids = collapse(fit(rel.Kind, nameOnly, ids))
				switch {
				case len(ids) == 1:
					rel.TargetID, rel.Resolution = ids[0], resolution
				case len(ids) > 1:
					rel.Resolution = parser.ResolutionAmbiguous
				}
				// A value use (a Go handler passed, assigned, returned) counts
				// only as an exact link to a function or method: anything else
				// read a variable, constant or field, or is unknown.
				if k := kindOf[rel.TargetID]; rel.Value && (rel.Resolution != parser.ResolutionExact || k != parser.KindFunction && k != parser.KindMethod) {
					rel.TargetID = ""
				}
			}
			// Dropped, not stored: it would show as an unresolved name.
			files[i].syms[j].Relations = slices.DeleteFunc(files[i].syms[j].Relations, func(rel parser.Relation) bool { return rel.Value && rel.TargetID == "" })
		}
	}
}

// embedUpsert embeds symbols in batches of 16 and upserts them in batches of
// 64, purging a file's old points right before its first new ones land. The
// state records a file once every one of its symbols is in the store.
func (r *run) embedUpsert(ctx context.Context, files []parsed) error {
	var syms []*parser.Symbol
	var owner []int // index into files, parallel to syms
	remaining := make([]int, len(files))
	for i := range files {
		if len(files[i].syms) == 0 {
			if err := r.Store.DeleteByFile(ctx, r.Collection, files[i].path); err != nil {
				return err
			}
			r.record(files[i])
			continue
		}
		remaining[i] = len(files[i].syms)
		for j := range files[i].syms {
			syms = append(syms, &files[i].syms[j])
			owner = append(owner, i)
		}
	}

	var items []store.Item
	var itemOwner []int
	bytes := 0 // Content bytes in items
	purged := make([]bool, len(files))
	flush := func() error {
		for _, fi := range itemOwner {
			if !purged[fi] {
				if err := r.Store.DeleteByFile(ctx, r.Collection, files[fi].path); err != nil {
					return err
				}
				purged[fi] = true
			}
		}
		if err := r.Store.Upsert(ctx, r.Collection, items); err != nil {
			return err
		}
		for _, fi := range itemOwner {
			if remaining[fi]--; remaining[fi] == 0 {
				r.record(files[fi])
			}
		}
		items, itemOwner = items[:0], itemOwner[:0]
		r.tick(false)
		return nil
	}
	for i := 0; i < len(syms); i += embedBatch {
		if err := ctx.Err(); err != nil {
			return err
		}
		batch := syms[i:min(i+embedBatch, len(syms))]
		texts := make([]string, len(batch))
		for j, s := range batch {
			texts[j] = embedText(s)
		}
		vecs, err := r.embed(ctx, texts)
		if err != nil {
			return err
		}
		for j, s := range batch {
			items = append(items, store.Item{Vector: vecs[j], Symbol: *s})
			itemOwner = append(itemOwner, owner[i+j])
			// Content is uncapped and a whole file can be one symbol:
			// a count-only cut could make one batch hundreds of MB.
			if bytes += len(s.Content); len(items) >= upsertBatch || bytes >= upsertBytes {
				if err := flush(); err != nil {
					return err
				}
				bytes = 0
			}
		}
	}
	if len(items) > 0 {
		return flush()
	}
	return nil
}

// record marks a file as fully indexed in the state.
func (r *run) record(p parsed) {
	e := Entry{ModTime: p.info.ModTime().UnixNano(), Size: p.info.Size(), IDs: symIDs(p), Hash: p.hash}
	if first, ok := r.named[p.path]; ok && !slices.Equal(first, e.IDs) {
		// The file changed after the name pass (a checkout in progress):
		// callers resolved in this run may hold its first ids, never stored.
		// Keep those ids and force a re-parse, so the next run sees them
		// vanish and re-resolves the callers.
		e.ModTime, e.IDs = 0, append(e.IDs, first...)
	}
	r.state.Files[p.path] = e
	r.status.Done++
}

func contentHash(src []byte) string {
	h := sha256.Sum256(src)
	return hex.EncodeToString(h[:])
}

func symIDs(p parsed) []string {
	ids := make([]string, len(p.syms))
	for i := range p.syms {
		ids[i] = p.syms[i].ID
	}
	return ids
}

// embed calls the embedder; transport retries are the embedder's job.
func (r *run) embed(ctx context.Context, texts []string) ([][]float32, error) {
	in := make([]string, len(texts))
	for i, t := range texts {
		in[i] = embed.DocText(r.Embedder.Model(), t)
	}
	vecs, err := r.Embedder.Embed(ctx, in)
	if err != nil {
		return nil, err
	}
	if len(vecs) != len(texts) {
		return nil, fmt.Errorf("embed: %d vectors for %d texts", len(vecs), len(texts))
	}
	return vecs, nil
}

func embedText(s *parser.Symbol) string {
	t := string(s.Kind) + " " + s.QualifiedName + "\n" + s.Signature + "\n" + s.Doc + "\n"
	// Slice Content before concatenating, or a megabyte symbol is copied
	// whole per embed. One byte past the cap keeps the rune walk below
	// identical to cutting the full text.
	if n := max(maxEmbedText-len(t)+1, 0); n < len(s.Content) {
		t += s.Content[:n]
	} else {
		t += s.Content
	}
	if len(t) <= maxEmbedText {
		return t
	}
	cut := maxEmbedText
	for cut > 0 && !utf8.RuneStart(t[cut]) {
		cut--
	}
	return t[:cut]
}

// vanishedErr is true for the error of a file that is no longer there: deleted, or
// under a path whose parent became a file (chain.go/x.go after a checkout
// made chain.go a file again).
func vanishedErr(err error) bool {
	return errors.Is(err, fs.ErrNotExist) || errors.Is(err, syscall.ENOTDIR)
}

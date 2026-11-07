// Package index runs the indexing pipeline for one workspace root: walk,
// skip rules, parse, workspace-wide relation resolution, embed, upsert.
// Progress and the per-file snapshot live under Dir(root).
package index

import (
	"context"
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
	"time"
	"unicode/utf8"

	"golang.org/x/sync/semaphore"

	"github.com/doITmagic/rag-code-mcp/v3/embed"
	"github.com/doITmagic/rag-code-mcp/v3/parser"
	"github.com/doITmagic/rag-code-mcp/v3/store"
)

var (
	// ErrLocked means another run holds Dir(root)/index.lock.
	ErrLocked = errors.New("index: another run holds the lock")
	// ErrHeadChanged means git HEAD moved while the run was indexing; the
	// caller should queue a full run.
	ErrHeadChanged = errors.New("index: git HEAD changed during the run")
)

const (
	embedBatch   = 16
	upsertBatch  = 64
	upsertBytes  = 8 << 20 // Content bytes per upsert, so one batch never holds a huge transaction
	maxEmbedText = 2000
	maxLookups   = 500 // store lookups per run while finding stale referrers
	lookupBatch  = 500 // values per lookup: ids, qualified names or names
)

// Options configures one run.
type Options struct {
	Root       string
	Collection string
	Store      store.Store
	Embedder   embed.Embedder
	Exclude    []string     // directory names pruned at any depth
	Files      []string     // nil = full walk; otherwise only these files are checked
	Deleted    []string     // files to purge from the index
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
}

type run struct {
	Options
	root         string
	status       Status
	state        *State
	fresh        bool                // the collection was (re)created this run: nothing to look up
	named        map[string][]string // ids of the name pass, which callers are resolved against
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
	if prev, ok := ReadStatus(root); ok && prev.Phase != "done" {
		r.Files = nil // the last run failed, was cut short or crashed: the index is partial
	}
	defer func() {
		if r.Files == nil {
			parser.ReleaseParsers()
		}
	}()

	var head string
	r.status.Branch, head = gitHead(ctx, root)
	r.writeStatus()

	r.state, err = LoadState(root)
	if err != nil {
		o.Log.Warn("index: state.json unreadable, starting fresh", "root", root, "err", err)
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
	dim, err := o.Embedder.Dimension(ctx)
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
	extra := r.staleReferrers(ctx, files, goneIDs, changed, gone)
	for _, f := range gone {
		if err := purge(f); err != nil {
			return r.finish(ctx, err)
		}
	}
	if len(extra) > 0 {
		live := extra[:0]
		for _, f := range extra {
			if _, err := os.Lstat(f); errors.Is(err, fs.ErrNotExist) {
				// Deleted since its points were stored (a files-only run after a
				// branch switch): purge it, it is not a parse failure.
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
	}
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
		if err := r.embedUpsert(ctx, batch); err != nil {
			return r.finish(ctx, err)
		}
	}
	if _, now := gitHead(ctx, root); now != head {
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
	r.tick(true)
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
// repository or git does not answer in 5 s.
func gitHead(ctx context.Context, root string) (branch, head string) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "git", "-C", root, "rev-parse", "HEAD", "--abbrev-ref", "HEAD").Output()
	if err != nil {
		return "", ""
	}
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	if len(lines) != 2 {
		return "", ""
	}
	return lines[1], lines[0]
}

// scan returns the files to (re)index and the files to purge, both sorted.
func (r *run) scan(ctx context.Context) (changed, gone []string, err error) {
	var files []string
	if r.Files == nil {
		if files, err = Walk(r.root, r.Exclude); err != nil {
			return nil, nil, err
		}
	} else {
		for _, f := range r.Files {
			files = append(files, r.abs(f))
		}
	}
	ex := set(r.Exclude)
	var ign map[string]bool // Walk already dropped git-ignored files
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
		if err != nil || ign[f] || (r.Files != nil && skipExplicit(r.root, ex, f, info)) {
			if _, ok := r.state.Files[f]; ok {
				goneSet[f] = true
			}
			continue
		}
		seen[f] = true
		if e, ok := r.state.Files[f]; !ok || e.ModTime != info.ModTime().UnixNano() || e.Size != info.Size() {
			changed = append(changed, f)
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
// must lie under root, outside hidden and excluded directories, and pass Skip.
func skipExplicit(root string, ex map[string]bool, f string, info fs.FileInfo) bool {
	rel, err := filepath.Rel(root, f)
	if err != nil || strings.HasPrefix(rel, "..") {
		return true
	}
	for _, d := range strings.Split(filepath.Dir(rel), string(filepath.Separator)) {
		if d != "." && (strings.HasPrefix(d, ".") || ex[d]) {
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
		if p.err != nil {
			r.status.Failed++
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
	if p.info, p.err = os.Stat(path); p.err != nil {
		return p
	}
	if p.info.Size() > maxFileSize {
		return p // grew past the cap since the walk: indexed as empty
	}
	src, err := os.ReadFile(path)
	if err != nil {
		p.err = err
		return p
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
			r.Log.Warn("index: referrer lookup failed", "err", err)
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
				for _, h := range hits {
					use(h)
				}
			}
			vals = vals[n:]
		}
		return true
	}
	now := map[string]bool{}
	var oldOfGained []string // old ids of the files that gained an id
	for _, p := range files {
		old := r.state.Files[p.path].IDs // the old ids until record()
		vanished = append(vanished, old...)
		gained := false
		for _, s := range p.syms {
			now[s.ID] = true
			gained = gained || !slices.Contains(old, s.ID)
		}
		if gained {
			oldOfGained = append(oldOfGained, old...)
		}
	}
	had := map[[2]string]bool{} // {file, qualified name} of the old points, not yet purged
	query(oldOfGained, func(v []string) store.Filter { return store.Filter{IDs: v} }, func(h store.Hit) {
		had[[2]string{h.Symbol.FilePath, h.Symbol.QualifiedName}] = true
	})
	byLang := map[string][]parser.Symbol{} // the symbols the run adds
	for _, p := range files {
		old := r.state.Files[p.path].IDs
		for _, s := range p.syms {
			// a symbol that only moved keeps its name: not added
			if !slices.Contains(old, s.ID) && !had[[2]string{p.path, s.QualifiedName}] {
				byLang[s.Language] = append(byLang[s.Language], s)
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
	ok := query(gone, func(v []string) store.Filter { return store.Filter{TargetIDs: v} }, add(all))
	// A name the run removes for good can turn an ambiguous call unique.
	kept := map[[2]string]bool{}
	for _, p := range files {
		for _, s := range p.syms {
			kept[[2]string{s.Language, s.Name}] = true
		}
	}
	lost := map[string]map[string]bool{} // language -> names
	ok = ok && query(gone, func(v []string) store.Filter { return store.Filter{IDs: v} }, func(h store.Hit) {
		if l, n := h.Symbol.Language, h.Symbol.Name; !kept[[2]string{l, n}] {
			if lost[l] == nil {
				lost[l] = map[string]bool{}
			}
			lost[l][n] = true
		}
	})
	for lang, names := range lost {
		if !ok {
			break
		}
		ambiguous := func(h parser.Symbol) bool {
			return slices.ContainsFunc(h.Relations, func(rel parser.Relation) bool {
				return names[rel.TargetName] && rel.Resolution == parser.ResolutionAmbiguous
			})
		}
		ok = query(slices.Collect(maps.Keys(names)), func(v []string) store.Filter { return store.Filter{TargetNames: v, Language: lang} }, add(ambiguous))
	}
	for lang, syms := range byLang {
		if !ok {
			break
		}
		qns, names := make([]string, len(syms)), map[string]bool{}
		var nameList []string
		for i, s := range syms {
			qns[i] = s.QualifiedName
			if !names[s.Name] {
				names[s.Name] = true
				nameList = append(nameList, s.Name)
			}
		}
		guessed := func(h parser.Symbol) bool { // a relation resolved by name, or not at all
			return slices.ContainsFunc(h.Relations, func(rel parser.Relation) bool {
				return names[rel.TargetName] && rel.Resolution != parser.ResolutionExact && rel.Resolution != parser.ResolutionAmbiguous
			})
		}
		ok = query(qns, func(v []string) store.Filter { return store.Filter{TargetQualifiedNames: v} }, add(all)) &&
			query(nameList, func(v []string) store.Filter { return store.Filter{TargetNames: v, Language: lang} }, add(guessed))
	}
	sort.Strings(out)
	return out
}

// names drops everything but the names (and kind, module, owner) of each
// symbol: what staleReferrers and newNameIndex read.
func names(files []parsed) []parsed {
	for i := range files {
		for j, s := range files[i].syms {
			files[i].syms[j] = parser.Symbol{ID: s.ID, QualifiedName: s.QualifiedName, Name: s.Name, Language: s.Language, Kind: s.Kind, Module: s.Module, Owner: s.Owner}
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
	changed   map[string]bool        // files of the run: their stored points are stale
}

func newNameIndex(files []parsed) nameIndex {
	idx := nameIndex{byQN: map[string][]string{}, bySuffix: map[string][]string{}, byName: map[[2]string][]string{}, byModule: map[string][]string{},
		byModName: map[[2]string][]string{}, lang: map[string]string{}, changed: make(map[string]bool, len(files))}
	for i := range files {
		idx.changed[files[i].path] = true
		for _, s := range files[i].syms {
			qn := parser.Normalize(s.QualifiedName)
			idx.lang[s.ID] = s.Language
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
	byQN, bySuffix, byName, byModule, langOf, changed := idx.byQN, idx.bySuffix, idx.byName, idx.byModule, idx.lang, idx.changed
	// No lookup cap: a target left unresolved here stays so until its caller
	// file changes. One store error stops the lookups for the run.
	failed := false
	cache := map[string][]string{}
	fromStore := func(key string, f store.Filter, match func(parser.Symbol) bool) []string {
		if r.fresh || failed {
			return nil
		}
		if ids, ok := cache[key]; ok {
			return ids
		}
		hits, err := r.Store.Scroll(ctx, r.Collection, f, 20)
		if err != nil {
			r.Log.Warn("index: relation lookup failed, remaining targets stay unresolved", "err", err)
			failed = true
			return nil
		}
		var ids []string
		for _, h := range hits {
			// Points of files in this run are stale until their upsert.
			if !changed[h.Symbol.FilePath] && match(h.Symbol) {
				ids = append(ids, h.Symbol.ID)
				langOf[h.Symbol.ID] = h.Symbol.Language
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
	// lookup takes run and store candidates together, so a files run sees
	// what a full run sees; two in the run already make it ambiguous.
	lookup := func(lang string, run []string, key string, f store.Filter, match func(parser.Symbol) bool) []string {
		ids := keep(lang, run)
		if len(ids) < 2 {
			ids = append(ids, keep(lang, fromStore(key, f, match))...)
		}
		return ids
	}
	exact := func(lang, tqn string) []string {
		key := parser.Normalize(tqn)
		return lookup(lang, byQN[key], "q:"+key, store.Filter{QualifiedName: tqn}, func(s parser.Symbol) bool {
			return parser.Normalize(s.QualifiedName) == key
		})
	}
	named := func(lang, name string) []string {
		ids := byName[[2]string{lang, name}]
		if len(ids) < 2 {
			ids = append(ids, fromStore("n:"+lang+"\x00"+name, store.Filter{Name: name, Language: lang}, func(s parser.Symbol) bool {
				return s.Name == name && s.Language == lang
			})...)
		}
		return ids
	}
	for i := range files {
		for j := range files[i].syms {
			lang := files[i].syms[j].Language
			for k := range files[i].syms[j].Relations {
				rel := &files[i].syms[j].Relations[k]
				var ids []string
				resolution := parser.ResolutionUnique
				if tqn := rel.TargetQualifiedName; tqn != "" {
					resolution = parser.ResolutionExact
					key := parser.Normalize(tqn)
					ids = exact(lang, tqn)
					// A TQN written relative to a source root other than the
					// workspace root (Python src/ layouts, monorepo subprojects)
					// names a qualified suffix; only a unique match is taken.
					if len(ids) == 0 {
						resolution = parser.ResolutionUnique
						ids = lookup(lang, bySuffix[key], "s:"+key, store.Filter{QualifiedSuffix: tqn}, func(s parser.Symbol) bool {
							return parser.MatchQualified(s.QualifiedName, key)
						})
					}
					// extends "res://x.gd" (GDScript) names a whole script: its
					// one top-level class, whatever that class is called.
					if len(ids) == 0 && rel.Kind == parser.RelExtends {
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
					// inherits (parent class, trait, embedded type) name the
					// owner, which exists but does not define it: fall back
					// to the name. An unknown owner is external (vendor, stdlib).
					if dot := strings.LastIndex(tqn, "."); len(ids) == 0 && dot > 0 && rel.TargetName != "" && len(exact(lang, tqn[:dot])) > 0 {
						ids = named(lang, rel.TargetName)
					}
				} else if name := rel.TargetName; name != "" {
					ids = named(lang, name)
				}
				switch {
				case len(ids) == 1:
					rel.TargetID, rel.Resolution = ids[0], resolution
				case len(ids) > 1:
					rel.Resolution = parser.ResolutionAmbiguous
				}
			}
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
	e := Entry{ModTime: p.info.ModTime().UnixNano(), Size: p.info.Size(), IDs: symIDs(p)}
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

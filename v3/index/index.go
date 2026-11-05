// Package index runs the indexing pipeline for one workspace root: walk,
// skip rules, parse, workspace-wide relation resolution, embed, upsert.
// Progress and the per-file snapshot live under <root>/.ragcode/.
package index

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/doITmagic/rag-code-mcp/v3/embed"
	"github.com/doITmagic/rag-code-mcp/v3/parser"
	"github.com/doITmagic/rag-code-mcp/v3/store"
)

var (
	// ErrLocked means another run holds <root>/.ragcode/index.lock.
	ErrLocked = errors.New("index: another run holds the lock")
	// ErrHeadChanged means git HEAD moved while the run was indexing; the
	// caller should queue a full run.
	ErrHeadChanged = errors.New("index: git HEAD changed during the run")
)

const (
	embedBatch   = 16
	upsertBatch  = 64
	upsertBytes  = 8 << 20 // Content bytes per upsert, well under Qdrant's 32 MB gRPC limit
	maxEmbedText = 2000
	maxLookups   = 500 // store lookups per run while finding stale referrers
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
// <root>/.ragcode/index_status.json.
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
	Phase     string    `json:"phase"` // scanning|parsing|embedding|done|failed
}

func statusPath(root string) string { return filepath.Join(root, ".ragcode", "index_status.json") }

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
	fresh        bool // the collection was (re)created this run: nothing to look up
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
	// A cloned repository may commit .ragcode as a symlink: never write
	// through it.
	dir := filepath.Join(root, ".ragcode")
	if fi, err := os.Lstat(dir); err == nil && !fi.IsDir() {
		return r.fail(fmt.Errorf("index: %s is not a directory", dir))
	}
	// Keep the index files out of git without touching the repository;
	// WriteAtomic replaces a committed symlink instead of following it.
	if err := WriteAtomic(filepath.Join(dir, ".gitignore"), []byte("*\n")); err != nil {
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
	r.state.Model = model
	if o.Recreate {
		// Saved before the drop: after a crash in between, a stale state.json
		// would match every file and incremental runs would never re-embed them.
		r.state = newState(root)
		r.state.Model = model
		if err := r.state.Save(); err != nil {
			return r.finish(ctx, err)
		}
		if err := o.Store.DeleteCollection(ctx, o.Collection); err != nil && !errors.Is(err, store.ErrNotFound) {
			return r.finish(ctx, err)
		}
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
		if hits, err := o.Store.Scroll(ctx, o.Collection, store.Filter{}, 1); err == nil && len(hits) == 0 {
			r.fresh = true
		}
	}
	if r.fresh {
		// The store is empty: everything is new, and a files-only run would
		// leave a partial index that nothing ever completes.
		r.state = newState(root)
		r.state.Model = model
		r.Files = nil
	}

	changed, gone, err := r.scan(ctx)
	if err != nil {
		return r.finish(ctx, err)
	}
	var goneIDs []string
	for _, f := range gone {
		if err := o.Store.DeleteByFile(ctx, o.Collection, f); err != nil {
			return r.finish(ctx, err)
		}
		if e, ok := r.state.Files[f]; ok {
			goneIDs = append(goneIDs, e.IDs...)
			delete(r.state.Files, f)
		}
		r.status.Deleted++
	}
	r.status.Total = len(changed)
	r.status.Phase = "parsing"
	r.tick(true)

	files := r.parse(ctx, changed)
	if extra := r.staleReferrers(ctx, files, goneIDs, changed, gone); len(extra) > 0 {
		r.status.Total += len(extra)
		files = append(files, r.parse(ctx, extra)...)
	}
	if ctx.Err() != nil {
		return r.finish(ctx, ctx.Err())
	}
	r.status.Phase = "embedding"
	r.resolve(ctx, files)
	if err := r.embedUpsert(ctx, files); err != nil {
		return r.finish(ctx, err)
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
		err = ctx.Err() // a store call cut short reports a gRPC status, not ctx.Err()
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
	goneSet := map[string]bool{}
	seen := make(map[string]bool, len(files))
	for _, f := range files {
		if err := ctx.Err(); err != nil {
			return nil, nil, err
		}
		info, err := os.Lstat(f) // a symlink fails Skip, as in Walk
		if err != nil || (r.Files != nil && skipExplicit(r.root, ex, f, info)) {
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

// parse analyzes files on max(2, NumCPU/2) workers and returns the results
// in the input order, minus the files that failed (counted in Failed).
func (r *run) parse(ctx context.Context, files []string) []parsed {
	jobs := make(chan string)
	results := make(chan parsed)
	var wg sync.WaitGroup
	for i := 0; i < max(2, runtime.NumCPU()/2); i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for f := range jobs {
				results <- parseOne(r.root, f)
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

func parseOne(root, path string) (p parsed) {
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
	src, err := os.ReadFile(path)
	if err != nil {
		p.err = err
		return p
	}
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
	now := map[string]bool{}
	var added []parser.Symbol
	for _, p := range files {
		old := r.state.Files[p.path].IDs // the old ids until record()
		vanished = append(vanished, old...)
		var had map[string]bool // qualified names in p.path's points, not yet purged
		for _, s := range p.syms {
			now[s.ID] = true
			if slices.Contains(old, s.ID) {
				continue
			}
			if had == nil { // a symbol that only moved keeps its name: not added
				had = map[string]bool{}
				if len(old) > 0 {
					hits, _ := scroll(store.Filter{FilePath: p.path})
					for _, h := range hits {
						had[h.Symbol.QualifiedName] = true
					}
				}
			}
			if !had[s.QualifiedName] {
				added = append(added, s)
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
	// ponytail: one scroll per vanished id or added symbol, capped; a
	// payload-only target_id rewrite would also spare re-embedding referrers.
	refs := func(f store.Filter, keep func(parser.Symbol) bool) bool {
		hits, ok := scroll(f)
		for _, h := range hits {
			if f := h.Symbol.FilePath; !skip[f] && keep(h.Symbol) {
				skip[f] = true
				out = append(out, f)
			}
		}
		return ok
	}
	all := func(parser.Symbol) bool { return true }
	for _, id := range vanished {
		if !now[id] && !refs(store.Filter{TargetID: id}, all) {
			break
		}
	}
	for _, s := range added {
		guessed := func(h parser.Symbol) bool { // a relation resolved by name, or not at all
			return slices.ContainsFunc(h.Relations, func(rel parser.Relation) bool {
				return rel.TargetName == s.Name && rel.Resolution != parser.ResolutionExact && rel.Resolution != parser.ResolutionAmbiguous
			})
		}
		if !refs(store.Filter{TargetQualifiedName: s.QualifiedName}, all) || !refs(store.Filter{TargetName: s.Name, Language: s.Language}, guessed) {
			break
		}
	}
	sort.Strings(out)
	return out
}

// resolve fills Relation.TargetID and Resolution over the run's symbols plus,
// when the store already holds the rest of the workspace, store lookups for
// the targets the run does not define.
func (r *run) resolve(ctx context.Context, files []parsed) {
	byQN := map[string][]string{}
	bySuffix := map[string][]string{}  // every dot-boundary suffix of a qualified name
	byName := map[[2]string][]string{} // language, name: a call never crosses languages
	changed := make(map[string]bool, len(files))
	for i := range files {
		changed[files[i].path] = true
		for _, s := range files[i].syms {
			qn := parser.Normalize(s.QualifiedName)
			byQN[qn] = append(byQN[qn], s.ID)
			for j := 0; j < len(qn); j++ {
				if j == 0 || qn[j-1] == '.' {
					bySuffix[qn[j:]] = append(bySuffix[qn[j:]], s.ID)
				}
			}
			byName[[2]string{s.Language, s.Name}] = append(byName[[2]string{s.Language, s.Name}], s.ID)
		}
	}
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
			}
		}
		cache[key] = ids
		return ids
	}
	exact := func(tqn string) []string {
		key := parser.Normalize(tqn)
		if ids := byQN[key]; len(ids) > 0 {
			return ids
		}
		return fromStore("q:"+key, store.Filter{QualifiedName: tqn}, func(s parser.Symbol) bool {
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
					ids = exact(tqn)
					// A TQN written relative to a source root other than the
					// workspace root (Python src/ layouts, monorepo subprojects)
					// names a qualified suffix; only a unique match is taken.
					if len(ids) == 0 {
						resolution = parser.ResolutionUnique
						if ids = bySuffix[key]; len(ids) == 0 {
							ids = fromStore("s:"+key, store.Filter{QualifiedSuffix: tqn}, func(s parser.Symbol) bool {
								return parser.MatchQualified(s.QualifiedName, key)
							})
						}
					}
					// $this->m(), self.m(), this.m() on a member the owner
					// inherits (parent class, trait, embedded type) name the
					// owner, which exists but does not define it: fall back
					// to the name. An unknown owner is external (vendor, stdlib).
					if dot := strings.LastIndex(tqn, "."); len(ids) == 0 && dot > 0 && rel.TargetName != "" && len(exact(tqn[:dot])) > 0 {
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
			// Content is uncapped and a file up to 4 MiB can be one symbol:
			// a count-only cut could exceed Qdrant's 32 MB message limit.
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
	ids := make([]string, len(p.syms))
	for i := range p.syms {
		ids[i] = p.syms[i].ID
	}
	r.state.Files[p.path] = Entry{ModTime: p.info.ModTime().UnixNano(), Size: p.info.Size(), IDs: ids}
	r.status.Done++
}

// embed calls the embedder; transport retries are the embedder's job.
func (r *run) embed(ctx context.Context, texts []string) ([][]float32, error) {
	vecs, err := r.Embedder.Embed(ctx, texts)
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

package tools

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"os"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
)

const (
	grepFiles = 5000
	grepHits  = 20
)

// GrepHit is one source line holding the searched word, read from disk and
// not from the index.
type GrepHit struct {
	FilePath string `json:"file_path"`
	Line     int    `json:"line"`
	Text     string `json:"text"`
	Match    string `json:"match"` // always "grep"
}

// grepRun is a text search running beside an index lookup, for the case the
// index does not know the name (a field, a local, a file not indexed yet).
type grepRun struct {
	word      string
	cancel    context.CancelFunc
	done      chan struct{}
	hits      []GrepHit
	truncated bool
}

// startGrep searches the source files under start (root when empty) for the
// last segment of name as a whole word. It is bounded by work (grepFiles,
// grepHits), not by a clock: a slow disk only takes longer, and the request's
// own context ends it. Call stop when the index answered, or add to wait for
// the lines.
func (t *tools) startGrep(ctx context.Context, root, start, name string) *grepRun {
	word := name[strings.LastIndexAny(name, ".:/\\")+1:]
	g := &grepRun{word: word, cancel: func() {}, done: make(chan struct{})}
	if word == "" {
		close(g.done)
		return g
	}
	if start == "" {
		start = root
	}
	ctx, g.cancel = context.WithCancel(ctx)
	go func() {
		defer close(g.done)
		g.hits, g.truncated = t.grep(ctx, root, start, word)
	}()
	return g
}

func (g *grepRun) stop() {
	if g != nil {
		g.cancel()
	}
}

// add waits for the search and, when it found lines, puts them in data
// (grep, grep_truncated) and says so in msg.
func (g *grepRun) add(msg string, data map[string]any) string {
	if g == nil {
		return msg
	}
	<-g.done
	g.cancel()
	if len(g.hits) == 0 && !g.truncated {
		return msg
	}
	data["grep"], data["grep_truncated"] = g.hits, g.truncated
	note := fmt.Sprintf("a text search of the source files finds %q on %d lines (data.grep; not from the index)", g.word, len(g.hits))
	if g.truncated {
		note = fmt.Sprintf("a text search of the source files finds %q on at least %d lines (data.grep, cut short; not from the index)", g.word, len(g.hits))
	}
	if len(g.hits) == 0 { // cut short with nothing yet: not the same as "not in the text"
		data["grep"] = []GrepHit{}
		note = fmt.Sprintf("a text search of the source files for %q was cut short before it found anything (cancelled, or over %d files): grep to be sure", g.word, grepFiles)
	}
	if msg == "" {
		return note
	}
	return msg + "; " + note
}

// grep returns at most grepHits lines of at most grepFiles source files that
// hold word as a whole word; truncated says the search stopped early.
func (t *tools) grep(ctx context.Context, root, start, word string) (hits []GrepHit, truncated bool) {
	re := regexp.MustCompile(`(?:^|[^A-Za-z0-9_$])` + regexp.QuoteMeta(word) + `(?:$|[^A-Za-z0-9_])`)
	files := t.sourceFiles(ctx, root, start, nil, grepFiles)
	truncated = len(files) >= grepFiles || ctx.Err() != nil // a cancelled walk is not a complete one
	// Files are read in parallel: on a slow file system (9p under WSL) the
	// time is the per-file open, not the CPU. Lines are kept per file and
	// joined in file order, so the answer is the same as a sequential read.
	per := make([][]GrepHit, len(files))
	var found atomic.Int32
	var wg sync.WaitGroup
	sem := make(chan struct{}, runtime.GOMAXPROCS(0))
	for i, path := range files {
		if ctx.Err() != nil || int(found.Load()) > grepHits { // later files cannot reach the first grepHits lines
			break
		}
		sem <- struct{}{}
		wg.Go(func() {
			defer func() { <-sem }()
			src, err := os.ReadFile(path)
			if err != nil || bytes.IndexByte(src, 0) >= 0 || !bytes.Contains(src, []byte(word)) {
				return
			}
			sc := bufio.NewScanner(bytes.NewReader(src))
			sc.Buffer(nil, len(src)+1)
			for n := 1; sc.Scan() && len(per[i]) <= grepHits; n++ {
				if re.Match(sc.Bytes()) {
					per[i] = append(per[i], GrepHit{FilePath: path, Line: n, Text: lineText([]string{sc.Text()}, 1), Match: "grep"})
					found.Add(1)
				}
			}
		})
	}
	wg.Wait()
	for _, h := range per {
		hits = append(hits, h...)
	}
	if len(hits) > grepHits {
		return hits[:grepHits], true
	}
	return hits, truncated || ctx.Err() != nil
}

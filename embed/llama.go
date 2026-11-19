package embed

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode/utf8"
)

const (
	batchSize  = 16
	batchBytes = 8 << 10 // the server embeds inputs one by one: a big request only delays a queued query
	retryDelay = time.Second
	probeText  = "dimension probe"
	// DefaultTokens is the longest input, in tokens, the built-in model
	// (EmbeddingGemma 2) takes: its context window.
	DefaultTokens = 8192
	// cutMargin is how many tokens short of the limit a refused input is cut
	// to: the count of the cut text is an estimate.
	cutMargin = 64
	// defaultIdle is how long the server stays up after the last call.
	defaultIdle = 5 * time.Minute
)

// Runtime finds the files the embedder runs: llama.cpp's llama-server and a
// GGUF embedding model. internal/llama downloads and checks them.
type Runtime interface {
	// Ensure returns both paths, fetching what is missing.
	Ensure(ctx context.Context) (server, model string, err error)
	// Installed reports whether both are on disk already.
	Installed() bool
	// RuntimeName names the runtime for status output.
	RuntimeName() string
}

// Status is what rag_server_info reports about the embedder.
type Status struct {
	Model   string `json:"model"`
	Runtime string `json:"runtime"`
	// State is running (the model is loaded), starting (the files are being
	// fetched or the model is loading, also after the call that began it gave
	// up), stopped (it starts on the next call) or not_installed (the next
	// call downloads it first).
	State string `json:"state"`
	Error string `json:"error,omitempty"` // why the last start failed
}

// Llama embeds with a llama-server this process runs itself: started on a
// loopback port by the first call, stopped after IdleStop without one and by
// Close. Nothing else is needed on the machine.
type Llama struct {
	// Threads caps the CPU threads the model computes with; 0 lets llama.cpp
	// pick. Set before use.
	Threads int
	// MinFreeMB is the free memory needed to load the model; with less, a
	// call that would start the server fails with ErrLowMemory. 0 turns the
	// check off.
	MinFreeMB int
	// IdleStop is how long the server outlives the last call; 0 means 5 minutes.
	IdleStop time.Duration
	// MaxTokens is the context the server is started with: the longest input
	// it embeds whole. 0 means DefaultTokens. A model trained for less gets
	// its own limit from the server. Set before use.
	MaxTokens int

	model  string
	rt     Runtime
	runDir string // where the record of the running server is kept, for orphan cleanup
	client *http.Client
	dim    atomic.Int32 // 0 until the first successful probe

	sem     chan struct{} // one-slot lock over the fields below; a channel so that waiting honours a context
	srv     *server
	users   int         // calls using srv now
	lastUse time.Time   // when users last dropped to 0
	timer   *time.Timer // the idle stop, armed by the last release
	fetch   *fetch      // a download in progress
	closed  bool
	bg      context.Context // ends with Close; downloads run under it
	bgStop  context.CancelFunc

	// What Status reports, under its own lock: a start may hold sem for as
	// long as a download takes, and Status must answer meanwhile.
	stMu    sync.Mutex
	state   string // "", "starting" or "running"
	lastErr string
}

// NewLlama returns an embedder for model, run from the files rt provides.
// runDir holds one small record per running server.
func NewLlama(model string, rt Runtime, runDir string) *Llama {
	bg, stop := context.WithCancel(context.Background())
	return &Llama{model: model, rt: rt, runDir: runDir, client: &http.Client{}, sem: make(chan struct{}, 1), bg: bg, bgStop: stop}
}

// Model returns the embedding model name.
func (l *Llama) Model() string { return l.model }

// maxTokens is the context the server is started with.
func (l *Llama) maxTokens() int {
	if l.MaxTokens > 0 {
		return l.MaxTokens
	}
	return DefaultTokens
}

// Embed embeds texts in batches of at most 16 texts and 8 KiB (a longer
// text goes alone), one request per batch. A text longer than the model
// context is cut to fit.
func (l *Llama) Embed(ctx context.Context, texts []string) ([][]float32, error) {
	out := make([][]float32, 0, len(texts))
	for start, end := 0, 0; start < len(texts); start = end {
		end = start + 1
		for size := len(texts[start]); end < len(texts) && end-start < batchSize && size+len(texts[end]) <= batchBytes; end++ {
			size += len(texts[end])
		}
		vecs, err := l.embedBatch(ctx, texts[start:end])
		var big *tooLargeError
		if errors.As(err, &big) {
			vecs, err = l.embedCut(ctx, texts[start:end], big)
		}
		if err != nil {
			return nil, err
		}
		out = append(out, vecs...)
	}
	return out, nil
}

// Dimension probes the model once and caches the vector size. A failed probe
// is retried on the next call.
func (l *Llama) Dimension(ctx context.Context) (int, error) {
	if d := l.dim.Load(); d > 0 {
		return int(d), nil
	}
	vecs, err := l.embedBatch(ctx, []string{probeText})
	if err != nil {
		return 0, err
	}
	l.dim.Store(int32(len(vecs[0])))
	return len(vecs[0]), nil
}

// Status reports the embedder without starting or downloading anything.
func (l *Llama) Status() Status {
	s := Status{Model: l.model, Runtime: l.rt.RuntimeName(), State: "stopped"}
	l.stMu.Lock()
	state, lastErr := l.state, l.lastErr
	l.stMu.Unlock()
	switch {
	case state != "":
		s.State = state
	case !l.rt.Installed():
		s.State = "not_installed"
	}
	s.Error = lastErr
	return s
}

// Close stops the server; later calls fail with ErrUnavailable.
func (l *Llama) Close() error {
	l.bgStop()
	l.sem <- struct{}{}
	defer func() { <-l.sem }()
	l.closed = true
	l.drop()
	return nil
}

// embedCut embeds texts, which the server refused together with refused, one
// by one, cutting each until the server takes it.
func (l *Llama) embedCut(ctx context.Context, texts []string, refused *tooLargeError) ([][]float32, error) {
	shorter := func(t string, big *tooLargeError) string {
		limit := big.limit
		if limit <= 0 {
			limit = l.maxTokens()
		}
		return cut(t, big.tokens, limit)
	}
	out := make([][]float32, 0, len(texts))
	for _, t := range texts {
		if len(texts) == 1 {
			t = shorter(t, refused) // the refusal was about this very text
		}
		for {
			vecs, err := l.embedBatch(ctx, []string{t})
			var big *tooLargeError
			if errors.As(err, &big) {
				if s := shorter(t, big); len(s) < len(t) {
					t = s
					continue
				}
			}
			if err != nil {
				return nil, err
			}
			out = append(out, vecs[0])
			break
		}
	}
	return out, nil
}

// cut shortens t, which the server counted as tokens tokens against a limit
// of limit, so that it should fit: to the share that fits, a little under the
// limit, when the count is known, to half otherwise. It never cuts inside a
// character, and returns t itself when t is a single character.
func cut(t string, tokens, limit int) string {
	n := len(t) / 2
	if tokens > limit && limit > 0 {
		keep := limit - min(cutMargin, limit/8)
		n = int(int64(len(t)) * int64(keep) / int64(tokens))
	}
	for n > 0 && !utf8.RuneStart(t[n]) {
		n--
	}
	if n == 0 {
		_, n = utf8.DecodeRuneInString(t) // at least the first character
	}
	return t[:n]
}

// embedBatch sends one request, retrying once after a connection-level
// failure: the server may have been stopped or have died, and the second
// attempt starts it again.
func (l *Llama) embedBatch(ctx context.Context, texts []string) ([][]float32, error) {
	body, err := json.Marshal(map[string]any{"input": texts})
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrUnavailable, err)
	}
	var vecs [][]float32
	for attempt := 0; ; attempt++ {
		vecs, err = l.call(ctx, body, len(texts))
		if err == nil {
			return vecs, nil
		}
		var te *transportError
		if attempt > 0 || !errors.As(err, &te) || ctx.Err() != nil {
			return nil, err
		}
		select {
		case <-ctx.Done():
			return nil, err
		case <-time.After(retryDelay):
		}
	}
}

// call makes sure the server runs and posts body to /v1/embeddings.
func (l *Llama) call(ctx context.Context, body []byte, n int) ([][]float32, error) {
	s, err := l.acquire(ctx)
	if err != nil {
		return nil, err
	}
	defer l.release()
	// No clock here: the caller's context and a closed connection end it.
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.base+"/v1/embeddings", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrUnavailable, err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+s.key)
	res, err := l.client.Do(req)
	if err != nil {
		return nil, &transportError{err}
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(io.LimitReader(res.Body, 2048))
		var e struct {
			Error struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		msg := strings.TrimSpace(string(raw))
		if json.Unmarshal(raw, &e) == nil && e.Error.Message != "" {
			msg = e.Error.Message
		}
		if big := parseTooLarge(msg); big != nil {
			return nil, big
		}
		return nil, fmt.Errorf("%w: llama-server %s: %s", ErrUnavailable, res.Status, msg)
	}
	var resp struct {
		Data []struct {
			Index     int       `json:"index"`
			Embedding []float32 `json:"embedding"`
		} `json:"data"`
	}
	if err := json.NewDecoder(res.Body).Decode(&resp); err != nil {
		return nil, fmt.Errorf("%w: bad response: %w", ErrUnavailable, err)
	}
	if len(resp.Data) != n {
		return nil, fmt.Errorf("%w: got %d embeddings for %d inputs", ErrUnavailable, len(resp.Data), n)
	}
	out := make([][]float32, n)
	for _, d := range resp.Data {
		if d.Index < 0 || d.Index >= n || out[d.Index] != nil || len(d.Embedding) == 0 {
			return nil, fmt.Errorf("%w: bad embedding for input %d", ErrUnavailable, d.Index)
		}
		out[d.Index] = d.Embedding
	}
	return out, nil
}

// tooLargeError is the server refusing an input longer than its context.
type tooLargeError struct {
	msg    string
	tokens int // what the server counted; 0 when it did not say
	limit  int // what the server takes; 0 when it did not say
}

// The two refusals of llama-server for an input it cannot embed whole: over
// the batch (which Llama sets to the context) and over the context of the
// slot, which is the model's own when that is smaller than the one asked for.
const (
	overBatch   = "too large to process"
	overContext = "larger than the max context size"
)

// parseTooLarge returns the refusal msg is, with the counts it names, or nil
// for any other error.
func parseTooLarge(msg string) *tooLargeError {
	var limitAt string
	switch {
	case strings.Contains(msg, overBatch):
		limitAt = "current batch size: "
	case strings.Contains(msg, overContext):
		limitAt = "max context size ("
	default:
		return nil
	}
	big := &tooLargeError{msg: msg}
	if i := strings.Index(msg, "input ("); i >= 0 {
		_, _ = fmt.Sscanf(msg[i:], "input (%d tokens)", &big.tokens)
	}
	if i := strings.Index(msg, limitAt); i >= 0 {
		_, _ = fmt.Sscanf(msg[i+len(limitAt):], "%d", &big.limit)
	}
	return big
}

func (e *tooLargeError) Error() string { return "embed: embedder unavailable: llama-server: " + e.msg }
func (e *tooLargeError) Unwrap() error { return ErrUnavailable }

type transportError struct{ err error }

func (e *transportError) Error() string   { return "embed: embedder unavailable: " + e.err.Error() }
func (e *transportError) Unwrap() []error { return []error{ErrUnavailable, e.err} }

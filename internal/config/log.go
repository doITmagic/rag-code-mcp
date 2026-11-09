package config

import (
	"fmt"
	"io"
	"log/slog"
	"os"
	"sync"
)

// NewLogger returns a text slog.Logger at the given level writing to w.
// Unknown levels fall back to info.
func NewLogger(level string, w io.Writer) *slog.Logger {
	var l slog.Level
	if err := l.UnmarshalText([]byte(level)); err != nil {
		l = slog.LevelInfo
	}
	return slog.New(slog.NewTextHandler(w, &slog.HandlerOptions{Level: l}))
}

// OpenLogFile opens path for appending and rotates it to path+".1" when it is
// over maxMB. The size is re-checked every 1000 writes.
func OpenLogFile(path string, maxMB int) (io.WriteCloser, error) {
	w := &logFile{path: path, max: int64(maxMB) << 20}
	if err := w.rotateIfLarge(); err != nil {
		return nil, err
	}
	if err := w.open(); err != nil {
		return nil, err
	}
	return w, nil
}

// logFile is an append-only file with size-based rotation.
// shortcut: one mutex around each write; a log line is the only I/O here.
type logFile struct {
	mu   sync.Mutex
	path string
	max  int64
	f    *os.File
	fi   os.FileInfo // identity of f, to notice a rotation by another process
	n    int
}

func (w *logFile) open() error {
	f, err := os.OpenFile(w.path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return fmt.Errorf("config: open log: %w", err)
	}
	w.f = f
	w.fi, _ = f.Stat()
	return nil
}

func (w *logFile) rotateIfLarge() error {
	st, err := os.Stat(w.path)
	if err != nil || st.Size() <= w.max {
		return nil // missing file: nothing to rotate; stat errors surface on open
	}
	if err := os.Rename(w.path, w.path+".1"); err != nil {
		return fmt.Errorf("config: rotate log: %w", err)
	}
	return nil
}

func (w *logFile) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.n++
	// Another server rotated the shared log: follow it, or a second rotation
	// would leave this process appending to an unlinked file.
	if st, err := os.Stat(w.path); err == nil && w.fi != nil && !os.SameFile(st, w.fi) {
		w.f.Close()
		if err := w.open(); err != nil {
			return 0, err
		}
	}
	if w.n%1000 == 0 {
		st, err := w.f.Stat()
		if err == nil && st.Size() > w.max {
			w.f.Close()
			// A failed rename (Windows: another process holds the file open)
			// keeps appending to the oversized file; retried 1000 writes later.
			_ = w.rotateIfLarge()
			if err := w.open(); err != nil {
				return 0, err
			}
		}
	}
	return w.f.Write(p)
}

func (w *logFile) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.f.Close()
}

package logger

import (
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/doITmagic/rag-code-mcp/internal/utils"
)

// SyncWriter wraps an *os.File and forwards writes directly to it.
// It relies on OS buffering and file close/rotation for flushing to disk,
// rather than calling Sync on every write (which is expensive under high volume).
// Once the file would grow past maxSize it is renamed to <path>.1 and a new
// one is started, so the log takes at most about twice maxSize on disk.
type SyncWriter struct {
	mu      sync.Mutex
	File    *os.File
	path    string
	size    int64
	maxSize int64
}

func (sw *SyncWriter) Write(p []byte) (n int, err error) {
	sw.mu.Lock()
	defer sw.mu.Unlock()
	if sw.maxSize > 0 && sw.size > 0 && sw.size+int64(len(p)) > sw.maxSize {
		sw.rotate()
	}
	n, err = sw.File.Write(p)
	sw.size += int64(n)
	return n, err
}

func (sw *SyncWriter) rotate() {
	// On failure keep writing to the current file and retry after another maxSize.
	sw.size = 0
	if err := os.Rename(sw.path, sw.path+".1"); err != nil {
		fmt.Fprintf(os.Stderr, "[WARN] Failed to rotate log file %s: %v\n", sw.path, err)
		return
	}
	f, err := os.OpenFile(sw.path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		fmt.Fprintf(os.Stderr, "[WARN] Failed to reopen log file %s: %v\n", sw.path, err)
		return
	}
	_ = sw.File.Close()
	sw.File = f
}

// SimpleLogger provides basic logging with file rotation and levels.
// It wraps the robust log/slog standard library package.
type SimpleLogger struct {
	mu      sync.Mutex
	writer  *SyncWriter
	slogger *slog.Logger
	level   *slog.LevelVar
}

// Global logger instance
var Instance = &SimpleLogger{
	level: new(slog.LevelVar),
}

func init() {
	Instance.level.Set(slog.LevelInfo)
	handler := slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{
		Level: Instance.level,
		ReplaceAttr: func(groups []string, a slog.Attr) slog.Attr {
			if a.Key == slog.TimeKey {
				a.Value = slog.StringValue(a.Value.Time().Format("2006-01-02 15:04:05.000"))
			}
			return a
		},
	})
	Instance.slogger = slog.New(handler)
}

// Close closes the underlying log file.
func (l *SimpleLogger) Close() {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.writer != nil {
		l.writer.mu.Lock()
		_ = l.writer.File.Close()
		l.writer.mu.Unlock()
		l.writer = nil
	}
}

// SetMaxSizeMB sets the size at which the log file is rotated; 0 disables it.
func (l *SimpleLogger) SetMaxSizeMB(mb int) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.writer != nil {
		l.writer.mu.Lock()
		l.writer.maxSize = int64(mb) * 1024 * 1024
		l.writer.mu.Unlock()
	}
}

// Debug logs a debug message (only if MCP_LOG_LEVEL is set to 'debug').
func (l *SimpleLogger) Debug(format string, args ...interface{}) {
	if l.level.Level() <= slog.LevelDebug {
		l.slogger.Debug(fmt.Sprintf(format, args...))
	}
}

// SetLevel dynamically updates the logging level based on string input (e.g. from config).
func (l *SimpleLogger) SetLevel(level string) {
	switch strings.ToLower(level) {
	case "debug":
		l.level.Set(slog.LevelDebug)
	case "warn":
		l.level.Set(slog.LevelWarn)
	case "error":
		l.level.Set(slog.LevelError)
	default:
		l.level.Set(slog.LevelInfo)
	}
}

// Info logs an informational message.
func (l *SimpleLogger) Info(format string, args ...interface{}) {
	if l.level.Level() <= slog.LevelInfo {
		l.slogger.Info(fmt.Sprintf(format, args...))
	}
}

// Error logs an error message.
func (l *SimpleLogger) Error(format string, args ...interface{}) {
	if l.level.Level() <= slog.LevelError {
		l.slogger.Error(fmt.Sprintf(format, args...))
	}
}

// Warn logs a warning message.
func (l *SimpleLogger) Warn(format string, args ...interface{}) {
	if l.level.Level() <= slog.LevelWarn {
		l.slogger.Warn(fmt.Sprintf(format, args...))
	}
}

// Highlight logs a message with CYAN color for terminal output.
func (l *SimpleLogger) Highlight(format string, args ...interface{}) {
	if l.level.Level() <= slog.LevelInfo {
		msg := fmt.Sprintf(format, args...)
		l.slogger.Info(fmt.Sprintf("\033[36m[SEARCH] %s\033[0m", msg))
	}
}

// Helper: ResolveLogPath expands tilde and makes path absolute.
func ResolveLogPath(path string) (string, error) {
	if path == "" {
		return "", nil
	}

	// If path is just a filename (no separators), put it NEXT TO THE EXECUTABLE
	if filepath.Base(path) == path {
		exePath, err := os.Executable()
		if err != nil {
			// Fallback to CWD if executable path fails
			return path, nil
		}
		exeDir := filepath.Dir(exePath)

		// debugFile := "/tmp/ragcode-path-debug.txt"
		// _ = os.WriteFile(debugFile, []byte(fmt.Sprintf("Exe: %s\nDir: %s\nPath: %s\n", exePath, exeDir, filepath.Join(exeDir, path))), 0666)

		return filepath.Join(exeDir, path), nil
	}

	// Handle tilde expansion for user convenience in config files
	if strings.HasPrefix(path, "~/") || path == "~" {
		home, err := os.UserHomeDir()
		if err != nil {
			return path, err
		}
		if path == "~" {
			return home, nil
		}
		return filepath.Join(home, path[2:]), nil
	}

	// If relative path with separators, make absolute relative to CWD
	if !filepath.IsAbs(path) {
		abs, err := filepath.Abs(path)
		if err != nil {
			return path, err
		}
		return abs, nil
	}

	return path, nil
}

// defaultLogPath returns the default log file path.
// Resolved via utils.GetLogDir() which respects config overrides.
func defaultLogPath() string {
	return filepath.Join(utils.GetLogDir(), "ragcode.log")
}

// InitLoggerFromEnv initializes logger based on environment variables.
func InitLoggerFromEnv() {
	// Parse log level
	logLevel := strings.ToLower(os.Getenv("MCP_LOG_LEVEL"))
	switch logLevel {
	case "debug":
		Instance.level.Set(slog.LevelDebug)
	case "warn":
		Instance.level.Set(slog.LevelWarn)
	case "error":
		Instance.level.Set(slog.LevelError)
	default:
		Instance.level.Set(slog.LevelInfo)
	}

	if Instance.writer != nil {
		Instance.Close()
	}

	path := os.Getenv("MCP_LOG_FILE")
	if path == "" {
		// Use ~/.ragcode/logs/ragcode.log
		path = defaultLogPath()
	}

	// Path is already resolved when setting env var
	expanded, err := ResolveLogPath(path)
	if err != nil {
		fmt.Fprintf(os.Stderr, "[WARN] Failed to resolve log path %s: %v\n", path, err)
		return
	}
	path = expanded

	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		fmt.Fprintf(os.Stderr, "[WARN] Failed to create log directory %s: %v\n", dir, err)
		return
	}

	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		fmt.Fprintf(os.Stderr, "[WARN] Failed to open log file %s: %v\n", path, err)
		return
	}

	var size int64
	if info, err := f.Stat(); err == nil {
		size = info.Size()
	}

	Instance.mu.Lock()
	Instance.writer = &SyncWriter{File: f, path: path, size: size}
	w := io.MultiWriter(os.Stderr, Instance.writer)
	Instance.mu.Unlock()

	// Switch handler to use multiwriter
	handler := slog.NewTextHandler(w, &slog.HandlerOptions{
		Level: Instance.level,
		ReplaceAttr: func(groups []string, a slog.Attr) slog.Attr {
			// Clean up output logs to match expected simple format
			if a.Key == slog.TimeKey {
				a.Value = slog.StringValue(a.Value.Time().Format("2006-01-02 15:04:05.000"))
			}
			return a
		},
	})
	Instance.slogger = slog.New(handler)

	// Route slog as default so any library using slog gets the same handler.
	// Note: stdlib log.Print* calls are NOT automatically routed through slog.
	slog.SetDefault(Instance.slogger)

	// STARTING SESSION mark
	Instance.slogger.Info(fmt.Sprintf("--- STARTING SESSION %s ---", time.Now().Format(time.RFC3339)))
	Instance.slogger.Info(fmt.Sprintf("Logger initialized successfully writing to %s", path))
}

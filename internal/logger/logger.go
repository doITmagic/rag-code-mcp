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
type SyncWriter struct {
	File *os.File
}

func (sw SyncWriter) Write(p []byte) (n int, err error) {
	return sw.File.Write(p)
}

// SimpleLogger provides basic logging with file rotation and levels.
// It wraps the robust log/slog standard library package.
type SimpleLogger struct {
	mu      sync.Mutex
	logFile *os.File
	writer  io.Writer
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
	if l.logFile != nil {
		_ = l.logFile.Close()
		l.logFile = nil
		l.writer = nil
	}
}

// Debug logs a debug message (only if MCP_LOG_LEVEL is set to 'debug').
func (l *SimpleLogger) Debug(format string, args ...interface{}) {
	if l.level.Level() <= slog.LevelDebug {
		l.slogger.Debug(fmt.Sprintf(format, args...))
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

	if Instance.logFile != nil {
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

	Instance.mu.Lock()
	Instance.logFile = f
	Instance.writer = SyncWriter{File: f}
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

// RotateLogFile checks file size and rotates if needed.
func RotateLogFile(path string, maxSizeMB int) {
	if maxSizeMB <= 0 {
		return
	}
	info, err := os.Stat(path)
	if err != nil {
		return // File doesn't exist or error
	}

	maxSizeBytes := int64(maxSizeMB) * 1024 * 1024
	if info.Size() < maxSizeBytes {
		return
	}

	// Log rotation needed
	fmt.Fprintf(os.Stderr, "[INFO] Log file %s exceeds %dMB (%d bytes). Rotating...\n", path, maxSizeMB, info.Size())

	// Stream-based rotation: copy tail to temp file, then replace original.
	// Avoids reading entire log into memory.
	fileSize := info.Size()
	cutOffset := fileSize / 10
	if cutOffset == 0 {
		return
	}

	f, err := os.Open(path)
	if err != nil {
		fmt.Fprintf(os.Stderr, "[WARN] Failed to open log file for rotation: %v\n", err)
		return
	}
	defer f.Close()

	// Seek past cutoff and find next newline
	if _, err := f.Seek(cutOffset, io.SeekStart); err != nil {
		fmt.Fprintf(os.Stderr, "[WARN] Failed to seek in log file for rotation: %v\n", err)
		return
	}

	const scanBuf = 4096
	buf := make([]byte, scanBuf)
	offset := cutOffset
	cutoff := int64(-1)

	for {
		n, readErr := f.Read(buf)
		if n > 0 {
			for i := 0; i < n; i++ {
				if buf[i] == '\n' {
					cutoff = offset + int64(i) + 1
					break
				}
			}
			if cutoff != -1 {
				break
			}
			offset += int64(n)
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			fmt.Fprintf(os.Stderr, "[WARN] Failed to read log file for rotation: %v\n", readErr)
			return
		}
	}

	if cutoff == -1 {
		cutoff = cutOffset
	}

	if cutoff >= fileSize {
		if err := os.Truncate(path, 0); err != nil {
			fmt.Fprintf(os.Stderr, "[WARN] Failed to truncate log file: %v\n", err)
		}
		return
	}

	// Copy tail to temp file
	tmp, err := os.CreateTemp(filepath.Dir(path), "ragcode-logrotate-*")
	if err != nil {
		fmt.Fprintf(os.Stderr, "[WARN] Failed to create temp file for log rotation: %v\n", err)
		return
	}
	tmpName := tmp.Name()
	defer func() {
		tmp.Close()
		_ = os.Remove(tmpName) // best-effort cleanup; harmless if rename succeeded
	}()

	if _, err := f.Seek(cutoff, io.SeekStart); err != nil {
		fmt.Fprintf(os.Stderr, "[WARN] Failed to seek in log file for rotation: %v\n", err)
		return
	}

	if _, err := io.Copy(tmp, f); err != nil {
		fmt.Fprintf(os.Stderr, "[WARN] Failed to copy log tail for rotation: %v\n", err)
		return
	}

	if err := tmp.Chmod(info.Mode()); err != nil {
		fmt.Fprintf(os.Stderr, "[WARN] Failed to set permissions on rotated log file: %v\n", err)
		return
	}

	if err := tmp.Close(); err != nil {
		fmt.Fprintf(os.Stderr, "[WARN] Failed to close temp log file: %v\n", err)
		return
	}

	// Atomic replace
	if err := os.Rename(tmpName, path); err != nil {
		fmt.Fprintf(os.Stderr, "[WARN] Failed to replace log file during rotation: %v\n", err)
		return
	}

	fmt.Fprintf(os.Stderr, "[INFO] Log file rotated. Removed %d bytes.\n", cutoff)
}

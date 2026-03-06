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

// SyncWriter wraps an *os.File and calls Sync() after every Write,
// ensuring log entries are flushed to disk immediately (no buffering).
type SyncWriter struct {
	File *os.File
}

func (sw SyncWriter) Write(p []byte) (n int, err error) {
	n, err = sw.File.Write(p)
	if err == nil {
		_ = sw.File.Sync()
	}
	return
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

	// Ensure stdlib log messages flow into slog as well
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

	// Read file
	content, err := os.ReadFile(path)
	if err != nil {
		fmt.Fprintf(os.Stderr, "[WARN] Failed to read log file for rotation: %v\n", err)
		return
	}

	// Calculate cutoff (remove ~10%)
	cutSize := len(content) / 10
	if cutSize == 0 {
		return
	}

	// Find next newline after cutoff to keep lines intact
	cutoffIndex := -1
	for i := cutSize; i < len(content); i++ {
		if content[i] == '\n' {
			cutoffIndex = i + 1
			break
		}
	}

	if cutoffIndex == -1 {
		cutoffIndex = cutSize
	}

	if cutoffIndex >= len(content) {
		if err := os.Truncate(path, 0); err != nil {
			fmt.Fprintf(os.Stderr, "[WARN] Failed to truncate log file: %v\n", err)
		}
		return
	}

	newContent := content[cutoffIndex:]

	// Rewrite file
	if err := os.WriteFile(path, newContent, 0644); err != nil {
		fmt.Fprintf(os.Stderr, "[WARN] Failed to write rotated log file: %v\n", err)
		return
	}

	fmt.Fprintf(os.Stderr, "[INFO] Log file rotated. Removed %d bytes.\n", cutoffIndex)
}

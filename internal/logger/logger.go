package logger

import (
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
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
type SimpleLogger struct {
	logFile *os.File
	writer  io.Writer
}

// Global logger instance
var Instance = &SimpleLogger{}

// Close closes the underlying log file.
func (l *SimpleLogger) Close() {
	if l.logFile != nil {
		_ = l.logFile.Close()
		l.logFile = nil
		l.writer = nil
	}
}

func (l *SimpleLogger) shouldLog(msgLevel string) bool {
	levels := map[string]int{"debug": 0, "info": 1, "warn": 2, "error": 3}
	logLevel := strings.ToLower(os.Getenv("MCP_LOG_LEVEL"))
	if logLevel == "" {
		logLevel = "info"
	}
	return levels[msgLevel] >= levels[logLevel]
}

// Debug logs a debug message (only if MCP_LOG_LEVEL is set to 'debug').
func (l *SimpleLogger) Debug(format string, args ...interface{}) {
	if l.shouldLog("debug") {
		fmt.Fprintf(os.Stderr, "[DEBUG] "+format+"\n", args...)
		if l.writer != nil {
			fmt.Fprintf(l.writer, "[DEBUG] "+format+"\n", args...)
		}
	}
}

// Info logs an informational message.
func (l *SimpleLogger) Info(format string, args ...interface{}) {
	if l.shouldLog("info") {
		fmt.Fprintf(os.Stderr, "[INFO] "+format+"\n", args...)
		if l.writer != nil {
			fmt.Fprintf(l.writer, "[INFO] "+format+"\n", args...)
		}
	}
}

// Error logs an error message.
func (l *SimpleLogger) Error(format string, args ...interface{}) {
	if l.shouldLog("error") {
		fmt.Fprintf(os.Stderr, "[ERROR] "+format+"\n", args...)
		if l.writer != nil {
			fmt.Fprintf(l.writer, "[ERROR] "+format+"\n", args...)
		}
	}
}

// Warn logs a warning message.
func (l *SimpleLogger) Warn(format string, args ...interface{}) {
	if l.shouldLog("warn") {
		fmt.Fprintf(os.Stderr, "[WARN] "+format+"\n", args...)
	}
}

// Highlight logs a message with CYAN color for terminal output.
func (l *SimpleLogger) Highlight(format string, args ...interface{}) {
	if l.shouldLog("info") {
		// Cyan: \033[36m, Reset: \033[0m
		fmt.Fprintf(os.Stderr, "\033[36m[SEARCH] "+format+"\033[0m\n", args...)
		if l.writer != nil {
			fmt.Fprintf(l.writer, "[SEARCH] "+format+"\n", args...)
		}
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

// defaultLogPath returns the platform-appropriate default log file path.
//
//	Linux:   ~/.local/state/ragcode/ragcode.log  (XDG State Dir)
//	macOS:   ~/Library/Logs/ragcode/ragcode.log
//	Windows: %LOCALAPPDATA%\ragcode\ragcode.log
func defaultLogPath() string {
	switch runtime.GOOS {
	case "windows":
		base := os.Getenv("LOCALAPPDATA")
		if base == "" {
			home, err := os.UserHomeDir()
			if err != nil {
				return ""
			}
			base = filepath.Join(home, "AppData", "Local")
		}
		return filepath.Join(base, "ragcode", "ragcode.log")
	case "darwin":
		home, err := os.UserHomeDir()
		if err != nil {
			return ""
		}
		return filepath.Join(home, "Library", "Logs", "ragcode", "ragcode.log")
	default: // linux și alte sisteme UNIX
		home, err := os.UserHomeDir()
		if err != nil {
			return ""
		}
		return filepath.Join(home, ".local", "state", "ragcode", "ragcode.log")
	}
}

// InitLoggerFromEnv initializes logger based on environment variables.
func InitLoggerFromEnv() {
	// Default to stderr to avoid interfering with MCP stdio protocol
	log.SetOutput(os.Stderr)

	if Instance.logFile != nil {
		Instance.Close()
	}

	path := os.Getenv("MCP_LOG_FILE")
	if path == "" {
		// Use XDG state dir default: ~/.local/state/ragcode/ragcode.log
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

	Instance.logFile = f
	Instance.writer = SyncWriter{File: f}

	// FORCE DEBUG WRITE DIRECTLY TO FILE
	timestamp := time.Now().Format(time.RFC3339)
	if _, err := Instance.writer.Write([]byte(fmt.Sprintf("--- STARTING SESSION %s ---\n", timestamp))); err != nil {
		fmt.Fprintf(os.Stderr, "[WARN] Failed to write startup line to log file: %v\n", err)
	}

	log.SetOutput(io.MultiWriter(os.Stderr, Instance.writer))

	// Log startup info to verify location
	fmt.Fprintf(os.Stderr, "[INFO] Logging to file: %s\n", path)
	log.Printf("Logger initialized successfully writing to %s", path)
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

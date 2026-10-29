package logger

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

// logging.max_size_mb must bound the log file: past the limit it is moved
// to <path>.1 and a fresh file is started.
func TestLogFileRotatesAtMaxSize(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ragcode.log")
	old := bytes.Repeat([]byte("old line\n"), 1024*1024/9+1) // just over 1MB
	if err := os.WriteFile(path, old, 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("MCP_LOG_FILE", path)
	InitLoggerFromEnv()
	defer Instance.Close()
	Instance.SetMaxSizeMB(1)

	Instance.Info("after rotation")

	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Size() >= int64(len(old)) {
		t.Fatalf("log file is %d bytes, not rotated at 1MB", info.Size())
	}
	data, _ := os.ReadFile(path)
	if !bytes.Contains(data, []byte("after rotation")) {
		t.Fatalf("new log file misses the latest line: %q", data)
	}
	if rotated, err := os.Stat(path + ".1"); err != nil || rotated.Size() < int64(len(old)) {
		t.Fatalf("previous log not kept in %s.1: %v", path, err)
	}
}

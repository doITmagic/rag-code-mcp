package main

import (
	"fmt"
	"os"
	"strings"
	"testing"
)

func TestRotateLogFile(t *testing.T) {
	// Create a temporary log file
	tmpFile, err := os.CreateTemp("", "test-rotate-*.log")
	if err != nil {
		t.Fatalf("Failed to create temp file: %v", err)
	}
	logPath := tmpFile.Name()
	defer os.Remove(logPath)
	tmpFile.Close()

	// Generate content larger than our small test limit
	// Limit is 1MB. Need to write > 1MB.

	// Generating 1.1MB of data
	var sb strings.Builder
	line := "This is a test log line number %d with some padding data to increase size.\n"

	// 1MB = 1048576 bytes
	targetSize := 1024*1024 + (100 * 1024) // 1.1 MB

	for i := 0; sb.Len() < targetSize; i++ {
		sb.WriteString(fmt.Sprintf(line, i))
	}

	originalContent := sb.String()
	if err := os.WriteFile(logPath, []byte(originalContent), 0644); err != nil {
		t.Fatalf("Failed to write data: %v", err)
	}

	// Perform rotation with limit 1MB
	rotateLogFile(logPath, 1)

	// Verify size
	info, err := os.Stat(logPath)
	if err != nil {
		t.Fatalf("Failed to stat file: %v", err)
	}

	t.Logf("Original size: %d, New size: %d", len(originalContent), info.Size())

	if info.Size() >= int64(len(originalContent)) {
		t.Errorf("File was not rotated! Size remained %d", info.Size())
	}

	// Verify content structure
	newContentBytes, _ := os.ReadFile(logPath)
	newContent := string(newContentBytes)

	// Calculate expected cut (approx 10%)
	// The logic is: cutSize = len/10. Then find newline after cutSize.
	// So we delete AT LEAST len/10 bytes.
	expectedMinDelete := len(originalContent) / 10

	if int64(len(newContent)) > int64(len(originalContent))-int64(expectedMinDelete) {
		t.Errorf("File didn't shrink enough. Expected to delete at least %d bytes, but size is %d (original %d)",
			expectedMinDelete, len(newContent), len(originalContent))
	}

	// Verify it starts cleanly (not mid-line)
	// Our generated lines start with "This is..."
	if !strings.HasPrefix(newContent, "This is a test log line") {
		t.Errorf("Rotated log does not start with expected log line prefix. Starts with: %q", newContent[:50])
	}

	// Verify it ends correctly
	if !strings.HasSuffix(newContent, "data to increase size.\n") {
		t.Errorf("Rotated log end is corrupted")
	}
}

func TestRotateLogFile_UnderLimit(t *testing.T) {
	// Create a temporary log file
	tmpFile, err := os.CreateTemp("", "test-rotate-small-*.log")
	if err != nil {
		t.Fatalf("Failed to create temp file: %v", err)
	}
	logPath := tmpFile.Name()
	defer os.Remove(logPath)

	content := "Small file content\nLine 2\n"
	if _, err := tmpFile.WriteString(content); err != nil {
		t.Fatal(err)
	}
	tmpFile.Close()

	// Rotate with 1MB limit (file is tiny)
	rotateLogFile(logPath, 1)

	// Verify unchanged
	newContent, _ := os.ReadFile(logPath)
	if string(newContent) != content {
		t.Error("File was rotated but should not have been!")
	}
}

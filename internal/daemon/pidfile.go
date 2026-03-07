package daemon

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// PIDInfo contains daemon process metadata read from the PID file.
type PIDInfo struct {
	PID       int
	Version   string
	StartedAt string
}

// WritePID writes daemon metadata to the PID file.
// Format: key=value pairs, one per line (PID, VERSION, STARTED).
func WritePID(path string, pid int, version string) error {
	content := fmt.Sprintf("PID=%d\nVERSION=%s\nSTARTED=%s\n",
		pid, version, time.Now().Format(time.RFC3339))
	return os.WriteFile(path, []byte(content), 0644)
}

// ReadPID reads and parses the PID file. Returns error if file doesn't exist
// or contains no valid PID.
func ReadPID(path string) (*PIDInfo, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	info := &PIDInfo{}
	for _, line := range strings.Split(string(data), "\n") {
		parts := strings.SplitN(line, "=", 2)
		if len(parts) != 2 {
			continue
		}
		key, val := parts[0], parts[1]
		switch key {
		case "PID":
			info.PID, _ = strconv.Atoi(val)
		case "VERSION":
			info.Version = val
		case "STARTED":
			info.StartedAt = val
		}
	}

	if info.PID == 0 {
		return nil, fmt.Errorf("invalid PID file: no PID found in %s", path)
	}
	return info, nil
}

// RemovePID deletes the PID file. Returns nil if file doesn't exist.
func RemovePID(path string) error {
	err := os.Remove(path)
	if os.IsNotExist(err) {
		return nil
	}
	return err
}

// IsProcessAlive checks if a process with the given PID is running
// by sending signal 0 (no-op signal used for existence check).
func IsProcessAlive(pid int) bool {
	process, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	return process.Signal(syscall.Signal(0)) == nil
}

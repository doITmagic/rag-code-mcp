package utils

import (
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// KillProcessesOnPort kills any processes listening on the given TCP port.
// It attempts SIGTERM first, then SIGKILL after a short delay.
func KillProcessesOnPort(port int) error {
	// Find PIDs listening on the port
	pids, err := findPIDsOnPort(port)
	if err != nil {
		return fmt.Errorf("failed to find PIDs on port %d: %w", port, err)
	}
	if len(pids) == 0 {
		return nil // nothing to kill
	}

	// Send SIGTERM first
	for _, pid := range pids {
		if err := syscall.Kill(pid, syscall.SIGTERM); err != nil {
			// Process might already be gone; ignore
			continue
		}
	}

	// Wait a moment and ensure they are gone
	time.Sleep(500 * time.Millisecond)
	remaining, err := findPIDsOnPort(port)
	if err != nil {
		return fmt.Errorf("failed to recheck PIDs on port %d: %w", port, err)
	}

	// Force kill any remaining processes
	for _, pid := range remaining {
		if err := syscall.Kill(pid, syscall.SIGKILL); err != nil {
			continue
		}
	}

	return nil
}

// findPIDsOnPort returns PIDs of processes listening on the given port.
func findPIDsOnPort(port int) ([]int, error) {
	// Use lsof to find PIDs listening on the port
	cmd := exec.Command("lsof", "-ti", fmt.Sprintf(":%d", port))
	output, err := cmd.Output()
	if err != nil {
		// Port is not in use or lsof not available
		return nil, nil
	}

	lines := strings.Split(strings.TrimSpace(string(output)), "\n")
	var pids []int
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		pid, err := strconv.Atoi(line)
		if err != nil {
			continue
		}
		pids = append(pids, pid)
	}

	return pids, nil
}

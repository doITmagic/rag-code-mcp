package workspace

import (
	"context"
	"os"
	"path/filepath"
)

// Options configures the marker-based detection.
type Options struct {
	Markers  []string
	MaxDepth int
}

// Result captures a possible workspace root.
type Result struct {
	Root   string
	Marker string // The file/folder that triggered the match
}

// FindRoot searches upwards from the given path for a directory containing a marker.
func FindRoot(ctx context.Context, startPath string, markers []string, maxDepth int) (*Result, error) {
	if maxDepth <= 0 {
		maxDepth = 10
	}

	abs, err := filepath.Abs(startPath)
	if err != nil {
		return nil, err
	}

	curr := abs
	// If it's a file, start from the directory
	if info, err := os.Stat(abs); err == nil && !info.IsDir() {
		curr = filepath.Dir(abs)
	}

	for i := 0; i < maxDepth; i++ {
		for _, marker := range markers {
			markerPath := filepath.Join(curr, marker)
			if _, err := os.Stat(markerPath); err == nil {
				return &Result{Root: curr, Marker: marker}, nil
			}
		}

		parent := filepath.Dir(curr)
		if parent == curr {
			break
		}
		curr = parent
	}

	return nil, nil // No root found
}

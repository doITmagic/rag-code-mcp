package indexer

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// maxAvgLineLen is the average-line-length threshold above which a file
// is considered minified.  Normal hand-written source rarely exceeds
// 120 characters per line; minified bundles routinely exceed 1 000.
const maxAvgLineLen = 500

// sampleBytes caps the amount of data we read for the density heuristic.
const sampleBytes = 256 << 10 // 256 KiB

// minifiedSuffixes are well-known filename patterns that unambiguously
// mark a file as a minified/bundled asset.
var minifiedSuffixes = []string{
	".min.js", ".min.css", ".min.mjs",
	".bundle.js", ".bundle.css", ".bundle.mjs",
	".packed.js", ".chunk.js", ".chunk.css",
	"-min.js", "-min.css",
}

// isMinifiedOrVendored reports whether path points to machine-generated,
// bundled, or minified code that should be skipped before tree-sitter parsing.
//
// Directory-level exclusions (vendor/, node_modules/, etc.) are already
// handled by the WalkDir filter in IndexWorkspace, so this function only
// covers two remaining cases:
//  1. Filename suffix  (.min.js, .bundle.css, …) — no I/O.
//  2. Content density  — reads ≤256 KiB, counts newlines.
func isMinifiedOrVendored(path string) bool {
	if matchesMinifiedName(path) {
		return true
	}
	return exceedsLineDensity(path)
}

// matchesMinifiedName checks well-known minified filename patterns.
func matchesMinifiedName(path string) bool {
	base := strings.ToLower(filepath.Base(path))
	for _, s := range minifiedSuffixes {
		if strings.HasSuffix(base, s) {
			return true
		}
	}
	return false
}

// exceedsLineDensity reads the first sampleBytes of path and returns
// true when the average line length exceeds maxAvgLineLen.
func exceedsLineDensity(path string) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()

	buf := make([]byte, sampleBytes)
	n, err := io.ReadFull(f, buf)
	if n == 0 && err != nil {
		return false
	}

	lines := bytes.Count(buf[:n], []byte{'\n'})
	if lines == 0 {
		lines = 1
	}
	return n/lines > maxAvgLineLen
}

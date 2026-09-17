package engine

import "testing"

func TestProcessedCount(t *testing.T) {
	cases := []struct {
		name                    string
		disk, total, done, want int
	}{
		{"full index halfway", 10, 10, 4, 4},
		{"full index done", 10, 10, 10, 10},
		// 134 files already indexed, 2 edited: never above what is on disk.
		{"modified files not double counted", 134, 2, 2, 134},
		{"incremental in progress", 134, 42, 10, 102},
		// a file was deleted since the last run: disk total shrank.
		{"after delete", 133, 1, 1, 133},
		{"more queued than on disk", 3, 5, 0, 0},
	}
	for _, c := range cases {
		if got := processedCount(c.disk, c.total, c.done); got != c.want {
			t.Errorf("%s: processedCount(%d, %d, %d) = %d, want %d", c.name, c.disk, c.total, c.done, got, c.want)
		}
	}
}

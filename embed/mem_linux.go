package embed

import (
	"bufio"
	"os"
	"strconv"
	"strings"
)

// availableMB reads MemAvailable from /proc/meminfo.
func availableMB() (int, bool) {
	f, err := os.Open("/proc/meminfo")
	if err != nil {
		return 0, false
	}
	defer f.Close()
	for sc := bufio.NewScanner(f); sc.Scan(); {
		if rest, ok := strings.CutPrefix(sc.Text(), "MemAvailable:"); ok {
			kb, err := strconv.Atoi(strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(rest), "kB")))
			return kb / 1024, err == nil
		}
	}
	return 0, false
}

// ===== index/walk_ignored_linux_test.go =====
//go:build linux

package index

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/sys/unix"
)

// Walk must not open files git ignores: on Windows every open of a file is
// scanned by the antivirus (~20 ms, up to 600 ms), and a root with thousands
// of analyzable files in gitignored directories stayed in "scanning" 0/0 for
// 10+ minutes. Opens are counted with inotify IN_OPEN on the ignored dirs.
func TestWalkDoesNotOpenIgnoredFiles(t *testing.T) {
	root := t.TempDir()
	git := func(args ...string) {
		out, err := exec.Command("git", append([]string{"-C", root}, args...)...).CombinedOutput()
		require.NoError(t, err, string(out))
	}
	git("init", "-q")
	write := func(rel, body string) {
		p := filepath.Join(root, rel)
		require.NoError(t, os.MkdirAll(filepath.Dir(p), 0o755))
		require.NoError(t, os.WriteFile(p, []byte(body), 0o644))
	}
	write(".gitignore", "audituri/\ndepozit/\n")
	write("main.go", "package main\n")
	write("pkg/a.go", "package pkg\n")
	const perDir = 1000
	var ignoredDirs []string
	for _, d := range []string{"audituri/x", "depozit/concurenti/sursa"} {
		ignoredDirs = append(ignoredDirs, filepath.Join(root, d))
		for i := range perDir {
			write(fmt.Sprintf("%s/f%04d.go", d, i), "package x\n")
		}
	}

	fd, err := unix.InotifyInit1(unix.IN_NONBLOCK | unix.IN_CLOEXEC)
	require.NoError(t, err)
	defer unix.Close(fd)
	for _, d := range ignoredDirs {
		_, err := unix.InotifyAddWatch(fd, d, unix.IN_OPEN)
		require.NoError(t, err)
	}

	files, err := Walk(root, nil)
	require.NoError(t, err)
	assert.Equal(t, []string{filepath.Join(root, "main.go"), filepath.Join(root, "pkg", "a.go")}, files)

	opened := 0
	buf := make([]byte, 1<<20)
	for {
		n, err := unix.Read(fd, buf)
		if n <= 0 || err != nil {
			break
		}
		for off := 0; off+unix.SizeofInotifyEvent <= n; {
			var ev unix.InotifyEvent
			require.NoError(t, binary.Read(bytes.NewReader(buf[off:off+unix.SizeofInotifyEvent]), binary.NativeEndian, &ev))
			if ev.Mask&unix.IN_ISDIR == 0 { // a readdir of the dir itself is not a file open
				opened++
			}
			off += unix.SizeofInotifyEvent + int(ev.Len)
		}
	}
	assert.Zero(t, opened, "Walk opened %d of the %d git-ignored files", opened, 2*perDir)
}

// A file whose name is not valid UTF-8 is skipped: the store would alter its path.
func TestWalkSkipsNonUTF8Path(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "ok.go"), []byte("package a\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(root, "bad\xff.go"), []byte("package a\n"), 0o644))
	files, err := Walk(root, nil)
	require.NoError(t, err)
	assert.Equal(t, []string{filepath.Join(root, "ok.go")}, files)
}

package engine

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// F1b: %SystemRoot% and everything below it, and exactly %ProgramFiles%,
// %ProgramFiles(x86)% and %ProgramData%, are refused as roots on Windows,
// case-insensitively. Pure logic: runs on every OS.
func TestWindowsSystemDir(t *testing.T) {
	env := map[string]string{
		"SystemRoot":        `C:\WINDOWS`,
		"SystemDrive":       `C:`,
		"ProgramFiles":      `C:\Program Files`,
		"ProgramFiles(x86)": `C:\Program Files (x86)`,
		"ProgramData":       `C:\ProgramData\`, // a trailing separator is ignored
	}
	getenv := func(k string) string { return env[k] }
	for _, p := range []string{
		`C:\Windows`, `c:\windows`, `C:\WINDOWS\System32`, `C:\Windows\WinSxS\x86_foo`,
		`C:\Program Files`, `c:\program files (x86)`, `C:\ProgramData`,
	} {
		assert.True(t, windowsSystemDir(p, getenv), p)
	}
	for _, p := range []string{
		`C:\Windows.old`, `C:\WindowsApps2`, `C:\Program Files\MyApp`, `C:\ProgramData\proj`,
		`D:\Windows`, `C:\Users\me\proj`, `C:\src`,
	} {
		assert.False(t, windowsSystemDir(p, getenv), p)
	}

	// A client that starts the server with a scrubbed environment: fall back
	// to windir, then to the system drive, then to C:.
	env = map[string]string{"windir": `E:\Win`}
	assert.True(t, windowsSystemDir(`E:\Win\System32`, getenv))
	env = map[string]string{"SystemDrive": `D:`}
	assert.True(t, windowsSystemDir(`D:\Windows`, getenv))
	assert.True(t, windowsSystemDir(`D:\Program Files`, getenv))
	assert.False(t, windowsSystemDir(`C:\Windows`, getenv), "the system drive comes from the environment")
	env = map[string]string{}
	assert.True(t, windowsSystemDir(`C:\Windows\System32`, getenv))
	assert.True(t, windowsSystemDir(`C:\ProgramData`, getenv))
}

func TestWSLWindowsSystemDir(t *testing.T) {
	for _, p := range []string{"/mnt/c/Windows", "/mnt/c/windows/System32", "/mnt/d/Program Files", "/mnt/c/ProgramData"} {
		assert.True(t, wslWindowsSystemDir(p), p)
	}
	for _, p := range []string{"/mnt/c", "/mnt/c/Users/me/proj", "/mnt/c/ProgramData/proj", "/mnt/data/Windows", "/home/me/Windows", "/mnt/c/Windows.old"} {
		assert.False(t, wslWindowsSystemDir(p), p)
	}
}

// The same through validateRoot on a real Windows machine (not run on Linux).
func TestValidateRootRefusesWindowsSystemDirs(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("windows paths")
	}
	cfg := testConfig(t)
	sys := os.Getenv("SystemRoot")
	for _, root := range []string{sys, filepath.Join(sys, "System32"), os.Getenv("ProgramFiles"), os.Getenv("ProgramData")} {
		_, err := validateRoot(cfg, root)
		var re *RootError
		require.ErrorAs(t, err, &re, root)
		assert.Contains(t, re.Reason, "system directory", root)
	}
}

// F4 (WSL, Z13): the whole drive, every drive and the Windows user home seen
// from WSL are refused like C:\ and the home; projects below them are not.
func TestWSLBroadDir(t *testing.T) {
	for p, want := range map[string]string{
		"/mnt":                  "filesystem root",
		"/mnt/c":                "filesystem root",
		"/mnt/d":                "filesystem root",
		"/mnt/c/Users":          "contains the home directory",
		"/mnt/c/users/scdoi":    "home directory",
		"/mnt/c/Users/scdoi":    "home directory",
		"/mnt/c/Users/scdoi/go": "",
		"/mnt/c/proj":           "",
		"/mnt/data":             "",
		"/mnt/data/Users/me":    "",
		"/home/me":              "",
		"/mntx":                 "",
	} {
		got := wslBroadDir(p)
		if want == "" {
			assert.Empty(t, got, p)
		} else {
			assert.Contains(t, got, want, p)
		}
	}
}

// F2 (WSL, Z13): drvfs opens /mnt/c/users/scdoi as /mnt/c/Users/scdoi; the
// root takes the spelling its directories list, so both are one root and one
// index. Tested on the helper: a case-sensitive temp dir lists the names.
func TestDiskCase(t *testing.T) {
	if runtime.GOOS != "linux" { // it needs Both and both side by side: a case-sensitive file system; diskCase serves WSL only
		t.Skip("needs a case-sensitive file system")
	}
	base := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(base, "Users", "scdoi", "doITmagic"), 0o755))
	require.NoError(t, os.MkdirAll(filepath.Join(base, "Users", "Both"), 0o755))
	require.NoError(t, os.MkdirAll(filepath.Join(base, "Users", "both"), 0o755))
	for rest, want := range map[string]string{
		"users/scdoi/doitmagic": "Users/scdoi/doITmagic",
		"Users/scdoi/doITmagic": "Users/scdoi/doITmagic",
		"users/BOTH":            "Users/BOTH", // two entries match: kept as given
		"users/both":            "Users/both", // the exact name is listed
		"users/none/x":          "Users/none/x",
	} {
		assert.Equal(t, filepath.Join(base, want), diskCase(base, rest), rest)
	}
}

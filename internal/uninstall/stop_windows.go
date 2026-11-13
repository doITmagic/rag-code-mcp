//go:build windows

package uninstall

import (
	"path/filepath"
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"
)

// stopWindows terminates every process whose image is binary, except self,
// and waits up to 5 s for each to exit. A rag-code-mcp.exe started from
// another folder (an IDE's server, another install) is left alone.
func stopWindows(binary string, self int) {
	snap, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return
	}
	defer func() { _ = windows.CloseHandle(snap) }()
	name := filepath.Base(binary)
	e := windows.ProcessEntry32{Size: uint32(unsafe.Sizeof(windows.ProcessEntry32{}))}
	for err = windows.Process32First(snap, &e); err == nil; err = windows.Process32Next(snap, &e) {
		if int(e.ProcessID) == self || !strings.EqualFold(windows.UTF16ToString(e.ExeFile[:]), name) {
			continue
		}
		h, herr := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION|windows.PROCESS_TERMINATE|windows.SYNCHRONIZE, false, e.ProcessID)
		if herr != nil {
			continue // another user's or an elevated process
		}
		buf := make([]uint16, windows.MAX_LONG_PATH)
		n := uint32(len(buf))
		if windows.QueryFullProcessImageName(h, 0, &buf[0], &n) == nil && sameExe(windows.UTF16ToString(buf[:n]), binary) &&
			windows.TerminateProcess(h, 1) == nil {
			_, _ = windows.WaitForSingleObject(h, 5000)
		}
		_ = windows.CloseHandle(h)
	}
}

package embed

import (
	"syscall"
	"unsafe"
)

var globalMemoryStatusEx = syscall.NewLazyDLL("kernel32.dll").NewProc("GlobalMemoryStatusEx")

// availableMB asks Windows for the available physical memory.
func availableMB() (int, bool) {
	var m struct { // MEMORYSTATUSEX
		length, load                          uint32
		totalPhys, availPhys                  uint64
		totalPage, availPage                  uint64
		totalVirtual, availVirtual, availExtV uint64
	}
	m.length = uint32(unsafe.Sizeof(m))
	if r, _, _ := globalMemoryStatusEx.Call(uintptr(unsafe.Pointer(&m))); r == 0 {
		return 0, false
	}
	return int(m.availPhys >> 20), true
}

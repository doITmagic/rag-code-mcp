package engine

import "syscall"

// statfsType returns path's file system magic; tests swap it. uint32: the
// field is int32 on 32-bit platforms.
var statfsType = func(path string) (uint32, error) {
	var s syscall.Statfs_t
	err := syscall.Statfs(path, &s)
	return uint32(s.Type), err
}

// noNotify names root's file system when inotify reports no change on it:
// 9p, the Windows drives WSL2 mounts under /mnt (measured on /mnt/c).
// shortcut: 9p only, add cifs/nfs when a user reports edits not picked up there.
func noNotify(root string) string {
	if t, err := statfsType(root); err == nil && t == 0x01021997 {
		return "9p"
	}
	return ""
}

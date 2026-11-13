//go:build !windows

package index

func fileBusy(error) bool { return false }

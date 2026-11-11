//go:build !linux

package engine

func noNotify(string) string { return "" }

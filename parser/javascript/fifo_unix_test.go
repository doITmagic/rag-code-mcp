//go:build linux

package javascript

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/doITmagic/rag-code-mcp/v2/parser"
)

func TestFIFOConfigIsNotRead(t *testing.T) {
	// Warm the grammar before measuring the filesystem read.
	if _, err := (analyzer{}).Analyze(parser.File{Path: "/warm.js", Source: []byte("export function warm() {}")}); err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	cfg := filepath.Join(root, "tsconfig.json")
	if err := syscall.Mkfifo(cfg, 0600); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		_, err := (analyzer{}).Analyze(parser.File{Path: filepath.Join(root, "main.js"), Root: root, Source: []byte("import React from 'react';\nexport function run() { React.render(); }\n")})
		done <- err
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		// O_RDWR never waits for a peer; close it to release the blocked reader.
		f, err := os.OpenFile(cfg, os.O_RDWR, 0600)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := f.WriteString("{}"); err != nil {
			t.Fatal(err)
		}
		f.Close()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Fatal("Analyze did not finish after FIFO was released")
		}
		t.Fatal("Analyze blocked on FIFO tsconfig.json; a non-regular config must be skipped")
	}
}

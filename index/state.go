package index

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
)

// Entry is what the index knows about one file: the mtime and size it was
// indexed at and the IDs of the symbols it produced.
type Entry struct {
	ModTime int64    `json:"mtime"` // unix nanoseconds
	Size    int64    `json:"size"`
	IDs     []string `json:"ids,omitempty"`
}

// Format is the version of what parsers and the indexer store. Bump it when a
// change must reach files that did not change: a state with another Format
// is re-indexed from scratch.
// 2: Go Exported means package API; calls through locals are not package calls.
// 3: Go same-package types declared in another file; Python local imports and
// class-body calls.
// 4: JS root-absolute imports and TypeScript type uses; Go blank vars keep
// their relations; Python signatures stop at the def's colon.
// 5: JS methods of an object literal bound to a top-level const.
// 6: JS aliased imports (tsconfig/jsconfig paths, Nuxt/Vite ~/ @/ conventions),
// calls in top-level initializers and export default defineX(...); Go
// goimports-style import names; Python guarded imports; PHP signature types;
// GDScript super calls; Blade end line; relation target kinds and bases.
// 7: JS barrel re-exports followed and script-setup top-level calls owned by
// default; GDScript class_name bases; Go bases are embeddings only; a
// qualified-suffix match is exact and a name fallback unique.
// 8: twins (same qualified name in one file) are one target; a qualified
// name that normalizes to a bare name resolves by name; a qualified-suffix
// match is exact only in python and gdscript, unique elsewhere.
// 9: JS await f<T>() calls, top-level const/let type uses, one module per
// .vue; GDScript calls in property set/get bodies; a Go local named like the
// receiver shadows it.
// 10: a JS package specifier named like a root file is module
// node_modules/<spec>, never that file's module; a JS chained call split over
// lines is on its method name's line; a PHP class named Object keeps its name;
// Python module-level assignments carry their calls.
// 11: a chained call split over lines is on its method name's line in PHP,
// Go and Python too; a Go external test package (p_test) is its own module;
// an HTML void element spans its own tag; JS top-level destructuring calls
// belong to <script setup>'s default; a GDScript scene's attached script
// resolves to its class; a Python suffix is never cut inside a package.
// 12: a JS call through a member of an exported object literal
// (export default { f }, const api = { k: f }) targets the declaration it names.
// 13: a Go function or method named as a value (a handler passed, assigned,
// returned) is a usage when it links exactly to its declaration.
const Format = 13

// State is the per-root snapshot stored at Dir(root)/state.json. Only
// the run goroutine touches it; nothing here locks.
type State struct {
	root   string
	Model  string           `json:"model,omitempty"`  // embedding model the points were made with
	Format int              `json:"format,omitempty"` // Format the points were made with
	Files  map[string]Entry `json:"files"`
}

func newState(root string) *State {
	return &State{root: root, Files: map[string]Entry{}}
}

func statePath(root string) string { return filepath.Join(Dir(root), "state.json") }

// LoadState reads the snapshot for root. A missing file is an empty state;
// a corrupt one returns an empty state and the error.
func LoadState(root string) (*State, error) {
	s := newState(root)
	data, err := readIndexFile(statePath(root))
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return s, nil
		}
		return s, err
	}
	if err := json.Unmarshal(data, s); err != nil {
		return newState(root), err
	}
	if s.Files == nil {
		s.Files = map[string]Entry{}
	}
	return s, nil
}

// StateModel returns the embedding model the root's points were made with,
// "" when no state says.
func StateModel(root string) string {
	data, err := readIndexFile(statePath(root))
	if err != nil {
		return ""
	}
	var s struct {
		Model string `json:"model"`
	}
	_ = json.Unmarshal(data, &s)
	return s.Model
}

// maxIndexFile caps what readIndexFile reads.
const maxIndexFile = 1 << 30

// readIndexFile reads an index file: only a regular file in a real
// directory, at most maxIndexFile bytes, so a symlink to /dev/zero or out
// of the tree is refused.
func readIndexFile(path string) ([]byte, error) {
	if fi, err := os.Lstat(filepath.Dir(path)); err != nil {
		return nil, err
	} else if !fi.IsDir() {
		return nil, fmt.Errorf("index: %s is not a directory", filepath.Dir(path))
	}
	if fi, err := os.Lstat(path); err != nil {
		return nil, err
	} else if !fi.Mode().IsRegular() {
		return nil, fmt.Errorf("index: %s is not a regular file", path)
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, maxIndexFile+1))
	if err == nil && len(data) > maxIndexFile {
		err = fmt.Errorf("index: %s is over %d bytes", path, maxIndexFile)
	}
	return data, err
}

// Save writes the snapshot atomically.
func (s *State) Save() error {
	data, err := json.Marshal(s)
	if err != nil {
		return err
	}
	return WriteAtomic(statePath(s.root), data)
}

// WriteAtomic writes data to a temp file in the same directory and renames
// it over path, so readers never see a truncated file and a symlink at path
// is replaced, not followed.
func WriteAtomic(path string, data []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, filepath.Base(path)+".tmp-*")
	if err != nil {
		return err
	}
	_, werr := tmp.Write(data)
	cerr := tmp.Close()
	if werr == nil {
		werr = cerr
	}
	if werr == nil {
		werr = os.Chmod(tmp.Name(), 0o644)
	}
	if werr == nil {
		werr = os.Rename(tmp.Name(), path)
	}
	if werr != nil {
		os.Remove(tmp.Name())
	}
	return werr
}

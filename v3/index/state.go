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

// State is the per-root snapshot stored at <root>/.ragcode/state.json. Only
// the run goroutine touches it; nothing here locks.
type State struct {
	root  string
	Model string           `json:"model,omitempty"` // embedding model the points were made with
	Files map[string]Entry `json:"files"`
}

func newState(root string) *State {
	return &State{root: root, Files: map[string]Entry{}}
}

func statePath(root string) string { return filepath.Join(root, ".ragcode", "state.json") }

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

// maxIndexFile caps what readIndexFile reads.
const maxIndexFile = 1 << 30

// readIndexFile reads a file under <root>/.ragcode, which a cloned repository
// may have committed: only a regular file in a real directory, at most
// maxIndexFile bytes, so a symlink to /dev/zero or out of the tree is refused.
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

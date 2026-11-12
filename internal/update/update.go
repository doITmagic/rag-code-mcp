// Package update finds newer rag-code-mcp releases and installs them over
// the installed binary: the release check of rag_server_info and the
// automatic update of the server.
package update

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/doITmagic/rag-code-mcp/v2/index"
	"github.com/doITmagic/rag-code-mcp/v2/internal/config"
)

// GitHubLatest is the GitHub API endpoint of the latest (non pre-release) release.
const GitHubLatest = "https://api.github.com/repos/doITmagic/rag-code-mcp/releases/latest"

const maxArchive = 256 << 20 // a release archive is ~40 MB

type cache struct {
	Latest    string    `json:"latest"`
	CheckedAt time.Time `json:"checked_at"`
}

type release struct {
	TagName string `json:"tag_name"`
	Assets  []struct {
		Name string `json:"name"`
		URL  string `json:"browser_download_url"`
	} `json:"assets"`
}

// started is the installed binary this process runs, as it was at start.
var started struct {
	path string
	fi   os.FileInfo
}

// Remember records path as the binary this process started from (a missing
// path clears it). Call it once, before serving.
func Remember(path string) {
	started.path, started.fi = path, nil
	if fi, err := os.Stat(path); err == nil {
		started.fi = fi
	}
}

// Replaced reports whether the remembered binary was replaced since the
// start (by an update or a reinstall): this process still runs the old one.
func Replaced() bool {
	if started.fi == nil {
		return false
	}
	fi, err := os.Stat(started.path)
	return err == nil && (!os.SameFile(fi, started.fi) || !fi.ModTime().Equal(started.fi.ModTime()))
}

// RestartNote is the sentence tool answers carry once Replaced is true.
const RestartNote = "RagCode was updated on disk: restart this MCP server to use the new version (Claude Code: /mcp, then reconnect ragcode)"

// Cached returns the release tag of the last check ("" when it failed or
// never ran) and whether that check is fresh: under a day old, or under an
// hour for a failed one.
func Cached() (latest string, fresh bool) {
	var ch cache
	data, err := os.ReadFile(filepath.Join(config.HomeDir(), "cache", "update.json"))
	if err != nil || json.Unmarshal(data, &ch) != nil || ch.CheckedAt.IsZero() {
		return "", false
	}
	ttl := 24 * time.Hour
	if ch.Latest == "" {
		ttl = time.Hour
	}
	return ch.Latest, time.Since(ch.CheckedAt) < ttl
}

// Latest returns the latest release tag at url, from HomeDir()/cache/update.json
// when checked in the last 24 h (1 h after a failed check, cached with an
// empty tag), otherwise from url. "" means unknown.
func Latest(ctx context.Context, c *http.Client, url string) string {
	path := filepath.Join(config.HomeDir(), "cache", "update.json")
	if l, fresh := Cached(); fresh {
		return l
	}
	var rel release
	if err := getJSON(ctx, c, url, &rel); err != nil {
		rel.TagName = ""
	}
	// A canceled request says nothing about the release; don't cache it.
	if ctx.Err() == nil {
		data, _ := json.Marshal(cache{Latest: rel.TagName, CheckedAt: time.Now()})
		_ = index.WriteAtomic(path, data)
	}
	return rel.TagName
}

// Less reports a < b for "v1.2.3"-style tags; a pre-release is older than
// its final release; anything unparsable (dev builds) is never considered
// outdated.
func Less(a, b string) bool {
	parse := func(s string) ([3]int, bool) {
		var v [3]int
		s, _, _ = strings.Cut(strings.TrimPrefix(s, "v"), "-")
		s, _, _ = strings.Cut(s, "+") // build metadata: v2.0.0-rc1+b6f25e7
		parts := strings.Split(s, ".")
		if len(parts) != 3 {
			return v, false
		}
		for i, p := range parts {
			n, err := strconv.Atoi(p)
			if err != nil {
				return v, false
			}
			v[i] = n
		}
		return v, true
	}
	x, okA := parse(a)
	y, okB := parse(b)
	if !okA || !okB {
		return false
	}
	for i := range x {
		if x[i] != y[i] {
			return x[i] < y[i]
		}
	}
	a, _, _ = strings.Cut(a, "+") // build metadata may hold a hyphen
	b, _, _ = strings.Cut(b, "+")
	return strings.Contains(a, "-") && !strings.Contains(b, "-") // shortcut: two pre-releases of one version compare equal; order them if testers run several
}

// Run installs a newer release from source over target, holding
// HomeDir()/cache/update.lock so concurrent servers do not race. It returns
// the installed version, or "" when there is nothing newer or another
// process holds the lock.
func Run(ctx context.Context, source, target, current string) (string, error) {
	lockPath := filepath.Join(config.HomeDir(), "cache", "update.lock")
	if err := os.MkdirAll(filepath.Dir(lockPath), 0o755); err != nil {
		return "", err
	}
	unlock, err := index.LockFile(lockPath)
	if errors.Is(err, index.ErrLocked) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	defer unlock()
	return Install(ctx, source, target, current)
}

// Install puts the rag-code-mcp of a newer release at target. source is a
// GitHub latest-release API URL (or a server answering the same JSON), or a
// directory holding the release archive and checksums.txt. The archive must
// match its SHA-256 in checksums.txt, and the extracted binary must run and
// report a version newer than current (`-version`); otherwise target is left
// alone. It returns the installed version, or "" when source has nothing newer.
func Install(ctx context.Context, source, target, current string) (string, error) {
	asset := AssetName(runtime.GOOS, runtime.GOARCH)
	archive, sums, err := fetch(ctx, source, asset, current)
	if err != nil || archive == nil {
		return "", err
	}
	if err := verify(archive, sums, asset); err != nil {
		return "", err
	}
	bin, err := extract(archive, asset, binaryName())
	if err != nil {
		return "", err
	}
	tmp, err := os.CreateTemp(filepath.Dir(target), ".rag-code-mcp-update-*"+filepath.Ext(target))
	if err != nil {
		return "", err
	}
	defer os.Remove(tmp.Name())
	_, err = tmp.Write(bin)
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Chmod(tmp.Name(), 0o755)
	}
	if err != nil {
		return "", err
	}
	vctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	out, err := exec.CommandContext(vctx, tmp.Name(), "-version").Output()
	if err != nil {
		return "", fmt.Errorf("update: new binary does not run: %w", err)
	}
	v := strings.TrimPrefix(strings.TrimSpace(string(out)), "rag-code-mcp ")
	if !Less(current, v) {
		return "", nil // also refuses a downgrade
	}
	if err := Replace(tmp.Name(), target); err != nil {
		return "", err
	}
	return v, nil
}

// AssetName is the release archive of a platform, as goreleaser names it.
func AssetName(goos, goarch string) string {
	if goos == "windows" {
		return "rag-code-mcp_" + goos + "_" + goarch + ".zip"
	}
	return "rag-code-mcp_" + goos + "_" + goarch + ".tar.gz"
}

func binaryName() string {
	if runtime.GOOS == "windows" {
		return "rag-code-mcp.exe"
	}
	return "rag-code-mcp"
}

// fetch returns the archive and checksums.txt from source; nil archive when
// a GitHub-style source has no newer tag than current.
func fetch(ctx context.Context, source, asset, current string) (archive, sums []byte, err error) {
	if fi, err := os.Stat(source); err == nil && fi.IsDir() {
		if archive, err = os.ReadFile(filepath.Join(source, asset)); err != nil {
			return nil, nil, fmt.Errorf("update: %w", err)
		}
		if sums, err = os.ReadFile(filepath.Join(source, "checksums.txt")); err != nil {
			return nil, nil, fmt.Errorf("update: %w", err)
		}
		return archive, sums, nil
	}
	c := &http.Client{Timeout: 5 * time.Minute}
	var rel release
	if err := getJSON(ctx, c, source, &rel); err != nil {
		return nil, nil, fmt.Errorf("update: %w", err)
	}
	if !Less(current, rel.TagName) {
		return nil, nil, nil
	}
	urls := map[string]string{}
	for _, a := range rel.Assets {
		urls[a.Name] = a.URL
	}
	if urls[asset] == "" || urls["checksums.txt"] == "" {
		return nil, nil, fmt.Errorf("update: release %s has no %s or checksums.txt", rel.TagName, asset)
	}
	if archive, err = get(ctx, c, urls[asset]); err != nil {
		return nil, nil, err
	}
	if sums, err = get(ctx, c, urls["checksums.txt"]); err != nil {
		return nil, nil, err
	}
	return archive, sums, nil
}

func verify(archive, sums []byte, asset string) error {
	got := sha256.Sum256(archive)
	for line := range strings.SplitSeq(string(sums), "\n") {
		if f := strings.Fields(line); len(f) == 2 && strings.TrimPrefix(f[1], "*") == asset {
			if !strings.EqualFold(f[0], hex.EncodeToString(got[:])) {
				return fmt.Errorf("update: %s does not match checksums.txt", asset)
			}
			return nil
		}
	}
	return fmt.Errorf("update: checksums.txt has no line for %s", asset)
}

// extract returns the file named bin at the root of a .tar.gz or .zip.
func extract(archive []byte, asset, bin string) ([]byte, error) {
	if strings.HasSuffix(asset, ".zip") {
		zr, err := zip.NewReader(bytes.NewReader(archive), int64(len(archive)))
		if err != nil {
			return nil, fmt.Errorf("update: %w", err)
		}
		for _, f := range zr.File {
			if filepath.Base(f.Name) == bin && !f.FileInfo().IsDir() {
				rc, err := f.Open()
				if err != nil {
					return nil, err
				}
				defer rc.Close()
				return io.ReadAll(io.LimitReader(rc, maxArchive))
			}
		}
	} else {
		gz, err := gzip.NewReader(bytes.NewReader(archive))
		if err != nil {
			return nil, fmt.Errorf("update: %w", err)
		}
		tr := tar.NewReader(gz)
		for {
			h, err := tr.Next()
			if err == io.EOF {
				break
			}
			if err != nil {
				return nil, fmt.Errorf("update: %w", err)
			}
			if filepath.Base(h.Name) == bin && h.Typeflag == tar.TypeReg {
				return io.ReadAll(io.LimitReader(tr, maxArchive))
			}
		}
	}
	return nil, fmt.Errorf("update: %s not found in %s", bin, asset)
}

// Replace moves src over dst. Windows cannot overwrite a running binary but
// can rename it, so the old one moves to dst.old first.
func Replace(src, dst string) error {
	if runtime.GOOS != "windows" {
		return os.Rename(src, dst)
	}
	old := dst + ".old"
	_ = os.Remove(old)
	if err := os.Rename(dst, old); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err := os.Rename(src, dst); err != nil {
		_ = os.Rename(old, dst)
		return err
	}
	return nil
}

func getJSON(ctx context.Context, c *http.Client, url string, out any) error {
	body, err := get(ctx, c, url)
	if err != nil {
		return err
	}
	return json.Unmarshal(body, out)
}

func get(ctx context.Context, c *http.Client, url string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s returned %s", url, resp.Status)
	}
	return io.ReadAll(io.LimitReader(resp.Body, maxArchive))
}

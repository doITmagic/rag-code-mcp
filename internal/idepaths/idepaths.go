// Package idepaths knows where every supported IDE keeps its MCP server
// configuration and writes or removes the "ragcode" entry there. The
// installer and the uninstaller share this one table.
package idepaths

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"time"
)

// ErrNotInstalled is returned by Write when the IDE's directory does not
// exist, so the config file is not created for an IDE the user does not have.
var ErrNotInstalled = errors.New("idepaths: IDE not installed")

// Target is one IDE config file.
type Target struct {
	Key  string // short id: cursor, windsurf, claude-code, ...
	Name string // display name
	Path string // config file
	Dir  string // directory whose presence means the IDE is installed
	// Keys is the JSON key path to the servers object ("mcpServers",
	// "servers" or "context_servers"); the ragcode entry lives under it.
	Keys []string

	write  func(t Target, binary, configPath string) error // nil: generic JSON merge
	remove func(t Target) error                            // nil: generic JSON delete
}

// Targets lists every IDE config on this machine's OS, present or not.
func Targets() []Target {
	home, err := os.UserHomeDir()
	if err != nil {
		home = "."
	}
	appData := os.Getenv("APPDATA")
	j := filepath.Join
	cfgDir := j(home, ".config")
	if runtime.GOOS == "darwin" {
		cfgDir = j(home, "Library", "Application Support")
	}
	codexHome := os.Getenv("CODEX_HOME")
	if codexHome == "" {
		codexHome = j(home, ".codex")
	}
	cursor := j(home, ".cursor")
	claudeDesktop, zed, vscode := j(cfgDir, "Claude"), j(home, ".config", "zed"), j(cfgDir, "Code", "User")
	if runtime.GOOS == "windows" && appData != "" {
		cursor, claudeDesktop, zed, vscode = j(appData, "Cursor"), j(appData, "Claude"), j(appData, "Zed"), j(appData, "Code", "User")
	}

	mcp := []string{"mcpServers"}
	ts := []Target{
		{Key: "cursor", Name: "Cursor", Path: j(cursor, "mcp.json"), Keys: mcp},
		{Key: "windsurf", Name: "Windsurf", Path: j(home, ".codeium", "windsurf", "mcp_config.json"), Keys: mcp},
		{Key: "claude-code", Name: "Claude Code", Path: j(home, ".claude.json"), Dir: j(home, ".claude"), Keys: mcp},
		{Key: "claude-desktop", Name: "Claude Desktop", Path: j(claudeDesktop, "claude_desktop_config.json"), Keys: mcp},
		{Key: "vscode", Name: "VS Code", Path: j(vscode, "mcp.json"), Keys: []string{"servers"}},
		{Key: "zed", Name: "Zed", Path: j(zed, "settings.json"), Keys: []string{"context_servers"}, write: writeZed},
		{Key: "cline", Name: "Cline", Path: j(vscode, "globalStorage", "saoudrizwan.claude-dev", "settings", "cline_mcp_settings.json"), Keys: mcp},
		{Key: "roo", Name: "Roo Code", Path: j(vscode, "globalStorage", "rooveterinaryinc.roo-cline", "settings", "cline_mcp_settings.json"), Keys: mcp},
		{Key: "continue", Name: "Continue", Path: j(home, ".continue", "mcpServers", "ragcode.json"), Dir: j(home, ".continue"), Keys: mcp},
		{Key: "gemini", Name: "Gemini CLI", Path: j(home, ".gemini", "settings.json"), Keys: mcp},
		{Key: "codex", Name: "OpenAI Codex", Path: j(codexHome, "config.toml"), write: writeCodex, remove: removeCodex},
		{Key: "antigravity", Name: "Antigravity", Path: j(home, ".gemini", "config", "mcp_config.json"), Keys: mcp},
		{Key: "copilot", Name: "GitHub Copilot CLI", Path: j(home, ".copilot", "mcp-config.json"), Keys: mcp},
		{Key: "openhands", Name: "OpenHands", Path: j(home, ".openhands", "mcp.json"), Keys: mcp},
	}
	for i := range ts {
		if ts[i].Dir == "" {
			ts[i].Dir = filepath.Dir(ts[i].Path)
		}
	}
	return ts
}

// Write merges {"ragcode":{"command":binary,"args":["--config",configPath]}}
// into the IDE config, preserving every other key. It returns ErrNotInstalled
// when the IDE directory is missing and an error when the file is not valid
// JSON (nothing is overwritten then).
func (t Target) Write(binary, configPath string) error {
	if st, err := os.Stat(t.Dir); err != nil || !st.IsDir() {
		return ErrNotInstalled
	}
	if t.write != nil {
		return t.write(t, binary, configPath)
	}
	return t.edit(true, func(servers map[string]any) {
		entry := map[string]any{"command": binary, "args": []any{"--config", configPath}}
		if prev, ok := servers["ragcode"].(map[string]any); ok {
			for k, v := range prev {
				switch k {
				case "command", "args", "url", "type": // transport fields are ours
				case "env":
					if env := v1Env(v); env != nil {
						if len(env) > 0 {
							entry[k] = env
						}
					} else {
						entry[k] = v
					}
				default:
					entry[k] = v
				}
			}
		}
		servers["ragcode"] = entry
	})
}

// v1Env returns env without the keys v1's installer wrote, when env is v1's
// (it has QDRANT_URL, which v3 never reads); nil when it is not. Left in
// place, v1's OLLAMA_EMBED would override the model of config.yaml.
func v1Env(v any) map[string]any {
	env, ok := v.(map[string]any)
	if !ok {
		return nil
	}
	if _, v1 := env["QDRANT_URL"]; !v1 {
		return nil
	}
	out := map[string]any{}
	for k, val := range env {
		switch k {
		case "QDRANT_URL", "OLLAMA_BASE_URL", "OLLAMA_MODEL", "OLLAMA_EMBED":
		default:
			out[k] = val
		}
	}
	return out
}

// Remove deletes the ragcode entry. A missing file or entry is not an error.
func (t Target) Remove() error {
	if t.remove != nil {
		return t.remove(t)
	}
	if _, err := os.Stat(t.Path); err != nil {
		return nil
	}
	return t.edit(false, func(servers map[string]any) { delete(servers, "ragcode") })
}

// edit loads the file (empty object when missing), applies fn to the servers
// object under t.Keys and writes the result back when it changed. With create
// false a missing servers key means there is nothing to edit.
func (t Target) edit(create bool, fn func(servers map[string]any)) error {
	root, err := readJSON(t.Path)
	if err != nil {
		return err
	}
	before, _ := json.Marshal(root)
	node := root
	for _, k := range t.Keys {
		child, ok := node[k].(map[string]any)
		if !ok {
			if node[k] != nil {
				return fmt.Errorf("idepaths: %s: %q is not an object", t.Path, k)
			}
			if !create {
				return nil
			}
			child = map[string]any{}
			node[k] = child
		}
		node = child
	}
	fn(node)
	after, err := json.MarshalIndent(root, "", "  ")
	if err != nil {
		return err
	}
	if b, _ := json.Marshal(root); bytes.Equal(b, before) {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(t.Path), 0o755); err != nil {
		return err
	}
	return writeAtomic(t.Path, append(after, '\n'))
}

// writeAtomic replaces path through a temp file and a rename, so a full disk
// or a crash mid-write cannot truncate the user's whole IDE config. It writes
// to a symlink's target (dotfiles stay linked) and keeps the file's mode.
func writeAtomic(path string, data []byte) error {
	if real, err := filepath.EvalSymlinks(path); err == nil {
		path = real
	}
	mode := os.FileMode(0o644)
	if info, err := os.Stat(path); err == nil {
		mode = info.Mode().Perm()
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+"-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Chmod(mode); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

// readJSON returns the file as an object; a missing or blank file is an empty
// object. A UTF-8 BOM is tolerated; numbers are kept verbatim.
func readJSON(path string) (map[string]any, error) {
	data, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	data = bytes.TrimPrefix(data, []byte("\xef\xbb\xbf"))
	if len(bytes.TrimSpace(data)) == 0 {
		return map[string]any{}, nil
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	m := map[string]any{}
	if err := dec.Decode(&m); err != nil {
		return nil, fmt.Errorf("idepaths: %s is not valid JSON: %w", path, err)
	}
	if m == nil {
		return nil, fmt.Errorf("idepaths: %s is not a JSON object", path)
	}
	if dec.Decode(new(any)) != io.EOF {
		return nil, fmt.Errorf("idepaths: %s has trailing content", path)
	}
	return m, nil
}

// writeZed uses Zed's own entry shape under "context_servers".
func writeZed(t Target, binary, configPath string) error {
	return t.edit(true, func(servers map[string]any) {
		servers["ragcode"] = map[string]any{
			"source":  "custom",
			"command": binary,
			"args":    []any{"--config", configPath},
		}
	})
}

// Codex owns its TOML, so its CLI edits it: "codex mcp add|remove ragcode".
func writeCodex(t Target, binary, configPath string) error {
	return codex(t, "mcp", "add", "ragcode", "--", binary, "--config", configPath)
}

func removeCodex(t Target) error {
	if _, err := os.Stat(t.Path); err != nil {
		return nil
	}
	return codex(t, "mcp", "remove", "ragcode")
}

func codex(t Target, args ...string) error {
	cli := codexCLI()
	if cli == "" {
		return errors.New("idepaths: codex CLI not found")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, cli, args...)
	cmd.Env = append(os.Environ(), "CODEX_HOME="+filepath.Dir(t.Path))
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("idepaths: codex %s: %w (%s)", args[1], err, bytes.TrimSpace(out))
	}
	return nil
}

// codexCLI finds codex on PATH or bundled in an editor's ChatGPT extension.
func codexCLI() string {
	if cli, err := exec.LookPath("codex"); err == nil {
		return cli
	}
	home, _ := os.UserHomeDir()
	arch := map[string]string{"amd64": "x86_64", "arm64": "aarch64"}[runtime.GOARCH]
	bin := "codex"
	if runtime.GOOS == "windows" {
		bin += ".exe"
	}
	for _, editor := range []string{".vscode", ".vscode-insiders", ".cursor", ".antigravity"} {
		m, _ := filepath.Glob(filepath.Join(home, editor, "extensions", "openai.chatgpt-*", "bin", runtime.GOOS+"-"+arch, bin))
		if len(m) > 0 {
			return m[len(m)-1]
		}
	}
	return ""
}

package main

import (
	"encoding/json"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// An unparseable config must be left untouched rather than replaced by a file
// holding only ragcode, which silently deleted every other MCP server.
func TestUpdateMCPConfigKeepsInvalidFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "mcp.json")
	orig := []byte(`{"servers": {"other": {}},}`) // trailing comma
	if err := os.WriteFile(path, orig, 0o644); err != nil {
		t.Fatal(err)
	}
	if updateMCPConfig("vs-code", "VS Code", path, "/bin", "stdio", 3000) {
		t.Fatal("expected update to be skipped")
	}
	got, _ := os.ReadFile(path)
	if string(got) != string(orig) {
		t.Fatalf("file was modified:\n%s", got)
	}
}

func TestRequestedIntegrationsPreserveOtherSettings(t *testing.T) {
	for _, client := range []string{"claude", "claude-cli", "antigravity"} {
		t.Run(client, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "config.json")
			original := `{"counter":9007199254740993,"mcpServers":{"other":{"command":"keep"}},"projects":{"A":{},"a":{}}}`
			if err := os.WriteFile(path, []byte(original), 0600); err != nil {
				t.Fatal(err)
			}
			for i := 0; i < 2; i++ {
				if !updateMCPConfig(client, client, path, "/installed/rag-code-mcp", "stdio", 39000) {
					t.Fatal("configure failed")
				}
			}
			cfg, ok := readJSONConfig(path)
			if !ok {
				t.Fatal("invalid config")
			}
			servers := cfg["mcpServers"].(map[string]interface{})
			if len(servers) != 2 || servers["other"].(map[string]interface{})["command"] != "keep" || cfg["counter"].(json.Number).String() != "9007199254740993" {
				t.Fatal("unrelated settings changed")
			}
			entry := servers["ragcode"].(map[string]interface{})
			if len(entry["args"].([]interface{})) != 2 {
				t.Fatal("missing explicit config path")
			}
			if client == "claude-cli" {
				if !updateMCPConfig(client, client, path, "/installed/rag-code-mcp", "sse", 39000) {
					t.Fatal("HTTP configure failed")
				}
				cfg, _ = readJSONConfig(path)
				if cfg["mcpServers"].(map[string]interface{})["ragcode"].(map[string]interface{})["type"] != "http" {
					t.Fatal("Claude HTTP transport missing")
				}
			}
		})
	}
}

func TestCodexNativeRegistrationIsIdempotent(t *testing.T) {
	if _, err := exec.LookPath("codex"); err != nil {
		t.Skip("Codex CLI required for native integration test")
	}
	path := filepath.Join(t.TempDir(), "config.toml")
	original := "# preserve this comment\n[mcp_servers.other]\ncommand = 'keep'\n"
	if err := os.WriteFile(path, []byte(original), 0600); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if !updateMCPConfig("codex", "Codex", path, "/installed/rag-code-mcp", "stdio", 39000) {
			t.Fatal("configure failed")
		}
	}
	data, _ := os.ReadFile(path)
	if !strings.Contains(string(data), "[mcp_servers.other]\ncommand = \"keep\"") || strings.Count(string(data), "[mcp_servers.ragcode]") != 1 {
		t.Fatalf("unexpected config: %s", data)
	}
	invalid := []byte("broken = [")
	if err := os.WriteFile(path, invalid, 0600); err != nil {
		t.Fatal(err)
	}
	if updateMCPConfig("codex", "Codex", path, "/installed/rag-code-mcp", "stdio", 39000) {
		t.Fatal("invalid TOML accepted")
	}
	data, _ = os.ReadFile(path)
	if string(data) != string(invalid) {
		t.Fatal("invalid TOML overwritten")
	}
}

func TestIntegrationPaths(t *testing.T) {
	t.Setenv("CODEX_HOME", "")
	home := t.TempDir()
	paths := resolveIDEPaths(home)
	if paths["codex"].path != filepath.Join(home, ".codex", "config.toml") || paths["antigravity"].path != filepath.Join(home, ".gemini", "config", "mcp_config.json") || paths["claude-cli"].path != filepath.Join(home, ".claude.json") {
		t.Fatal("wrong integration paths")
	}
	if !normalizeIdeSelection([]string{"claude"}).explicit["claude-cli"] {
		t.Fatal("Claude Code not selected")
	}
}

func TestAutoConfigurationUsesDetectedClientDirectories(t *testing.T) {
	home := t.TempDir()
	t.Setenv("USERPROFILE", home)
	t.Setenv("HOME", home)
	t.Setenv("APPDATA", filepath.Join(home, "AppData"))
	t.Setenv("CODEX_HOME", filepath.Join(home, ".codex"))
	paths := resolveIDEPaths(home)
	for _, key := range []string{"antigravity", "claude", "codex"} {
		if err := os.MkdirAll(filepath.Dir(paths[key].path), 0700); err != nil {
			t.Fatal(err)
		}
	}
	configureIDEs([]string{"auto"}, filepath.Join(home, ".ragcode", "bin"), "stdio", 39000)
	for _, key := range []string{"antigravity", "claude-cli", "claude"} {
		if _, err := os.Stat(paths[key].path); err != nil {
			t.Fatalf("%s not auto-configured: %v", key, err)
		}
	}
	if _, err := exec.LookPath("codex"); err == nil {
		if _, err := os.Stat(paths["codex"].path); err != nil {
			t.Fatal(err)
		}
	}
}

// A UTF-8 BOM is valid JSON-with-BOM as written by Windows tools; existing
// servers must survive and ragcode go under the key the file already uses.
func TestUpdateMCPConfigToleratesBOM(t *testing.T) {
	path := filepath.Join(t.TempDir(), "mcp.json")
	if err := os.WriteFile(path, []byte("\xef\xbb\xbf"+`{"servers": {"other": {"command": "x"}}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if !updateMCPConfig("vs-code", "VS Code", path, "/bin", "stdio", 3000) {
		t.Fatal("expected update to succeed")
	}
	data, _ := os.ReadFile(path)
	var cfg struct {
		Servers map[string]any `json:"servers"`
	}
	if err := json.Unmarshal(data, &cfg); err != nil {
		t.Fatal(err)
	}
	if cfg.Servers["other"] == nil || cfg.Servers["ragcode"] == nil {
		t.Fatalf("want both servers, got %v", cfg.Servers)
	}
}

// Only the stdio adapter starts the daemon, so with the default transport a
// running daemon must not turn IDE entries into URLs that stop working once
// the daemon is gone (reboot, upgrade).
func TestAutoTransportStaysStdioWhileDaemonRuns(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	if got := resolveTransport("auto", ln.Addr().(*net.TCPAddr).Port); got != "stdio" {
		t.Fatalf("auto with a running daemon = %q, want stdio", got)
	}
}

// With --transport sse and no --sse-port, IDEs must point at the port the
// daemon listens on by default (rag-code-mcp --http-port 39000).
func TestSSETransportDefaultsToDaemonPort(t *testing.T) {
	path := filepath.Join(t.TempDir(), "mcp.json")
	if !updateMCPConfig("cursor", "Cursor", path, "/bin/rag-code-mcp", "sse", *ssePortFlag) {
		t.Fatal("configure failed")
	}
	cfg, _ := readJSONConfig(path)
	entry := cfg["mcpServers"].(map[string]interface{})["ragcode"].(map[string]interface{})
	if entry["url"] != "http://localhost:39000/mcp" {
		t.Fatalf("url = %v, want the daemon's default port 39000", entry["url"])
	}
}

// VS Code reads MCP servers from "servers" only; an entry under "mcpServers"
// in a freshly created mcp.json is never loaded.
func TestVSCodeMCPConfigCreatedWithServersKey(t *testing.T) {
	home := isolate(t)
	t.Setenv("APPDATA", filepath.Join(home, "AppData"))
	configureIDEs([]string{"vs-code"}, filepath.Join(home, ".ragcode", "bin"), "stdio", 39000)

	path := resolveIDEPaths(home)["vs-code"].path
	cfg, ok := readJSONConfig(path)
	if !ok {
		t.Fatalf("cannot read %s", path)
	}
	servers, _ := cfg["servers"].(map[string]interface{})
	if servers["ragcode"] == nil || cfg["mcpServers"] != nil {
		t.Fatalf("ragcode not under \"servers\": %v", cfg)
	}
}

// The Zed entry must start ragcode from config.yaml like the other IDEs: env
// pinning the model and service URLs overrode the user's config.yaml in every
// daemon Zed started.
func TestZedStdioEntryUsesInstalledConfig(t *testing.T) {
	home := isolate(t)
	binDir := filepath.Join(home, ".ragcode", "bin")
	configureIDEs([]string{"zed"}, binDir, "stdio", 39000)

	path := resolveIDEPaths(home)["zed"].path
	cfg, ok := readJSONConfig(path)
	if !ok {
		t.Fatalf("cannot read %s", path)
	}
	command := cfg["context_servers"].(map[string]interface{})["ragcode"].(map[string]interface{})["command"].(map[string]interface{})
	if command["env"] != nil {
		t.Fatalf("Zed entry overrides config.yaml through env: %v", command["env"])
	}
	args, _ := command["args"].([]interface{})
	if len(args) != 2 || args[0] != "--config" || args[1] != filepath.Join(binDir, "config.yaml") {
		t.Fatalf("Zed entry args = %v, want --config %s", args, filepath.Join(binDir, "config.yaml"))
	}
}

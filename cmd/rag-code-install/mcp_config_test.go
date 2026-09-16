package main

import (
	"encoding/json"
	"os"
	"path/filepath"
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

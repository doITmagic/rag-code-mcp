package tests

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/doITmagic/rag-code-mcp/internal/service/tools"
	golang "github.com/doITmagic/rag-code-mcp/pkg/parser/go"
)

func TestExactSearchFindsPackageQualifiedGoFunction(t *testing.T) {
	dir, store := indexFixture(t, "go", golang.NewCodeAnalyzer(), map[string]string{
		"cache.go": "package cache\n\nfunc Load() string { return \"loaded\" }\n\ntype Disk struct{}\n\nfunc (Disk) Load() string { return \"disk\" }\n",
	})
	eng := setupTestEngine(store)
	t.Cleanup(eng.Close)
	exact := true
	text, err := tools.NewSmartSearchTool(eng).Execute(context.Background(), tools.SmartSearchInput{Query: "cache.Load", FilePath: filepath.Join(dir, "cache.go"), ExactSymbol: &exact})
	if err != nil {
		t.Fatal(err)
	}
	var response struct{ Data []map[string]interface{} }
	if err := json.Unmarshal([]byte(text), &response); err != nil {
		t.Fatal(err)
	}
	if len(response.Data) != 1 || response.Data[0]["type"] != "function" {
		t.Fatalf("cache.Load want only the package function: %s", text)
	}
}

// A full import path names one package; a.Client.Get's callers exclude
// b.Client.Twice, which calls b.Client.Get on its own receiver.
func TestGoImportPathQualifiesSymbol(t *testing.T) {
	dir, store := indexFixture(t, "go", golang.NewCodeAnalyzer(), map[string]string{
		"go.mod":      "module example.com/edge\n\ngo 1.22\n",
		"a/client.go": "package a\n\ntype Client struct{}\n\nfunc (c *Client) Get() {}\n\nfunc Fetch(c *Client) { c.Get() }\n",
		"b/client.go": "package b\n\ntype Client struct{}\n\nfunc (c *Client) Get() {}\n\nfunc (c *Client) Twice() { c.Get() }\n",
	})
	eng := setupTestEngine(store)
	t.Cleanup(eng.Close)
	path := filepath.Join(dir, "go.mod")
	exact := true
	for query, want := range map[string]int{
		"example.com/edge/a.Client.Get": 1, "example.com/edge/a.Fetch": 1, "example.com/other/a.Client.Get": 0,
	} {
		text, err := tools.NewSmartSearchTool(eng).Execute(context.Background(), tools.SmartSearchInput{Query: query, FilePath: path, ExactSymbol: &exact})
		if err != nil {
			t.Fatal(err)
		}
		var response struct{ Data []map[string]interface{} }
		if err := json.Unmarshal([]byte(text), &response); err != nil {
			t.Fatal(err)
		}
		if len(response.Data) != want {
			t.Errorf("%s want %d symbols: %s", query, want, text)
		}
	}
	text, err := tools.NewFindUsagesTool(eng).Execute(context.Background(), map[string]interface{}{"symbol_name": "example.com/edge/a.Client.Get", "file_path": path})
	if err != nil {
		t.Fatal(err)
	}
	var usages struct{ Data []tools.UsageResult }
	if err := json.Unmarshal([]byte(text), &usages); err != nil {
		t.Fatal(err)
	}
	if len(usages.Data) != 1 || usages.Data[0].Name != "Fetch" {
		t.Errorf("usages want only Fetch: %s", text)
	}
	text, err = tools.NewCallHierarchyTool(eng).Execute(context.Background(), map[string]interface{}{
		"symbol_name": "example.com/edge/a.Client.Get", "direction": "incoming", "file_path": path, "depth": 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	var hierarchy struct{ Data tools.CallNode }
	if err := json.Unmarshal([]byte(text), &hierarchy); err != nil {
		t.Fatal(err)
	}
	if hierarchy.Data.Resolution != "resolved" || len(hierarchy.Data.Children) != 1 {
		t.Errorf("hierarchy want resolved with caller Fetch: %s", text)
	}
}

// An import path may end in a dotted segment, as gopkg.in/yaml.v3 does; the
// package itself is still named yaml.
func TestGoImportPathWithDottedLastSegment(t *testing.T) {
	dir, store := indexFixture(t, "go", golang.NewCodeAnalyzer(), map[string]string{
		"go.mod":  "module gopkg.in/yaml.v3\n\ngo 1.22\n",
		"yaml.go": "package yaml\n\ntype Node struct{}\n\nfunc (n *Node) Encode() {}\n\nfunc Marshal() {}\n",
	})
	eng := setupTestEngine(store)
	t.Cleanup(eng.Close)
	path := filepath.Join(dir, "go.mod")
	exact := true
	for query, want := range map[string]int{
		"gopkg.in/yaml.v3.Marshal": 1, "gopkg.in/yaml.v3.Node.Encode": 1, "gopkg.in/yaml.v2.Marshal": 0,
	} {
		text, err := tools.NewSmartSearchTool(eng).Execute(context.Background(), tools.SmartSearchInput{Query: query, FilePath: path, ExactSymbol: &exact})
		if err != nil {
			t.Fatal(err)
		}
		var response struct{ Data []map[string]interface{} }
		if err := json.Unmarshal([]byte(text), &response); err != nil {
			t.Fatal(err)
		}
		if len(response.Data) != want {
			t.Errorf("%s want %d symbols: %s", query, want, text)
		}
	}
}

// When a type shares its package's name, server.Start reads both as the
// method server.Start and as the package function Start.
func TestPackageNamedTypeKeepsBothReadings(t *testing.T) {
	dir, store := indexFixture(t, "go", golang.NewCodeAnalyzer(), map[string]string{
		"server.go": "package server\n\ntype server struct{}\n\nfunc (s *server) Start() {}\n\nfunc Start() {}\n",
	})
	eng := setupTestEngine(store)
	t.Cleanup(eng.Close)
	path := filepath.Join(dir, "server.go")
	exact := true
	text, err := tools.NewSmartSearchTool(eng).Execute(context.Background(), tools.SmartSearchInput{Query: "server.Start", FilePath: path, ExactSymbol: &exact})
	if err != nil {
		t.Fatal(err)
	}
	var response struct{ Data []map[string]interface{} }
	if err := json.Unmarshal([]byte(text), &response); err != nil {
		t.Fatal(err)
	}
	if len(response.Data) != 2 {
		t.Errorf("server.Start want the method and the function: %s", text)
	}
	text, err = tools.NewCallHierarchyTool(eng).Execute(context.Background(), map[string]interface{}{
		"symbol_name": "server.Start", "direction": "incoming", "file_path": path, "depth": 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	var hierarchy struct{ Status string }
	if err := json.Unmarshal([]byte(text), &hierarchy); err != nil {
		t.Fatal(err)
	}
	if hierarchy.Status != "ambiguous" {
		t.Errorf("server.Start hierarchy want ambiguous: %s", text)
	}
}

// Under its import path, server.Start names the package function: a name
// there starts with its package, so it can select the function the bare
// server.Start shares with the method of type server.
func TestImportPathNameSelectsPackageFunction(t *testing.T) {
	dir, store := indexFixture(t, "go", golang.NewCodeAnalyzer(), map[string]string{
		"go.mod":           "module example.com/edge\n\ngo 1.22\n",
		"server/server.go": "package server\n\ntype server struct{}\n\nfunc (s *server) Start() {}\n\nfunc Start() {}\n",
	})
	eng := setupTestEngine(store)
	t.Cleanup(eng.Close)
	exact := true
	for query, want := range map[string]string{"example.com/edge/server.Start": "function", "example.com/edge/server.server.Start": "method"} {
		text, err := tools.NewSmartSearchTool(eng).Execute(context.Background(), tools.SmartSearchInput{Query: query, FilePath: filepath.Join(dir, "go.mod"), ExactSymbol: &exact})
		if err != nil {
			t.Fatal(err)
		}
		var response struct{ Data []map[string]interface{} }
		if err := json.Unmarshal([]byte(text), &response); err != nil {
			t.Fatal(err)
		}
		if len(response.Data) != 1 || response.Data[0]["type"] != want {
			t.Errorf("%s want only the %s: %s", query, want, text)
		}
	}
}

// (*server).Start names only the method, even when the package has a
// function Start.
func TestPointerReceiverNameIsOnlyTheMethod(t *testing.T) {
	dir, store := indexFixture(t, "go", golang.NewCodeAnalyzer(), map[string]string{
		"server.go": "package server\n\ntype server struct{}\n\nfunc (s *server) Start() {}\n\nfunc Start() {}\n",
	})
	eng := setupTestEngine(store)
	t.Cleanup(eng.Close)
	exact := true
	text, err := tools.NewSmartSearchTool(eng).Execute(context.Background(), tools.SmartSearchInput{Query: "(*server).Start", FilePath: filepath.Join(dir, "server.go"), ExactSymbol: &exact})
	if err != nil {
		t.Fatal(err)
	}
	var response struct{ Data []map[string]interface{} }
	if err := json.Unmarshal([]byte(text), &response); err != nil {
		t.Fatal(err)
	}
	if len(response.Data) != 1 || response.Data[0]["type"] != "method" {
		t.Errorf("(*server).Start want only the method: %s", text)
	}
}

package indexer

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/doITmagic/rag-code-mcp/pkg/parser"
	_ "github.com/doITmagic/rag-code-mcp/pkg/parser/gdscript" // register Godot analyzer
	_ "github.com/doITmagic/rag-code-mcp/pkg/parser/generic"  // register text analyzer
)

func writeTestFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

// A file symlink is never followed: its target may live outside the
// workspace (/etc/passwd, ~/.aws/credentials) and rag_search would return it.
func TestIndexWorkspaceSkipsFileSymlinks(t *testing.T) {
	root, outside := t.TempDir(), t.TempDir()
	writeTestFile(t, filepath.Join(root, "main.go"), "package main\nfunc Kept() {}\n")
	writeTestFile(t, filepath.Join(outside, "credentials"), "aws_secret_access_key = outsideSecretMarker\n")
	writeTestFile(t, filepath.Join(outside, "x.go"), "package main\nfunc OutsideLeakMarker() {}\n")
	for link, target := range map[string]string{"notes.txt": "credentials", "leak.go": "x.go"} {
		if err := os.Symlink(filepath.Join(outside, target), filepath.Join(root, link)); err != nil {
			t.Skipf("symlinks unavailable: %v", err)
		}
	}

	store := &mockStore{}
	svc := NewService(&mockEmbedder{}, store)
	for _, lang := range []string{"go", "generic"} {
		if err := svc.IndexWorkspace(context.Background(), root, lang, Options{Language: lang}); err != nil {
			t.Fatal(err)
		}
	}
	kept := false
	for _, point := range store.upsertPoints {
		content, _ := point.Payload["content"].(string)
		path, _ := point.Payload["file_path"].(string)
		if strings.Contains(content, "outsideSecretMarker") || strings.Contains(content, "OutsideLeakMarker") {
			t.Fatalf("outside content indexed under %s: %q", path, content)
		}
		kept = kept || strings.Contains(content, "func Kept")
	}
	if !kept {
		t.Fatal("regular workspace file was not indexed")
	}
}

// A huge source file (a sparse 3 GB bindata.go) was read whole and OOM-killed
// the daemon, which then resumed the same run and died again on every start.
func TestIndexWorkspaceSkipsOversizedFiles(t *testing.T) {
	old := parser.MaxFileSize
	parser.MaxFileSize = 1 << 10
	t.Cleanup(func() { parser.MaxFileSize = old })
	root := t.TempDir()
	writeTestFile(t, filepath.Join(root, "main.go"), "package main\nfunc Kept() {}\n")
	big := "package main\nfunc OversizedMarker() {}\n" + strings.Repeat("// padding\n", 200)
	writeTestFile(t, filepath.Join(root, "bindata.go"), big)
	writeTestFile(t, filepath.Join(root, "notes.txt"), "OversizedMarker\n"+strings.Repeat("padding\n", 200))

	store := &mockStore{}
	svc := NewService(&mockEmbedder{}, store)
	for _, lang := range []string{"go", "generic"} {
		if err := svc.IndexWorkspace(context.Background(), root, lang, Options{Language: lang}); err != nil {
			t.Fatal(err)
		}
	}
	if len(store.upsertPoints) == 0 {
		t.Fatal("regular workspace file was not indexed")
	}
	for _, point := range store.upsertPoints {
		if content, _ := point.Payload["content"].(string); strings.Contains(content, "OversizedMarker") {
			t.Fatalf("oversized file indexed: %v", point.Payload["file_path"])
		}
	}
}

// An ignored .env holds real secrets; rag_search returned them verbatim.
func TestIndexWorkspaceSkipsGitignoredEnvFiles(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	root := t.TempDir()
	if out, err := exec.Command("git", "init", "-q", root).CombinedOutput(); err != nil {
		t.Fatalf("git init: %v %s", err, out)
	}
	writeTestFile(t, filepath.Join(root, ".gitignore"), ".env\n.env.production\n")
	writeTestFile(t, filepath.Join(root, ".env"), "DB_PASSWORD=ignoredSecretMarker\n")
	writeTestFile(t, filepath.Join(root, ".env.production"), "API_KEY=ignoredSecretMarker\n")
	writeTestFile(t, filepath.Join(root, ".env.example"), "DB_PASSWORD=changeme\n")
	// prod.env and db.env are env files too (the analyzer takes any .env
	// extension), so an ignored one leaked its secrets the same way.
	if err := os.Mkdir(filepath.Join(root, "deploy"), 0o700); err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, filepath.Join(root, "deploy", ".gitignore"), "*.env\n")
	writeTestFile(t, filepath.Join(root, "deploy", "prod.env"), "PROD_DB_PASS=ignoredSecretMarker\n")

	store := &mockStore{}
	svc := NewService(&mockEmbedder{}, store)
	if err := svc.IndexWorkspace(context.Background(), root, "generic", Options{Language: "generic"}); err != nil {
		t.Fatal(err)
	}
	example := false
	for _, point := range store.upsertPoints {
		content, _ := point.Payload["content"].(string)
		if strings.Contains(content, "ignoredSecretMarker") {
			t.Fatalf("gitignored env file indexed: %v", point.Payload["file_path"])
		}
		example = example || strings.Contains(content, "changeme")
	}
	if !example {
		t.Fatal("tracked .env.example was not indexed")
	}
}

// Godot scenes embed meshes and tile data as megabyte-long lines, so a main
// level scene often exceeds the size cap; the scene analyzer streams it and
// keeps only headers, so it must still be indexed.
func TestIndexWorkspaceKeepsLargeGodotScenes(t *testing.T) {
	old := parser.MaxFileSize
	parser.MaxFileSize = 1 << 10
	t.Cleanup(func() { parser.MaxFileSize = old })
	root := t.TempDir()
	scene := "[gd_scene format=3]\n\n[sub_resource type=\"ArrayMesh\" id=\"1\"]\ndata = PackedByteArray(" +
		strings.Repeat("0, ", 4<<10) + ")\n\n[node name=\"LevelMarker\" type=\"Node3D\"]\n"
	writeTestFile(t, filepath.Join(root, "level.tscn"), scene)

	store := &mockStore{}
	svc := NewService(&mockEmbedder{}, store)
	if err := svc.IndexWorkspace(context.Background(), root, "gdscript", Options{Language: "gdscript"}); err != nil {
		t.Fatal(err)
	}
	for _, point := range store.upsertPoints {
		if content, _ := point.Payload["content"].(string); strings.Contains(content, "LevelMarker") {
			return
		}
	}
	t.Fatalf("large Godot scene was not indexed (%d points)", len(store.upsertPoints))
}

// A scene with tens of thousands of nodes produced a point of several MB;
// any Qdrant response holding it exceeded gRPC's 4 MiB limit and rag_search
// failed with ResourceExhausted for the whole collection.
func TestIndexWorkspaceBoundsGodotScenePoint(t *testing.T) {
	root := t.TempDir()
	var scene strings.Builder
	scene.WriteString("[gd_scene format=3]\n\n[node name=\"Root\" type=\"Node2D\"]\n")
	for i := 0; i < 20000; i++ {
		fmt.Fprintf(&scene, "\n[node name=\"N%d\" type=\"Sprite2D\" parent=\".\"]\n", i)
	}
	writeTestFile(t, filepath.Join(root, "many.tscn"), scene.String())

	store := &mockStore{}
	svc := NewService(&mockEmbedder{}, store)
	if err := svc.IndexWorkspace(context.Background(), root, "gdscript", Options{Language: "gdscript"}); err != nil {
		t.Fatal(err)
	}
	if len(store.upsertPoints) == 0 {
		t.Fatal("scene was not indexed")
	}
	for _, point := range store.upsertPoints {
		data, err := json.Marshal(point.Payload)
		if err != nil {
			t.Fatal(err)
		}
		if len(data) > 256<<10 {
			t.Fatalf("scene point payload is %d bytes", len(data))
		}
	}
}

// A '[' in the workspace path or a package directory (proj [v2], Next.js
// routes/[id]) was read as a glob pattern, so its Go files gave 0 symbols.
func TestIndexWorkspaceIndexesGoUnderBracketDirs(t *testing.T) {
	root := filepath.Join(t.TempDir(), "proj [v2]")
	if err := os.MkdirAll(filepath.Join(root, "routes", "[id]"), 0o700); err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, filepath.Join(root, "main.go"), "package main\nfunc BracketMain() {}\n")
	writeTestFile(t, filepath.Join(root, "main_test.go"), "package main\nfunc TestBracketMain() {}\n")
	writeTestFile(t, filepath.Join(root, "routes", "[id]", "r.go"), "package id\nfunc RouteHandler() {}\n")

	store := &mockStore{}
	svc := NewService(&mockEmbedder{}, store)
	if err := svc.IndexWorkspace(context.Background(), root, "go", Options{Language: "go"}); err != nil {
		t.Fatal(err)
	}
	names := map[string]bool{}
	for _, point := range store.upsertPoints {
		name, _ := point.Payload["name"].(string)
		names[name] = true
	}
	for _, want := range []string{"BracketMain", "TestBracketMain", "RouteHandler"} {
		if !names[want] {
			t.Errorf("%s not indexed; got %v", want, names)
		}
	}
}

// A watched file that became skippable (grown past the size cap, now an
// ignored .env, replaced by a symlink) kept its old points, so rag_search
// returned a symbol or secret the file no longer holds until a full run.
func TestIndexFileDropsFileThatBecameSkippable(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "probe.go")
	writeTestFile(t, path, "package main\nfunc GrownProbe() {}\n")
	store := &mockStore{}
	svc := NewService(&mockEmbedder{}, store)
	state := NewState()
	if _, err := svc.IndexFile(context.Background(), "go", path, state); err != nil {
		t.Fatal(err)
	}

	old := parser.MaxFileSize
	parser.MaxFileSize = 1 << 10
	t.Cleanup(func() { parser.MaxFileSize = old })
	writeTestFile(t, path, "package main\nfunc GrownProbe() {}\n"+strings.Repeat("// padding\n", 200))
	store.deletedFilters = nil
	if _, err := svc.IndexFile(context.Background(), "go", path, state); err != nil {
		t.Fatal(err)
	}
	if len(store.deletedFilters) != 1 || store.deletedFilters[0] != path {
		t.Fatalf("old points of the skipped file were not deleted: %v", store.deletedFilters)
	}
	if _, ok := state.GetFileState(path); ok {
		t.Fatal("skipped file is still tracked in state")
	}
}

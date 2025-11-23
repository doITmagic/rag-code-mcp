package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/doITmagic/rag-code-mcp/internal/codetypes"
	"github.com/doITmagic/rag-code-mcp/internal/llm"
	"github.com/doITmagic/rag-code-mcp/internal/memory"
	"github.com/doITmagic/rag-code-mcp/internal/workspace"
)

// mockProvider is a simple mock for llm.Provider used in tests.
type mockProvider struct{}

func (m *mockProvider) Generate(ctx context.Context, prompt string, opts ...llm.GenerateOption) (string, error) {
	return "mock", nil
}

func (m *mockProvider) GenerateStream(ctx context.Context, prompt string, opts ...llm.GenerateOption) (<-chan string, <-chan error) {
	out := make(chan string)
	errCh := make(chan error)
	close(out)
	close(errCh)
	return out, errCh
}

func (m *mockProvider) Embed(ctx context.Context, text string) ([]float64, error) {
	// Return a fixed-size embedding
	return []float64{0.1, 0.2, 0.3}, nil
}

func (m *mockProvider) Name() string { return "mock" }

var _ llm.Provider = (*mockProvider)(nil)

func TestGetCodeContextTool_Validation(t *testing.T) {
	tool := NewGetCodeContextTool()
	ctx := context.Background()

	if _, err := tool.Execute(ctx, map[string]interface{}{}); err == nil {
		t.Fatalf("expected error when file_path is missing")
	}

	if _, err := tool.Execute(ctx, map[string]interface{}{"file_path": "foo"}); err == nil {
		t.Fatalf("expected error when start_line is missing")
	}

	if _, err := tool.Execute(ctx, map[string]interface{}{"file_path": "foo", "start_line": float64(1)}); err == nil {
		t.Fatalf("expected error when end_line is missing")
	}
}

func TestGetCodeContextTool_BasicRange(t *testing.T) {
	tool := NewGetCodeContextTool()
	ctx := context.Background()

	tmpDir := t.TempDir()
	filePath := filepath.Join(tmpDir, "sample.go")
	content := "line1\nline2\nline3\nline4\nline5\n"
	if err := os.WriteFile(filePath, []byte(content), 0o644); err != nil {
		t.Fatalf("failed to write temp file: %v", err)
	}

	out, err := tool.Execute(ctx, map[string]interface{}{
		"file_path":     filePath,
		"start_line":    float64(2),
		"end_line":      float64(3),
		"context_lines": float64(1),
	})
	if err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}

	if !strings.Contains(out, "line2") || !strings.Contains(out, "line3") {
		t.Errorf("expected output to contain main lines 2 and 3, got: %s", out)
	}
}

func TestSearchLocalIndexTool_NoMemoriesConfigured(t *testing.T) {
	tool := NewSearchLocalIndexTool(nil, &mockProvider{})
	ctx := context.Background()

	_, err := tool.Execute(ctx, map[string]interface{}{"query": "test", "file_path": "/tmp/test.go"})
	if err == nil || !strings.Contains(err.Error(), "no long-term memories configured") {
		t.Fatalf("expected error about no long-term memories, got: %v", err)
	}
}

func TestSearchLocalIndexTool_FallbackMemory(t *testing.T) {
	// Prepare in-memory LTM with one document
	ltm := memory.NewInMemoryLongTermMemory()
	ctx := context.Background()
	_ = ltm.Store(ctx, memory.Document{
		ID:      "1",
		Content: "hello world",
	})

	tool := NewSearchLocalIndexTool(ltm, &mockProvider{})

	out, err := tool.Execute(ctx, map[string]interface{}{"query": "hello", "limit": float64(1), "file_path": "/tmp/test.go"})
	if err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}

	var symbols []codetypes.SymbolDescriptor
	if err := json.Unmarshal([]byte(out), &symbols); err != nil {
		t.Fatalf("failed to unmarshal results as JSON: %v", err)
	}
	if len(symbols) == 0 {
		t.Fatalf("expected at least one symbol descriptor")
	}
	if symbols[0].Description == "" || !strings.Contains(symbols[0].Description, "hello world") {
		t.Errorf("expected description to include stored content, got: %+v", symbols[0])
	}
}

func TestSearchDocsTool_NoMemoryConfigured(t *testing.T) {
	tool := NewSearchDocsTool(nil, &mockProvider{})
	ctx := context.Background()

	out, err := tool.Execute(ctx, map[string]interface{}{"query": "docs", "file_path": "/tmp/test.go"})
	if err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}

	if !strings.Contains(out, "Documentation search is not configured") {
		t.Errorf("unexpected message: %s", out)
	}
}

func TestSearchDocsTool_NoEmbedderConfigured(t *testing.T) {
	ltm := memory.NewInMemoryLongTermMemory()
	ctx := context.Background()
	tool := NewSearchDocsTool(ltm, nil)

	out, err := tool.Execute(ctx, map[string]interface{}{"query": "docs", "file_path": "/tmp/test.go"})
	if err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}

	if !strings.Contains(out, "no embedding provider is configured") {
		t.Errorf("unexpected message: %s", out)
	}
}

func TestSearchDocsTool_HappyPath(t *testing.T) {
	ltm := memory.NewInMemoryLongTermMemory()
	ctx := context.Background()
	_ = ltm.Store(ctx, memory.Document{ID: "1", Content: "documentation content"})

	tool := NewSearchDocsTool(ltm, &mockProvider{})

	out, err := tool.Execute(ctx, map[string]interface{}{"query": "docs", "limit": float64(1), "file_path": "/tmp/test.go"})
	if err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}

	if !strings.Contains(out, "documentation content") {
		t.Errorf("expected docs content in result, got: %s", out)
	}
}

func TestHybridSearchTool_NoMemoryConfigured(t *testing.T) {
	tool := NewHybridSearchTool(nil, &mockProvider{})
	ctx := context.Background()

	_, err := tool.Execute(ctx, map[string]interface{}{"query": "test", "file_path": "/tmp/test.go"})
	if err == nil || !strings.Contains(err.Error(), "no long-term memory configured") {
		t.Fatalf("expected error about no long-term memory, got: %v", err)
	}
}

func TestHybridSearchTool_SemanticOnly(t *testing.T) {
	ltm := memory.NewInMemoryLongTermMemory()
	ctx := context.Background()
	_ = ltm.Store(ctx, memory.Document{
		ID:      "1",
		Content: "some code snippet",
		Metadata: map[string]interface{}{
			"score": 0.9,
		},
	})

	tool := NewHybridSearchTool(ltm, &mockProvider{})

	out, err := tool.Execute(ctx, map[string]interface{}{"query": "something", "limit": float64(1), "output_format": "markdown", "file_path": "/tmp/test.go"})
	if err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}

	if !strings.Contains(out, "Hybrid search found 1 snippet(s):") {
		t.Errorf("expected hybrid search header, got: %s", out)
	}
	if !strings.Contains(out, "some code snippet") {
		t.Errorf("expected snippet content in result, got: %s", out)
	}
}

func TestHybridSearchTool_WithLexicalMatches(t *testing.T) {
	ltm := memory.NewInMemoryLongTermMemory()
	ctx := context.Background()
	_ = ltm.Store(ctx, memory.Document{
		ID:      "1",
		Content: "foo bar foo",
		Metadata: map[string]interface{}{
			"score": 0.8,
		},
	})

	tool := NewHybridSearchTool(ltm, &mockProvider{})

	out, err := tool.Execute(ctx, map[string]interface{}{"query": "foo", "limit": float64(1), "output_format": "markdown", "file_path": "/tmp/test.go"})
	if err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}

	if !strings.Contains(out, "Hybrid search found 1 snippet(s):") {
		t.Errorf("expected hybrid search header, got: %s", out)
	}
	if !strings.Contains(out, "foo bar foo") {
		t.Errorf("expected snippet content in result, got: %s", out)
	}
	if !strings.Contains(out, "hybrid") {
		t.Errorf("expected scores in result, got: %s", out)
	}
}

func TestListPackageExportsTool_ValidationAndHappyPath(t *testing.T) {
	ctx := context.Background()

	toolNoPkg := NewListPackageExportsTool(nil, &mockProvider{})
	if _, err := toolNoPkg.Execute(ctx, map[string]interface{}{}); err == nil {
		t.Fatalf("expected error when package is missing")
	}

	ltm := memory.NewInMemoryLongTermMemory()
	chunk := codetypes.CodeChunk{
		Name:      "MyFunc",
		Type:      "function",
		Package:   "mypkg",
		Signature: "MyFunc()",
		Docstring: "Does something",
		FilePath:  "/tmp/file.go",
		StartLine: 10,
	}
	b, err := json.Marshal(chunk)
	if err != nil {
		t.Fatalf("failed to marshal chunk: %v", err)
	}
	_ = ltm.Store(ctx, memory.Document{ID: "1", Content: string(b)})

	tool := NewListPackageExportsTool(ltm, &mockProvider{})

	out, err := tool.Execute(ctx, map[string]interface{}{"package": "mypkg", "file_path": "/tmp/file.go"})
	if err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}

	if !strings.Contains(out, "MyFunc") {
		t.Errorf("expected to list exported function MyFunc, got: %s", out)
	}
}

func TestGetFunctionDetailsTool_HappyPathAndNotFound(t *testing.T) {
	ctx := context.Background()
	ltm := memory.NewInMemoryLongTermMemory()

	chunk := codetypes.CodeChunk{
		Name:      "DoThing",
		Type:      "function",
		Package:   "mypkg",
		Signature: "DoThing()",
		Docstring: "test doc",
		FilePath:  "/tmp/file.go",
		StartLine: 1,
		EndLine:   1,
		Code:      "func DoThing() {}",
	}
	b, err := json.Marshal(chunk)
	if err != nil {
		t.Fatalf("failed to marshal chunk: %v", err)
	}
	_ = ltm.Store(ctx, memory.Document{ID: "1", Content: string(b)})

	tool := NewGetFunctionDetailsTool(ltm, &mockProvider{})

	out, err := tool.Execute(ctx, map[string]interface{}{"function_name": "DoThing", "file_path": "/tmp/file.go"})
	if err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}
	if !strings.Contains(out, "# DoThing") || !strings.Contains(out, "DoThing()") {
		t.Errorf("unexpected output: %s", out)
	}

	outNotFound, err := tool.Execute(ctx, map[string]interface{}{"function_name": "Missing", "file_path": "/tmp/file.go"})
	if err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}
	if !strings.Contains(outNotFound, "not found") {
		t.Errorf("expected not found message, got: %s", outNotFound)
	}
}

func TestFindTypeDefinitionTool_HappyPathAndNotFound(t *testing.T) {
	ctx := context.Background()
	ltm := memory.NewInMemoryLongTermMemory()

	chunk := codetypes.CodeChunk{
		Name:      "MyStruct",
		Type:      "type",
		Package:   "mypkg",
		FilePath:  "/tmp/file.go",
		StartLine: 1,
		EndLine:   1,
		Code:      "type MyStruct struct{}",
	}
	b, err := json.Marshal(chunk)
	if err != nil {
		t.Fatalf("failed to marshal chunk: %v", err)
	}
	_ = ltm.Store(ctx, memory.Document{ID: "1", Content: string(b)})

	tool := NewFindTypeDefinitionTool(ltm, &mockProvider{})

	out, err := tool.Execute(ctx, map[string]interface{}{"type_name": "MyStruct", "file_path": "/tmp/file.go"})
	if err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}
	if !strings.Contains(out, "# MyStruct") || !strings.Contains(out, "Kind:") {
		t.Errorf("unexpected output: %s", out)
	}

	outNotFound, err := tool.Execute(ctx, map[string]interface{}{"type_name": "Missing", "file_path": "/tmp/file.go"})
	if err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}
	if !strings.Contains(outNotFound, "not found") {
		t.Errorf("expected not found message, got: %s", outNotFound)
	}
}

func TestFindImplementationsTool_HappyPath(t *testing.T) {
	ctx := context.Background()
	ltm := memory.NewInMemoryLongTermMemory()

	chunk1 := codetypes.CodeChunk{
		Name:      "Impl1",
		Type:      "function",
		Package:   "mypkg",
		FilePath:  "/tmp/file.go",
		StartLine: 1,
		EndLine:   3,
		Code:      "func Impl1() { Foo(); Foo() }",
	}
	chunk2 := codetypes.CodeChunk{
		Name:      "Impl2",
		Type:      "function",
		Package:   "mypkg",
		FilePath:  "/tmp/file2.go",
		StartLine: 5,
		EndLine:   7,
		Code:      "func Impl2() { Foo() }",
	}

	b1, err := json.Marshal(chunk1)
	if err != nil {
		t.Fatalf("failed to marshal chunk1: %v", err)
	}
	b2, err := json.Marshal(chunk2)
	if err != nil {
		t.Fatalf("failed to marshal chunk2: %v", err)
	}

	_ = ltm.Store(ctx, memory.Document{ID: "1", Content: string(b1)})
	_ = ltm.Store(ctx, memory.Document{ID: "2", Content: string(b2)})

	tool := NewFindImplementationsTool(ltm, &mockProvider{})

	out, err := tool.Execute(ctx, map[string]interface{}{"symbol_name": "Foo", "file_path": "/tmp/file.go"})
	if err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}

	if !strings.Contains(out, "Impl1") || !strings.Contains(out, "Impl2") {
		t.Errorf("expected both implementations in output, got: %s", out)
	}
	if !strings.Contains(out, "Occurrences:") {
		t.Errorf("expected occurrences info in output, got: %s", out)
	}
}

func TestReadFileLines(t *testing.T) {
	ctxDir := t.TempDir()
	filePath := filepath.Join(ctxDir, "file.go")
	content := "a\nb\nc\n"
	if err := os.WriteFile(filePath, []byte(content), 0o644); err != nil {
		t.Fatalf("failed to write file: %v", err)
	}

	lines, err := readFileLines(filePath, 1, 2)
	if err != nil {
		t.Fatalf("readFileLines returned error: %v", err)
	}
	if lines != "a\nb" {
		t.Errorf("expected 'a\\nb', got %q", lines)
	}

	if _, err := readFileLines(filePath, 0, 5); err == nil {
		t.Fatalf("expected error for invalid range, got nil")
	}
}

func TestFindTypeDefinitionTool_PHPUser(t *testing.T) {
	ctx := context.Background()
	ltm := memory.NewInMemoryLongTermMemory()

	userPath := "/home/razvan/go/src/github.com/doITmagic/rag-code-mcp/barou/app/User.php"
	if _, err := os.Stat(userPath); os.IsNotExist(err) {
		t.Skipf("User.php not found at %s, skipping", userPath)
	}

	chunk := codetypes.CodeChunk{
		Name:     "User",
		Type:     "class",
		Language: "php",
		Package:  "App",
		FilePath: userPath,
		// StartLine/EndLine are not critical here; PHP analyzer will reread the file.
	}
	b, err := json.Marshal(chunk)
	if err != nil {
		t.Fatalf("failed to marshal chunk: %v", err)
	}
	_ = ltm.Store(ctx, memory.Document{ID: "php-user", Content: string(b)})

	tool := NewFindTypeDefinitionTool(ltm, &mockProvider{})

	out, err := tool.Execute(ctx, map[string]interface{}{"type_name": "User", "file_path": userPath})
	if err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}

	t.Logf("FindTypeDefinition PHP User output:\n%s", out)
}

func TestGetFunctionDetailsTool_PHPUserRoles(t *testing.T) {
	ctx := context.Background()
	ltm := memory.NewInMemoryLongTermMemory()

	userPath := "/home/razvan/go/src/github.com/doITmagic/rag-code-mcp/barou/app/User.php"
	if _, err := os.Stat(userPath); os.IsNotExist(err) {
		t.Skipf("User.php not found at %s, skipping", userPath)
	}

	chunk := codetypes.CodeChunk{
		Name:     "roles",
		Type:     "method",
		Language: "php",
		Package:  "App",
		FilePath: userPath,
		// Leave StartLine zero so we don't depend on exact line numbers.
	}
	b, err := json.Marshal(chunk)
	if err != nil {
		t.Fatalf("failed to marshal chunk: %v", err)
	}
	_ = ltm.Store(ctx, memory.Document{ID: "php-user-roles", Content: string(b)})

	tool := NewGetFunctionDetailsTool(ltm, &mockProvider{})

	out, err := tool.Execute(ctx, map[string]interface{}{"function_name": "roles", "package": "App", "file_path": userPath})
	if err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}

	t.Logf("GetFunctionDetails PHP User::roles output:\n%s", out)
}

func TestFindTypeDefinitionTool_PHPUser_JSON(t *testing.T) {
	ctx := context.Background()
	ltm := memory.NewInMemoryLongTermMemory()

	userPath := "/home/razvan/go/src/github.com/doITmagic/rag-code-mcp/barou/app/User.php"
	if _, err := os.Stat(userPath); os.IsNotExist(err) {
		t.Skipf("User.php not found at %s, skipping", userPath)
	}

	chunk := codetypes.CodeChunk{
		Name:     "User",
		Type:     "class",
		Language: "php",
		Package:  "App",
		FilePath: userPath,
	}
	b, err := json.Marshal(chunk)
	if err != nil {
		t.Fatalf("failed to marshal chunk: %v", err)
	}
	_ = ltm.Store(ctx, memory.Document{ID: "php-user-json", Content: string(b)})

	tool := NewFindTypeDefinitionTool(ltm, &mockProvider{})

	out, err := tool.Execute(ctx, map[string]interface{}{
		"type_name":     "User",
		"output_format": "json",
		"file_path":     userPath,
	})
	if err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}

	t.Logf("FindTypeDefinition PHP User JSON output:\n%s", out)

	var desc codetypes.ClassDescriptor
	if err := json.Unmarshal([]byte(out), &desc); err != nil {
		t.Fatalf("failed to unmarshal ClassDescriptor JSON: %v", err)
	}

	if desc.Language != "php" {
		t.Errorf("expected language=php, got %q", desc.Language)
	}
	if desc.Name != "User" {
		t.Errorf("expected name=User, got %q", desc.Name)
	}
	if desc.Namespace != "App" {
		t.Errorf("expected namespace=App, got %q", desc.Namespace)
	}
	if desc.Location.FilePath == "" {
		t.Errorf("expected non-empty file path in location")
	}
}

func TestGetFunctionDetailsTool_PHPUserRoles_JSON(t *testing.T) {
	ctx := context.Background()
	ltm := memory.NewInMemoryLongTermMemory()

	userPath := "/home/razvan/go/src/github.com/doITmagic/rag-code-mcp/barou/app/User.php"
	if _, err := os.Stat(userPath); os.IsNotExist(err) {
		t.Skipf("User.php not found at %s, skipping", userPath)
	}

	chunk := codetypes.CodeChunk{
		Name:     "roles",
		Type:     "method",
		Language: "php",
		Package:  "App",
		FilePath: userPath,
	}
	b, err := json.Marshal(chunk)
	if err != nil {
		t.Fatalf("failed to marshal chunk: %v", err)
	}
	_ = ltm.Store(ctx, memory.Document{ID: "php-user-roles-json", Content: string(b)})

	tool := NewGetFunctionDetailsTool(ltm, &mockProvider{})

	out, err := tool.Execute(ctx, map[string]interface{}{
		"function_name": "roles",
		"package":       "App",
		"output_format": "json",
		"file_path":     userPath,
	})
	if err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}

	t.Logf("GetFunctionDetails PHP User::roles JSON output:\n%s", out)

	var desc codetypes.FunctionDescriptor
	if err := json.Unmarshal([]byte(out), &desc); err != nil {
		t.Fatalf("failed to unmarshal FunctionDescriptor JSON: %v", err)
	}

	if desc.Language != "php" {
		t.Errorf("expected language=php, got %q", desc.Language)
	}
	if desc.Name != "roles" {
		t.Errorf("expected name=roles, got %q", desc.Name)
	}
	if desc.Receiver != "User" {
		t.Errorf("expected receiver=User, got %q", desc.Receiver)
	}
	if len(desc.Returns) == 0 {
		t.Errorf("expected at least one return type (including Laravel relation)")
	}
}

func TestListPackageExports_PHPApp(t *testing.T) {
	ctx := context.Background()
	root := "/home/razvan/go/src/github.com/doITmagic/rag-code-mcp/barou"
	if _, err := os.Stat(root); os.IsNotExist(err) {
		t.Skipf("barou workspace not found at %s, skipping", root)
	}

	info := &workspace.Info{Root: root, ProjectType: "php"}

	out, err := listPHPExports(ctx, info, "App", "", "")
	if err != nil {
		t.Fatalf("listPHPExports returned error: %v", err)
	}

	t.Logf("listPHPExports PHP App output:\n%s", out)
}

func TestListPackageExports_PHPApp_JSON(t *testing.T) {
	ctx := context.Background()
	root := "/home/razvan/go/src/github.com/doITmagic/rag-code-mcp/barou"
	if _, err := os.Stat(root); os.IsNotExist(err) {
		t.Skipf("barou workspace not found at %s, skipping", root)
	}

	info := &workspace.Info{Root: root, ProjectType: "php"}

	out, err := listPHPExports(ctx, info, "App", "", "json")
	if err != nil {
		t.Fatalf("listPHPExports (json) returned error: %v", err)
	}

	t.Logf("listPHPExports PHP App JSON output:\n%s", out)

	var symbols []codetypes.SymbolDescriptor
	if err := json.Unmarshal([]byte(out), &symbols); err != nil {
		t.Fatalf("failed to unmarshal SymbolDescriptor list: %v", err)
	}

	if len(symbols) == 0 {
		t.Fatalf("expected at least one exported symbol in JSON output")
	}

	// Sanity check: there should be a User model in App namespace
	foundUser := false
	for _, s := range symbols {
		if s.Name == "User" && s.Language == "php" && s.Namespace == "App" {
			foundUser = true
			break
		}
	}
	if !foundUser {
		t.Errorf("expected to find User symbol in JSON exports for App")
	}
}

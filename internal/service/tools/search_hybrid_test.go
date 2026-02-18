package tools

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/doITmagic/rag-code-mcp/internal/codetypes"
	"github.com/doITmagic/rag-code-mcp/internal/memory"
)

func TestSearchLocalIndexTool_JSONOutput(t *testing.T) {
	ltm := memory.NewInMemoryLongTermMemory()
	ctx := context.Background()

	chunk := codetypes.CodeChunk{
		Name:      "Foo",
		Type:      "function",
		Language:  "go",
		Package:   "mypkg",
		FilePath:  "/tmp/file.go",
		StartLine: 10,
		EndLine:   12,
		Signature: "func Foo()",
		Docstring: "Foo does something",
		Code:      "func Foo() {}",
	}
	b, err := json.Marshal(chunk)
	if err != nil {
		t.Fatalf("failed to marshal chunk: %v", err)
	}
	_ = ltm.Store(ctx, memory.Document{ID: "1", Content: string(b)})

	tool := NewSearchLocalIndexTool(ltm, &mockProvider{})

	out, err := tool.Execute(ctx, map[string]interface{}{
		"query":         "Foo",
		"limit":         float64(1),
		"output_format": "json",
		"file_path":     "/tmp/file.go",
	})
	if err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}

	var symbols []codetypes.SymbolDescriptor
	if err := json.Unmarshal([]byte(out), &symbols); err != nil {
		t.Fatalf("failed to unmarshal SymbolDescriptor list: %v", err)
	}

	if len(symbols) == 0 {
		t.Fatalf("expected at least one symbol in JSON output")
	}

	s := symbols[0]
	if s.Name != "Foo" || s.Language != "go" || s.Package != "mypkg" {
		t.Errorf("unexpected symbol descriptor: %+v", s)
	}
	if s.Metadata == nil {
		t.Fatalf("expected metadata with snippet, got nil")
	}
	if _, ok := s.Metadata["snippet"]; !ok {
		t.Errorf("expected snippet in metadata, got: %+v", s.Metadata)
	}
}

func TestHybridSearchTool_JSONOutput(t *testing.T) {
	ltm := memory.NewInMemoryLongTermMemory()
	ctx := context.Background()

	chunk := codetypes.CodeChunk{
		Name:      "Bar",
		Type:      "function",
		Language:  "php",
		Package:   "App",
		FilePath:  "/tmp/Bar.php",
		StartLine: 5,
		EndLine:   8,
		Signature: "function Bar()",
		Docstring: "Bar function",
		Code:      "function Bar() {}",
	}
	b, err := json.Marshal(chunk)
	if err != nil {
		t.Fatalf("failed to marshal chunk: %v", err)
	}
	_ = ltm.Store(ctx, memory.Document{
		ID:      "1",
		Content: string(b),
		Metadata: map[string]interface{}{
			"score": 0.9,
		},
	})

	tool := NewHybridSearchTool(ltm, &mockProvider{})

	out, err := tool.Execute(ctx, map[string]interface{}{
		"query":         "Bar",
		"limit":         float64(1),
		"output_format": "json",
		"file_path":     "/tmp/Bar.php",
	})
	if err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}

	var symbols []codetypes.SymbolDescriptor
	if err := json.Unmarshal([]byte(out), &symbols); err != nil {
		t.Fatalf("failed to unmarshal SymbolDescriptor list: %v", err)
	}

	if len(symbols) != 1 {
		t.Fatalf("expected exactly one symbol in JSON output, got %d", len(symbols))
	}
	s := symbols[0]
	if s.Name != "Bar" || s.Language != "php" || s.Package != "App" {
		t.Errorf("unexpected symbol descriptor: %+v", s)
	}
	if s.Metadata == nil {
		t.Fatalf("expected metadata with scores/snippet, got nil")
	}
	if _, ok := s.Metadata["snippet"]; !ok {
		t.Errorf("expected snippet in metadata, got: %+v", s.Metadata)
	}
}

// MockSpyMemory tracks method calls to verify behavior
type MockSpyMemory struct {
	*memory.InMemoryLongTermMemory
	SearchCalled     bool
	SearchCodeCalled bool
}

func NewMockSpyMemory() *MockSpyMemory {
	return &MockSpyMemory{
		InMemoryLongTermMemory: memory.NewInMemoryLongTermMemory(),
	}
}

func (m *MockSpyMemory) Search(ctx context.Context, query []float64, limit int) ([]memory.Document, error) {
	m.SearchCalled = true
	// Return a dummy doc to avoid "No relevant code found"
	return []memory.Document{{ID: "1", Content: "dummy"}}, nil
}

func (m *MockSpyMemory) SearchCodeOnly(ctx context.Context, query []float64, limit int) ([]memory.Document, error) {
	m.SearchCodeCalled = true
	return []memory.Document{{ID: "1", Content: "dummy"}}, nil
}

func TestHybridSearchTool_IncludeDocs(t *testing.T) {
	ctx := context.Background()

	tests := []struct {
		name           string
		params         map[string]interface{}
		wantSearch     bool
		wantSearchCode bool
	}{
		{
			name:           "Default (missing include_docs)",
			params:         map[string]interface{}{"query": "foo", "file_path": "test.go"},
			wantSearch:     false,
			wantSearchCode: true,
		},
		{
			name:           "Explicit include_docs: false",
			params:         map[string]interface{}{"query": "foo", "file_path": "test.go", "include_docs": false},
			wantSearch:     false,
			wantSearchCode: true,
		},
		{
			name:           "Explicit include_docs: true",
			params:         map[string]interface{}{"query": "foo", "file_path": "test.go", "include_docs": true},
			wantSearch:     true,
			wantSearchCode: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mem := NewMockSpyMemory()
			tool := NewHybridSearchTool(mem, &mockProvider{})

			_, _ = tool.Execute(ctx, tt.params)

			if mem.SearchCalled != tt.wantSearch {
				t.Errorf("Search() called = %v, want %v", mem.SearchCalled, tt.wantSearch)
			}
			if mem.SearchCodeCalled != tt.wantSearchCode {
				t.Errorf("SearchCodeOnly() called = %v, want %v", mem.SearchCodeCalled, tt.wantSearchCode)
			}
		})
	}
}

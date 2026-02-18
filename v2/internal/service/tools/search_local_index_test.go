package tools

import (
	"context"
	"testing"
)

func TestSearchLocalIndexTool_IncludeDocs(t *testing.T) {
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
			tool := NewSearchLocalIndexTool(mem, &mockProvider{})

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

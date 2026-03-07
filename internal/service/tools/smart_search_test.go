package tools

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestGroupDocsByTree(t *testing.T) {
	// Create a temporary file for testing disk reads
	tempDir := t.TempDir()
	tempFilePath := filepath.Join(tempDir, "test_doc.md")
	
	// Create a dummy document with 20 lines
	lines := make([]string, 20)
	for i := 0; i < 20; i++ {
		lines[i] = "Line " + string(rune('A'+i)) // Line A, Line B, etc.
	}
	os.WriteFile(tempFilePath, []byte(strings.Join(lines, "\n")), 0644)

	// Create tool instance (we only need the groupDocsByTree method)
	tool := &SmartSearchTool{}

	tests := []struct {
		name     string
		input    []mergedResult
		expected int // Expected number of results after grouping
		check    func(t *testing.T, results []mergedResult)
	}{
		{
			name: "No grouping needed for code",
			input: []mergedResult{
				{id: "1", filePath: "main.go", symbolType: "function", score: 0.9},
				{id: "2", filePath: "main.go", symbolType: "function", score: 0.8},
			},
			expected: 2,
			check: func(t *testing.T, results []mergedResult) {
				if len(results) != 2 {
					t.Errorf("Expected 2 results, got %d", len(results))
				}
			},
		},
		{
			name: "Group documentation chunks from same file and signature",
			input: []mergedResult{
				{
					id: "chunk_1", filePath: tempFilePath, symbolType: "documentation", signature: "### Intro",
					startLine: 1, endLine: 3, score: 0.8,
				},
				{
					id: "chunk_2", filePath: tempFilePath, symbolType: "documentation", signature: "### Intro",
					startLine: 4, endLine: 6, score: 0.9,
				},
				// This one is in a different signature
				{
					id: "chunk_3", filePath: tempFilePath, symbolType: "documentation", signature: "### Setup",
					startLine: 8, endLine: 10, score: 0.5,
				},
				// This one is code, ignore grouping
				{
					id: "chunk_4", filePath: tempFilePath, symbolType: "code_block", signature: "### Intro",
					startLine: 7, endLine: 7, score: 0.85,
				},
			},
			// Expect: 1 merged block for "### Intro", 1 block for "### Setup".
			// Note: chunk_4 has "code_block" type! Code blocks in markdown also get grouped!
			expected: 2,
			check: func(t *testing.T, results []mergedResult) {
				if len(results) != 2 {
					t.Fatalf("Expected 2 results, got %d", len(results))
				}
				
				// results are sorted by score. The merged "### Intro" should have max score 0.9
				if results[0].score != 0.9 {
					t.Errorf("Expected max score 0.9, got %v", results[0].score)
				}
				
				// Check start/end line bounds for the merged "### Intro"
				if results[0].startLine != 1 || results[0].endLine != 7 {
					t.Errorf("Expected lines 1-7, got %d-%d", results[0].startLine, results[0].endLine)
				}
				
				// Verify the content was loaded from disk and contains Lines A to G (1 to 7)
				if !strings.Contains(results[0].content, "Line A") || !strings.Contains(results[0].content, "Line G") {
					t.Errorf("Merged content does not match expected lines from disk")
				}
				
				// Setup should be untouched
				if results[1].signature != "### Setup" {
					t.Errorf("Expected second result to be '### Setup'")
				}
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := tool.groupDocsByTree(tc.input)
			if len(got) != tc.expected {
				t.Errorf("Expected %d results, got %d", tc.expected, len(got))
			}
			tc.check(t, got)
		})
	}
}

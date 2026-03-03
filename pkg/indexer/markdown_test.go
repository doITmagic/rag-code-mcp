package indexer

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestChunkMarkdownFile_BasicHeadings(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "README.md")

	content := `# My Project

This is the introduction.

## Installation

Run the following command:

` + "```bash" + `
go install ./...
` + "```" + `

## API Reference

### Authentication

The AuthService handles all auth flows.

#### OAuth Flow

The ` + "`AuthService.Authenticate()`" + ` function handles OAuth.

### Users

| Method | Endpoint |
| GET | /api/users |
| POST | /api/login |
`
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	cfg := DefaultMarkdownConfig()
	chunks, err := ChunkMarkdownFile(path, cfg)
	if err != nil {
		t.Fatalf("ChunkMarkdownFile failed: %v", err)
	}

	if len(chunks) == 0 {
		t.Fatal("Expected at least 1 chunk, got 0")
	}

	t.Logf("Got %d chunks:", len(chunks))
	for i, c := range chunks {
		t.Logf("  Chunk %d (heading: %q): %d chars", i, c.SectionHeading, len(c.Content))
		// Show first 100 chars
		preview := c.Content
		if len(preview) > 100 {
			preview = preview[:100] + "..."
		}
		t.Logf("    Preview: %s", preview)
	}

	// Verify file path is set
	for _, c := range chunks {
		if c.FilePath != path {
			t.Errorf("Expected FilePath %q, got %q", path, c.FilePath)
		}
	}
}

func TestChunkMarkdownFile_HeadingHierarchy(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "docs.md")

	// Create a large enough doc that it needs multiple chunks
	content := "# Top Level\n\n"
	content += "## Section A\n\n"
	content += strings.Repeat("This is section A content. ", 100) + "\n\n"
	content += "## Section B\n\n"
	content += "### Sub Section B1\n\n"
	content += strings.Repeat("This is sub section B1 content. ", 100) + "\n\n"

	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	cfg := MarkdownChunkConfig{
		ChunkSize:    500,
		ChunkOverlap: 50,
		KeepHeading:  true,
	}

	chunks, err := ChunkMarkdownFile(path, cfg)
	if err != nil {
		t.Fatalf("ChunkMarkdownFile failed: %v", err)
	}

	if len(chunks) < 2 {
		t.Fatalf("Expected at least 2 chunks with small chunk size, got %d", len(chunks))
	}

	t.Logf("Got %d chunks with heading hierarchy:", len(chunks))
	for i, c := range chunks {
		heading := c.SectionHeading
		if heading == "" {
			heading = "(none)"
		}
		t.Logf("  Chunk %d: heading=%q, %d chars", i, heading, len(c.Content))
	}
}

func TestChunkMarkdownFile_EmptyFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "empty.md")

	if err := os.WriteFile(path, []byte(""), 0644); err != nil {
		t.Fatal(err)
	}

	chunks, err := ChunkMarkdownFile(path, DefaultMarkdownConfig())
	if err != nil {
		t.Fatalf("Unexpected error for empty file: %v", err)
	}
	if len(chunks) != 0 {
		t.Errorf("Expected 0 chunks for empty file, got %d", len(chunks))
	}
}

func TestChunkMarkdownFile_NonExistent(t *testing.T) {
	_, err := ChunkMarkdownFile("/nonexistent/file.md", DefaultMarkdownConfig())
	if err == nil {
		t.Error("Expected error for non-existent file")
	}
}

func TestChunkMarkdownFile_PreservesCodeBlocks(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "code.md")

	content := "# Code Example\n\n" +
		"Here is some code:\n\n" +
		"```go\n" +
		"func main() {\n" +
		"    fmt.Println(\"Hello\")\n" +
		"}\n" +
		"```\n\n" +
		"And that's it.\n"

	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	chunks, err := ChunkMarkdownFile(path, DefaultMarkdownConfig())
	if err != nil {
		t.Fatalf("ChunkMarkdownFile failed: %v", err)
	}

	// Code block should be in one chunk, not split
	foundCodeBlock := false
	for _, c := range chunks {
		if strings.Contains(c.Content, "fmt.Println") && strings.Contains(c.Content, "func main()") {
			foundCodeBlock = true
			break
		}
	}

	if !foundCodeBlock {
		t.Error("Code block was split across chunks — expected it to be preserved in a single chunk")
		for i, c := range chunks {
			t.Logf("  Chunk %d: %s", i, c.Content[:min(100, len(c.Content))])
		}
	}
}

func TestIsMarkdownFile(t *testing.T) {
	tests := []struct {
		path string
		want bool
	}{
		{"README.md", true},
		{"docs/guide.MD", true},
		{"notes.markdown", true},
		{"CHANGELOG.Markdown", true},
		{"main.go", false},
		{"style.css", false},
		{"data.json", false},
		{"noext", false},
	}
	for _, tt := range tests {
		got := IsMarkdownFile(tt.path)
		if got != tt.want {
			t.Errorf("IsMarkdownFile(%q) = %v, want %v", tt.path, got, tt.want)
		}
	}
}

func TestExtractFirstHeading(t *testing.T) {
	tests := []struct {
		text string
		want string
	}{
		{"# Title\nContent", "# Title"},
		{"## Sub Title\nMore", "## Sub Title"},
		{"No heading here", ""},
		{"### Deep\n#### Deeper", "### Deep"},
		{"Just text\n# Late heading", "# Late heading"},
	}
	for _, tt := range tests {
		got := extractFirstHeading(tt.text)
		if got != tt.want {
			t.Errorf("extractFirstHeading(%q) = %q, want %q", tt.text[:min(30, len(tt.text))], got, tt.want)
		}
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

package workspace

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/doITmagic/rag-code-mcp/internal/config"
	"github.com/doITmagic/rag-code-mcp/internal/llm"
	"github.com/doITmagic/rag-code-mcp/internal/memory"
)

// MockLLMProvider for testing
type MockLLMProvider struct{}

func (m *MockLLMProvider) Generate(ctx context.Context, prompt string, opts ...llm.GenerateOption) (string, error) {
	return "mock response", nil
}

func (m *MockLLMProvider) GenerateStream(ctx context.Context, prompt string, opts ...llm.GenerateOption) (<-chan string, <-chan error) {
	ch := make(chan string)
	errCh := make(chan error)
	close(ch)
	close(errCh)
	return ch, errCh
}

func (m *MockLLMProvider) Embed(ctx context.Context, text string) ([]float64, error) {
	// Return 768-dim zero vector
	return make([]float64, 768), nil
}

func (m *MockLLMProvider) Name() string {
	return "mock"
}

// MockLongTermMemory for testing
type MockLongTermMemory struct {
	docs []memory.Document
}

func (m *MockLongTermMemory) Store(ctx context.Context, doc memory.Document) error {
	m.docs = append(m.docs, doc)
	return nil
}

func (m *MockLongTermMemory) Search(ctx context.Context, query []float64, limit int) ([]memory.Document, error) {
	return m.docs, nil
}

func (m *MockLongTermMemory) Delete(ctx context.Context, id string) error {
	for i, doc := range m.docs {
		if doc.ID == id {
			m.docs = append(m.docs[:i], m.docs[i+1:]...)
			return nil
		}
	}
	return nil
}

func (m *MockLongTermMemory) DeleteByMetadata(ctx context.Context, key, value string) error {
	var newDocs []memory.Document
	for _, doc := range m.docs {
		if val, ok := doc.Metadata[key]; ok {
			if strVal, ok := val.(string); ok && strVal == value {
				continue
			}
		}
		newDocs = append(newDocs, doc)
	}
	m.docs = newDocs
	return nil
}

func (m *MockLongTermMemory) Clear(ctx context.Context) error {
	m.docs = nil
	return nil
}

func TestMarkdownIndexing(t *testing.T) {
	// Create a temporary workspace with markdown files
	tmpDir := t.TempDir()

	// Create README.md
	readmeContent := `# Test Project

This is a test project for markdown indexing.

## Features

- Feature 1
- Feature 2

## Installation

Run npm install to install dependencies.
`
	err := os.WriteFile(filepath.Join(tmpDir, "README.md"), []byte(readmeContent), 0644)
	if err != nil {
		t.Fatalf("Failed to create README.md: %v", err)
	}

	// Create docs/guide.md
	docsDir := filepath.Join(tmpDir, "docs")
	err = os.MkdirAll(docsDir, 0755)
	if err != nil {
		t.Fatalf("Failed to create docs dir: %v", err)
	}

	guideContent := `# User Guide

## Getting Started

Welcome to the guide.

## Usage

Use this tool to do things.
`
	err = os.WriteFile(filepath.Join(docsDir, "guide.md"), []byte(guideContent), 0644)
	if err != nil {
		t.Fatalf("Failed to create guide.md: %v", err)
	}

	// Create package.json marker
	packageJSON := `{"name": "test-project", "version": "1.0.0"}`
	err = os.WriteFile(filepath.Join(tmpDir, "package.json"), []byte(packageJSON), 0644)
	if err != nil {
		t.Fatalf("Failed to create package.json: %v", err)
	}

	// Create workspace info
	info := &Info{
		ID:          "test-workspace-123",
		Root:        tmpDir,
		ProjectType: "node",
	}

	// Create mock manager
	mockLLM := &MockLLMProvider{}
	mockLTM := &MockLongTermMemory{}

	cfg := &config.Config{}
	manager := &Manager{
		llm:    mockLLM,
		config: cfg,
	}

	// Test markdown indexing
	ctx := context.Background()
	scan, err := manager.scanWorkspace(info)
	if err != nil {
		t.Fatalf("Failed to scan workspace: %v", err)
	}
	numChunks := manager.indexMarkdownFiles(ctx, scan.DocFiles, "test-collection", mockLTM)

	if numChunks == 0 {
		t.Error("Expected to index markdown chunks, got 0")
	}

	if len(mockLTM.docs) == 0 {
		t.Error("Expected documents to be stored, got 0")
	}

	t.Logf("Indexed %d markdown chunks from 2 files", numChunks)
	t.Logf("Stored %d documents", len(mockLTM.docs))

	// Verify metadata
	foundMarkdown := false
	for _, doc := range mockLTM.docs {
		if chunkType, ok := doc.Metadata["chunk_type"].(string); ok && chunkType == "markdown" {
			foundMarkdown = true
			break
		}
	}

	if !foundMarkdown {
		t.Error("Expected at least one document with chunk_type=markdown")
	}
}

func TestMarkdownIndexing_SkipsCommonDirs(t *testing.T) {
	tmpDir := t.TempDir()

	// Create markdown files in directories that should be skipped
	skipDirs := []string{"node_modules", "vendor", ".git", "dist", "build"}
	for _, dir := range skipDirs {
		dirPath := filepath.Join(tmpDir, dir)
		err := os.MkdirAll(dirPath, 0755)
		if err != nil {
			t.Fatalf("Failed to create %s: %v", dir, err)
		}
		err = os.WriteFile(filepath.Join(dirPath, "README.md"), []byte("# Should Skip"), 0644)
		if err != nil {
			t.Fatalf("Failed to create README in %s: %v", dir, err)
		}
	}

	// Create a markdown file that should be indexed
	err := os.WriteFile(filepath.Join(tmpDir, "README.md"), []byte("# Should Index"), 0644)
	if err != nil {
		t.Fatalf("Failed to create root README: %v", err)
	}

	info := &Info{
		ID:          "test-skip-123",
		Root:        tmpDir,
		ProjectType: "unknown",
	}

	mockLLM := &MockLLMProvider{}
	mockLTM := &MockLongTermMemory{}
	cfg := &config.Config{}
	manager := &Manager{
		llm:    mockLLM,
		config: cfg,
	}

	ctx := context.Background()
	scan, err := manager.scanWorkspace(info)
	if err != nil {
		t.Fatalf("Failed to scan workspace: %v", err)
	}
	numChunks := manager.indexMarkdownFiles(ctx, scan.DocFiles, "test-collection", mockLTM)

	// Should only index the root README, not the ones in skip dirs
	if numChunks == 0 {
		t.Error("Expected to index root README")
	}

	// Check that we didn't index files from skip directories
	for _, doc := range mockLTM.docs {
		if file, ok := doc.Metadata["file"].(string); ok {
			for _, skipDir := range skipDirs {
				if filepath.Base(filepath.Dir(file)) == skipDir {
					t.Errorf("Indexed file from skip directory: %s", file)
				}
			}
		}
	}

	t.Logf("Correctly indexed %d chunks, skipping common directories", numChunks)
}

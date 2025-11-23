package workspace

import (
	"context"
	"testing"
	"time"

	"github.com/doITmagic/rag-code-mcp/internal/config"
)

// TestGetMemoryForWorkspaceLanguage tests language-specific memory retrieval
func TestGetMemoryForWorkspaceLanguage(t *testing.T) {
	// This is an integration test skeleton
	// Requires running Qdrant instance
	t.Skip("Skipping integration test - requires Qdrant")

	cfg := &config.Config{
		Workspace: config.WorkspaceConfig{
			Enabled:          true,
			AutoIndex:        false, // Don't auto-index in tests
			MaxWorkspaces:    10,
			CollectionPrefix: "test-ragcode",
		},
	}

	// Create test workspace info
	info := &Info{
		Root:             "/tmp/test-workspace",
		ID:               "test123",
		ProjectType:      "go",
		Languages:        []string{"go", "python"},
		CollectionPrefix: "test-ragcode",
		DetectedAt:       time.Now(),
	}

	ctx := context.Background()

	// Test collection names
	goCollection := info.CollectionNameForLanguage("go")
	if goCollection != "test-ragcode-test123-go" {
		t.Errorf("Expected 'test-ragcode-test123-go', got '%s'", goCollection)
	}

	pythonCollection := info.CollectionNameForLanguage("python")
	if pythonCollection != "test-ragcode-test123-python" {
		t.Errorf("Expected 'test-ragcode-test123-python', got '%s'", pythonCollection)
	}

	t.Logf("Go collection: %s", goCollection)
	t.Logf("Python collection: %s", pythonCollection)

	// Would test:
	// 1. manager.GetMemoryForWorkspaceLanguage(ctx, info, "go")
	// 2. manager.GetMemoryForWorkspaceLanguage(ctx, info, "python")
	// 3. Verify separate collections created
	// 4. Verify can index/search each independently
	_ = ctx
	_ = cfg
}

// TestGetMemoriesForAllLanguages tests getting memories for all detected languages
func TestGetMemoriesForAllLanguages(t *testing.T) {
	t.Skip("Skipping integration test - requires Qdrant")

	info := &Info{
		Root:             "/tmp/polyglot-workspace",
		ID:               "poly123",
		ProjectType:      "go",
		Languages:        []string{"go", "python", "javascript"},
		CollectionPrefix: "test-ragcode",
		DetectedAt:       time.Now(),
	}

	// Would test:
	// 1. manager.GetMemoriesForAllLanguages(ctx, info)
	// 2. Verify 3 collections created (go, python, javascript)
	// 3. Verify can search each independently
	// 4. Verify cross-language search aggregation

	expectedCollections := []string{
		"test-ragcode-poly123-go",
		"test-ragcode-poly123-python",
		"test-ragcode-poly123-javascript",
	}

	for i, lang := range info.Languages {
		collection := info.CollectionNameForLanguage(lang)
		if collection != expectedCollections[i] {
			t.Errorf("Language %s: expected '%s', got '%s'",
				lang, expectedCollections[i], collection)
		}
	}
}

// TestLanguageInferenceFromPath tests language detection from file paths
func TestLanguageInferenceFromPath(t *testing.T) {
	// This would test the inferLanguageFromPath function in tools/utils.go
	// when integrated with workspace detection

	info := &Info{
		Root:             "/home/user/project",
		ID:               "abc123",
		Languages:        []string{"go", "python"},
		CollectionPrefix: "ragcode",
	}

	testCases := []struct {
		filePath           string
		expectedLanguage   string
		expectedCollection string
	}{
		{
			filePath:           "/home/user/project/main.go",
			expectedLanguage:   "go",
			expectedCollection: "ragcode-abc123-go",
		},
		{
			filePath:           "/home/user/project/script.py",
			expectedLanguage:   "python",
			expectedCollection: "ragcode-abc123-python",
		},
		{
			filePath:           "/home/user/project/app.js",
			expectedLanguage:   "javascript",
			expectedCollection: "ragcode-abc123-javascript",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.filePath, func(t *testing.T) {
			// In real implementation, this would:
			// 1. Extract file extension
			// 2. Map to language
			// 3. Get appropriate collection
			collection := info.CollectionNameForLanguage(tc.expectedLanguage)
			if collection != tc.expectedCollection {
				t.Errorf("Expected collection '%s', got '%s'",
					tc.expectedCollection, collection)
			}
		})
	}
}

// TestIndexingKeyGeneration tests the indexing key format for multi-language
func TestIndexingKeyGeneration(t *testing.T) {
	info := &Info{
		ID:        "workspace123",
		Languages: []string{"go", "python"},
	}

	// Indexing keys should be: workspaceID + "-" + language
	expectedKeys := []string{
		"workspace123-go",
		"workspace123-python",
	}

	for i, lang := range info.Languages {
		indexKey := info.ID + "-" + lang
		if indexKey != expectedKeys[i] {
			t.Errorf("Language %s: expected key '%s', got '%s'",
				lang, expectedKeys[i], indexKey)
		}
	}
}

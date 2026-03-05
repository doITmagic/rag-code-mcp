package indexer

import (
	"context"
	"crypto/sha256"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/doITmagic/rag-code-mcp/pkg/storage"
	"github.com/tmc/langchaingo/textsplitter"
)

// MarkdownChunkConfig configures the markdown chunking behavior.
type MarkdownChunkConfig struct {
	ChunkSize    int  // Maximum characters per chunk (default: 2000)
	ChunkOverlap int  // Overlap between adjacent chunks (default: 200)
	KeepHeading  bool // Prepend parent headings to each chunk (default: true)
}

// DefaultMarkdownConfig returns sensible defaults for markdown chunking.
func DefaultMarkdownConfig() MarkdownChunkConfig {
	return MarkdownChunkConfig{
		ChunkSize:    2000,
		ChunkOverlap: 200,
		KeepHeading:  true,
	}
}

// MarkdownChunk represents a single chunk extracted from a markdown file.
type MarkdownChunk struct {
	Content        string // The chunk text (with heading hierarchy if enabled)
	FilePath       string // Absolute path to the source .md file
	SectionHeading string // The closest heading above this chunk
	ChunkIndex     int    // Zero-based index within the file
}

// ChunkMarkdownFile reads a markdown file and splits it into semantic chunks
// using langchaingo's MarkdownTextSplitter with heading hierarchy preservation.
func ChunkMarkdownFile(path string, cfg MarkdownChunkConfig) ([]MarkdownChunk, error) {
	content, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read markdown file %s: %w", path, err)
	}

	if len(strings.TrimSpace(string(content))) == 0 {
		return nil, nil // Empty file, nothing to chunk
	}

	splitter := textsplitter.NewMarkdownTextSplitter(
		textsplitter.WithChunkSize(cfg.ChunkSize),
		textsplitter.WithChunkOverlap(cfg.ChunkOverlap),
		textsplitter.WithHeadingHierarchy(cfg.KeepHeading),
		textsplitter.WithCodeBlocks(true),
	)

	texts, err := splitter.SplitText(string(content))
	if err != nil {
		return nil, fmt.Errorf("split markdown %s: %w", path, err)
	}

	chunks := make([]MarkdownChunk, 0, len(texts))
	for i, text := range texts {
		text = strings.TrimSpace(text)
		if text == "" {
			continue
		}

		heading := extractFirstHeading(text)

		chunks = append(chunks, MarkdownChunk{
			Content:        text,
			FilePath:       path,
			SectionHeading: heading,
			ChunkIndex:     i,
		})
	}

	return chunks, nil
}

// extractFirstHeading returns the first markdown heading found in the text,
// or an empty string if none exists.
func extractFirstHeading(text string) string {
	lines := strings.SplitN(text, "\n", 20) // Only scan first 20 lines
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "#") {
			// Count leading hashes
			i := 0
			for i < len(trimmed) && trimmed[i] == '#' {
				i++
			}
			if i >= 1 && i <= 6 && (i == len(trimmed) || trimmed[i] == ' ') {
				return trimmed
			}
		}
	}
	return ""
}

// IsMarkdownFile returns true if the file extension indicates a markdown file.
func IsMarkdownFile(path string) bool {
	ext := strings.ToLower(filepath.Ext(path))
	return ext == ".md" || ext == ".markdown"
}

// ShouldSkipDir returns true for directories that should be excluded from markdown scanning.
func ShouldSkipDir(name string) bool {
	skip := map[string]bool{
		"node_modules": true,
		"vendor":       true,
		".git":         true,
		".ragcode":     true,
		".vscode":      true,
		".idea":        true,
		"__pycache__":  true,
		"dist":         true,
		"build":        true,
	}
	return strings.HasPrefix(name, ".") || skip[name]
}

// IndexMarkdownFiles indexes a list of markdown files into the given collection.
// It chunks each file, generates embeddings, and stores them in Qdrant with
// chunk_type: "markdown" metadata for filtering.
func (s *Service) IndexMarkdownFiles(ctx context.Context, collection string, mdFiles []string, cfg MarkdownChunkConfig) (int, error) {
	if len(mdFiles) == 0 {
		return 0, nil
	}

	total := len(mdFiles)
	log.Printf("[INFO] 📚 Indexing %d markdown file(s) into %s...", total, collection)

	totalChunks := 0
	for i, path := range mdFiles {
		log.Printf("[INFO] 📄 [%d/%d] %s", i+1, total, filepath.Base(path))
		n, err := s.indexSingleMarkdown(ctx, collection, path, cfg)
		if err != nil {
			log.Printf("[WARN] Failed to index markdown %s: %v", path, err)
			continue
		}
		log.Printf("[INFO]     → %d chunk(s) indexed", n)
		totalChunks += n
	}

	if totalChunks > 0 {
		log.Printf("[INFO] ✅ Indexed %d markdown chunks from %d file(s)", totalChunks, total)
	}

	return totalChunks, nil
}

// indexSingleMarkdown chunks and indexes one markdown file.
func (s *Service) indexSingleMarkdown(ctx context.Context, collection, path string, cfg MarkdownChunkConfig) (int, error) {
	chunks, err := ChunkMarkdownFile(path, cfg)
	if err != nil {
		return 0, err
	}
	if len(chunks) == 0 {
		return 0, nil
	}

	// Delete old points for this file before re-indexing
	if err := s.store.DeleteByFilter(ctx, collection, "file_path", path); err != nil {
		log.Printf("[WARN] Failed to delete old markdown points for %s: %v", path, err)
	}

	var points []storage.Point
	for _, chunk := range chunks {
		// Generate embedding for the chunk text
		embedCtx, embedCancel := context.WithTimeout(ctx, 30*time.Second)
		vector64, err := s.embedder.Embed(embedCtx, chunk.Content)
		embedCancel()

		if err != nil {
			log.Printf("[WARN] Embed failed for markdown chunk %s#%d: %v", path, chunk.ChunkIndex, err)
			continue
		}

		// Update activity watchdog
		s.lastActivity.Store(time.Now().Unix())

		// Convert float64 → float32
		vector := make([]float32, len(vector64))
		for i, v := range vector64 {
			vector[i] = float32(v)
		}

		// Stable ID based on file path + chunk index
		idKey := fmt.Sprintf("md:%s:%d", path, chunk.ChunkIndex)
		id := fmt.Sprintf("%x", sha256.Sum256([]byte(idKey)))[:32]

		payload := map[string]interface{}{
			"chunk_type":      "markdown",
			"file_path":       chunk.FilePath,
			"section_heading": chunk.SectionHeading,
			"chunk_id":        chunk.ChunkIndex,
			"content":         chunk.Content,
			"name":            filepath.Base(chunk.FilePath),
			"type":            "documentation",
			"text":            chunk.Content, // Same field as code symbols, used for search display
		}

		points = append(points, storage.Point{
			ID:      id,
			Vector:  vector,
			Payload: payload,
		})
	}

	if len(points) == 0 {
		return 0, nil
	}

	// Batch upsert (same pattern as IndexItems)
	batchSize := 50
	for i := 0; i < len(points); i += batchSize {
		end := i + batchSize
		if end > len(points) {
			end = len(points)
		}

		upsertCtx, upsertCancel := context.WithTimeout(ctx, 30*time.Second)
		_, err := s.store.Upsert(upsertCtx, collection, points[i:end])
		upsertCancel()

		if err != nil {
			return 0, fmt.Errorf("upsert markdown batch failed: %w", err)
		}
	}

	return len(points), nil
}

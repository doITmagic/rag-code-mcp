package ragcode

import (
	"context"
	"encoding/json"
	"fmt"
	"hash/fnv"
	"log"
	"path/filepath"
	"strings"

	"github.com/doITmagic/rag-code-mcp/internal/codetypes"
	"github.com/doITmagic/rag-code-mcp/internal/llm"
	"github.com/doITmagic/rag-code-mcp/internal/memory"
)

// maxEmbedChars is the maximum number of Unicode characters sent to the embedding
// model. Common models (e.g. nomic-embed-text) have an 8 192-token context window
// (~4 chars/token → ~32 768 chars). We use 30 000 to give ~6% headroom and stay
// compatible with smaller-window models.
const maxEmbedChars = 30_000

// buildEmbedText constructs the text to embed for a CodeChunk, then truncates it
// to maxChars (rune-safe, UTF-8 correct) to avoid exceeding the model's context
// window. Metadata (docstring, signature) is always preserved in full; only Code
// is truncated when the total exceeds maxChars.
// Returns (text, wasTruncated).
func buildEmbedText(ch codetypes.CodeChunk, maxChars int) (string, bool) {
	meta := strings.TrimSpace(strings.Join(filterNonEmpty([]string{
		ch.Docstring,
		ch.Signature,
	}), "\n\n"))

	var full string
	if ch.Code != "" {
		if meta != "" {
			full = meta + "\n\n" + ch.Code
		} else {
			full = ch.Code
		}
	} else {
		full = meta
	}

	runes := []rune(full)
	if len(runes) <= maxChars {
		return full, false
	}

	// Truncate only the Code portion — keep metadata intact.
	metaWithSep := meta
	if meta != "" && ch.Code != "" {
		metaWithSep = meta + "\n\n"
	}
	metaRunes := []rune(metaWithSep)
	remaining := maxChars - len(metaRunes)
	if remaining < 0 {
		remaining = 0
	}
	codeRunes := []rune(ch.Code)
	if remaining > len(codeRunes) {
		remaining = len(codeRunes)
	}
	truncated := metaWithSep + string(codeRunes[:remaining])
	return truncated, true
}

// Indexer indexes CodeChunks into LongTermMemory using an embedding Provider.
type Indexer struct {
	analyzer codetypes.PathAnalyzer
	embedder llm.Provider
	ltm      memory.LongTermMemory
}

func NewIndexer(analyzer codetypes.PathAnalyzer, embedder llm.Provider, ltm memory.LongTermMemory) *Indexer {
	return &Indexer{analyzer: analyzer, embedder: embedder, ltm: ltm}
}

// IndexPaths analyzes, embeds and stores all code chunks under the given paths.
// collection and dimension management should be handled by the caller (Qdrant client).
func (i *Indexer) IndexPaths(ctx context.Context, paths []string, sourceTag string) (int, error) {
	chunks, err := i.analyzer.AnalyzePaths(paths)
	if err != nil {
		return 0, err
	}

	indexed := 0
	for _, ch := range chunks {
		text, wasTruncated := buildEmbedText(ch, maxEmbedChars)
		text = strings.TrimSpace(text)
		if text == "" {
			continue
		}
		if wasTruncated {
			// Log a warning so users know which symbols were partially indexed.
			// The full code is still accessible via rag_read_file_context.
			log.Printf("[WARN] embed text truncated for %s (%s:%d-%d) — content exceeds model context window",
				ch.Name, filepath.Base(ch.FilePath), ch.StartLine, ch.EndLine)
		}

		emb, err := i.embedder.Embed(ctx, text)
		if err != nil {
			return indexed, fmt.Errorf("embed failed for %s:%s: %w", ch.FilePath, ch.Name, err)
		}

		h := fnv.New64a()
		h.Write([]byte(fmt.Sprintf("%s:%d-%d:%s", ch.FilePath, ch.StartLine, ch.EndLine, ch.Name)))
		id := fmt.Sprintf("%d", h.Sum64())

		chunkJSON, err := json.Marshal(ch)
		if err != nil {
			return indexed, fmt.Errorf("marshal chunk failed for %s: %w", ch.Name, err)
		}

		doc := memory.Document{
			ID:        id,
			Content:   string(chunkJSON),
			Embedding: emb,
			Metadata: map[string]interface{}{
				"file":       ch.FilePath,
				"package":    ch.Package,
				"name":       ch.Name,
				"type":       ch.Type,
				"signature":  ch.Signature,
				"start_line": ch.StartLine,
				"end_line":   ch.EndLine,
				"source":     sourceTag,
				"basename":   filepath.Base(ch.FilePath),
			},
		}

		if err := i.ltm.Store(ctx, doc); err != nil {
			return indexed, fmt.Errorf("store failed for %s: %w", id, err)
		}
		indexed++
	}
	return indexed, nil
}

func filterNonEmpty(parts []string) []string {
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		if strings.TrimSpace(part) != "" {
			out = append(out, part)
		}
	}
	return out
}

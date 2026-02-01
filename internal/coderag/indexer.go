package coderag

import (
	"context"
	"encoding/json"
	"fmt"
	"hash/fnv"
	"path/filepath"
	"strings"

	"github.com/doITmagic/coderag-mcp/internal/codetypes"
	"github.com/doITmagic/coderag-mcp/internal/llm"
	"github.com/doITmagic/coderag-mcp/internal/memory"
)

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
		text := strings.TrimSpace(strings.Join(filterNonEmpty([]string{
			ch.Docstring,
			ch.Signature,
			ch.Code,
		}), "\n\n"))
		if text == "" {
			continue
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

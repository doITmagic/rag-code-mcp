package indexer

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/doITmagic/rag-code-mcp/internal/service/internalutil"
	"github.com/doITmagic/rag-code-mcp/pkg/llm"
	"github.com/doITmagic/rag-code-mcp/pkg/parser"
	"github.com/doITmagic/rag-code-mcp/pkg/storage"
)

// Service orchestrates the indexing process.
type Service struct {
	embedder llm.Provider
	store    storage.VectorStore
}

// NewService creates a new indexer service.
func NewService(embedder llm.Provider, store storage.VectorStore) *Service {
	return &Service{
		embedder: embedder,
		store:    store,
	}
}

// IndexItems processes symbols and stores them in the vector database.
func (s *Service) IndexItems(ctx context.Context, collection string, symbols []parser.Symbol) error {
	if len(symbols) == 0 {
		return nil
	}

	// Group symbols by file to minimize I/O if we need to read content
	fileSymbols := make(map[string][]parser.Symbol)
	for _, sym := range symbols {
		fileSymbols[sym.FilePath] = append(fileSymbols[sym.FilePath], sym)
	}

	var allPoints []storage.Point

	for path, syms := range fileSymbols {
		content, err := os.ReadFile(path)
		if err != nil {
			// Skip files we can't read
			continue
		}
		lines := strings.Split(string(content), "\n")

		for _, sym := range syms {
			// Extract content if not provided by parser
			if sym.Content == "" {
				start := sym.StartLine - 1
				end := sym.EndLine
				if start < 0 {
					start = 0
				}
				if end > len(lines) {
					end = len(lines)
				}
				if start < end {
					sym.Content = strings.Join(lines[start:end], "\n")
				}
			}

			// Generate text for embedding
			embedText := fmt.Sprintf("%s\n%s\n%s\n%s", sym.Package, sym.Name, sym.Signature, sym.Content)
			if sym.Docstring != "" {
				embedText = sym.Docstring + "\n" + embedText
			}

			vector64, err := s.embedder.Embed(ctx, embedText)
			if err != nil {
				return fmt.Errorf("failed to embed symbol %s: %w", sym.Name, err)
			}
			vector := internalutil.Float64To32(vector64)

			// Unique ID based on path, name and range
			idKey := fmt.Sprintf("%s:%s:%d:%d", sym.FilePath, sym.Name, sym.StartLine, sym.EndLine)
			id := fmt.Sprintf("%x", sha256.Sum256([]byte(idKey)))[:32]

			payload, _ := s.structToMap(sym)
			payload["text"] = embedText

			allPoints = append(allPoints, storage.Point{
				ID:      id,
				Vector:  vector,
				Payload: payload,
			})
		}
	}

	// Ensure collection exists
	dim := int(s.embedder.GetEmbeddingDimension())
	if dim == 0 {
		return fmt.Errorf("embedding dimension is zero; provider not initialized")
	}

	exists, err := s.store.CollectionExists(ctx, collection)
	if err != nil {
		return err
	}
	if !exists {
		if err := s.store.CreateCollection(ctx, collection, dim); err != nil {
			return err
		}
	}

	// Batch upsert (split into chunks of 50 for safety)
	batchSize := 50
	for i := 0; i < len(allPoints); i += batchSize {
		end := i + batchSize
		if end > len(allPoints) {
			end = len(allPoints)
		}
		if err := s.store.Upsert(ctx, collection, allPoints[i:end]); err != nil {
			return fmt.Errorf("failed to upsert batch: %w", err)
		}
	}

	return nil
}

func (s *Service) structToMap(obj interface{}) (map[string]interface{}, error) {
	data, err := json.Marshal(obj)
	if err != nil {
		return nil, err
	}
	var res map[string]interface{}
	err = json.Unmarshal(data, &res)
	return res, err
}

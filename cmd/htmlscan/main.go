package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"

	htmlanalyzer "github.com/doITmagic/coderag-mcp/internal/coderag/analyzers/html"
)

type chunkSummary struct {
	Name      string      `json:"name"`
	Type      string      `json:"type"`
	FilePath  string      `json:"file_path"`
	Signature string      `json:"signature"`
	Metadata  interface{} `json:"metadata,omitempty"`
	Snippet   string      `json:"snippet"`
}

func main() {
	flag.Parse()
	if flag.NArg() == 0 {
		log.Fatalf("usage: %s <path> [path...]", os.Args[0])
	}

	analyzer := htmlanalyzer.NewCodeAnalyzer()
	chunks, err := analyzer.AnalyzePaths(flag.Args())
	if err != nil {
		log.Fatalf("analyze: %v", err)
	}

	fmt.Printf("Found %d chunks\n", len(chunks))
	var summaries []chunkSummary
	for _, ch := range chunks {
		snippet := ch.Docstring
		if snippet == "" {
			snippet = ch.Code
		}
		if len(snippet) > 200 {
			snippet = snippet[:200] + "..."
		}
		summaries = append(summaries, chunkSummary{
			Name:      ch.Name,
			Type:      ch.Type,
			FilePath:  ch.FilePath,
			Signature: ch.Signature,
			Metadata:  ch.Metadata,
			Snippet:   snippet,
		})
	}

	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(summaries); err != nil {
		log.Fatalf("encode: %v", err)
	}
}

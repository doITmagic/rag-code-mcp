package parser_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/doITmagic/rag-code-mcp/pkg/parser"
	"github.com/doITmagic/rag-code-mcp/pkg/parser/docs"
	"github.com/doITmagic/rag-code-mcp/pkg/parser/javascript"
	"github.com/doITmagic/rag-code-mcp/pkg/parser/php"
	"github.com/doITmagic/rag-code-mcp/pkg/parser/python"
)

func TestStatefulAnalyzersAreConcurrentSafe(t *testing.T) {
	tests := []struct {
		name, ext string
		analyzer  parser.Analyzer
		source    func(int) string
	}{
		{"docs", ".json", docs.NewAnalyzer(), func(i int) string { return fmt.Sprintf(`{"value": %d}`, i) }},
		{"javascript", ".js", javascript.NewCodeAnalyzer(), func(i int) string {
			return fmt.Sprintf("export function function%d() { return %d }", i, i)
		}},
		{"php", ".php", php.NewAnalyzer(), func(i int) string { return fmt.Sprintf("<?php function function%d() {}", i) }},
		{"python", ".py", python.NewAnalyzer(), func(i int) string {
			return fmt.Sprintf("def function%d():\n    pass\n", i)
		}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var wg sync.WaitGroup
			errs := make(chan error, 8)
			for i := 0; i < cap(errs); i++ {
				path := filepath.Join(t.TempDir(), fmt.Sprintf("file%d%s", i, tt.ext))
				if err := os.WriteFile(path, []byte(tt.source(i)), 0o600); err != nil {
					t.Fatal(err)
				}
				wg.Add(1)
				go func() {
					defer wg.Done()
					result, err := tt.analyzer.Analyze(context.Background(), path)
					if err != nil {
						errs <- err
						return
					}
					for _, symbol := range result.Symbols {
						if symbol.FilePath != path {
							errs <- fmt.Errorf("%s returned symbol from %s", path, symbol.FilePath)
						}
					}
				}()
			}
			wg.Wait()
			close(errs)
			for err := range errs {
				t.Error(err)
			}
		})
	}
}

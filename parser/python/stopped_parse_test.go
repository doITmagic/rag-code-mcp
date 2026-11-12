package python

import (
	"bytes"
	"compress/gzip"
	"io"
	"os"
	"strings"
	"testing"

	"github.com/doITmagic/rag-code-mcp/v2/parser"
)

// A file whose parse runs into the timeout (a real module with a 200 KiB
// line of unclosed brackets, found by fuzzing) is an error, so the indexer
// counts it as failed instead of storing it as a file without symbols.
func TestParseTimeoutIsAnError(t *testing.T) {
	gz, err := os.ReadFile("testdata/unclosed_200k.py.gz")
	if err != nil {
		t.Fatal(err)
	}
	zr, err := gzip.NewReader(bytes.NewReader(gz))
	if err != nil {
		t.Fatal(err)
	}
	src, err := io.ReadAll(zr)
	if err != nil {
		t.Fatal(err)
	}
	syms, err := analyzer{}.Analyze(parser.File{Path: "/ws/auth.py", Root: "/ws", Source: src, Language: "python"})
	if err == nil || !strings.Contains(err.Error(), "parse stopped") {
		t.Fatalf("want a parse stopped error, got %d symbols and err %v", len(syms), err)
	}
}

package parser_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/doITmagic/rag-code-mcp/v2/parser"
)

// 20,000 symbols of one name on one line (a minified bundle) get their ids in
// linear time; the work runs in a child process so a regression is killed
// after 5 s instead of holding the suite.
func TestFinalizeManySameLineSymbols(t *testing.T) {
	const count = 20000 // a one-line JavaScript file of "var repeat;" that long is 220 KB
	f := parser.File{Path: filepath.Join(t.TempDir(), "bundle.js"), Language: "javascript"}
	syms := make([]parser.Symbol, count)
	if mode := os.Getenv("RAGCODE_TEST_COLLISION_CHILD"); mode != "" {
		for i := range syms {
			syms[i] = parser.Symbol{Name: "repeat", Kind: parser.KindVariable, StartLine: 1}
		}
		parser.Finalize(f, syms)
		return
	}
	for i := range syms {
		syms[i] = parser.Symbol{Name: fmt.Sprintf("unique%d", i), Kind: parser.KindVariable, StartLine: 1}
	}
	start := time.Now()
	parser.Finalize(f, syms)
	t.Logf("control: finalized %d distinct names in %s", count, time.Since(start))
	for _, mode := range []string{"finalize"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestFinalizeManySameLineSymbols$")
			cmd.Env = append(os.Environ(), "RAGCODE_TEST_COLLISION_CHILD="+mode)
			out, err := cmd.CombinedOutput()
			if errors.Is(ctx.Err(), context.DeadlineExceeded) {
				t.Fatalf("%s did not finish %d same-line duplicate symbols within 5s; isolated child was killed", mode, count)
			}
			if err != nil {
				t.Fatalf("child failed: %v\n%s", err, out)
			}
		})
	}
}

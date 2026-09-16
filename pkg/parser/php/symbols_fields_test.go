package php

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// Global functions must carry their file, lines and source like methods do,
// and method visibility must reach is_public.
func TestGlobalFunctionFieldsAndMethodVisibility(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "Pay.php")
	src := "<?php\nnamespace App;\n\nclass Gateway\n{\n    public function charge(int $c): bool { return $this->check($c); }\n    private function check(int $c): bool { return $c > 0; }\n}\n\nfunction helper() { return 1; }\n"
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	res, err := NewAnalyzer().Analyze(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	pub := map[string]bool{}
	var fn *struct {
		file, code string
		start, end int
	}
	for _, s := range res.Symbols {
		pub[s.Name] = s.IsPublic
		if s.Name == "helper" {
			fn = &struct {
				file, code string
				start, end int
			}{s.FilePath, s.Content, s.StartLine, s.EndLine}
		}
	}
	if fn == nil || fn.file != path || fn.start != 10 || fn.end != 10 || fn.code == "" {
		t.Fatalf("helper symbol = %+v", fn)
	}
	if !pub["charge"] || pub["check"] {
		t.Fatalf("visibility: charge=%v check=%v", pub["charge"], pub["check"])
	}
}

package html

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestHTMLAnalyzer(t *testing.T) {
	tmpDir := t.TempDir()
	
	code := `<!DOCTYPE html>
<html>
<head><title>Test Page</title></head>
<body>
	<h1>Welcome</h1>
	<h2>About Us</h2>
	<p>Some text</p>
</body>
</html>`

	filePath := filepath.Join(tmpDir, "index.html")
	if err := os.WriteFile(filePath, []byte(code), 0644); err != nil {
		t.Fatalf("failed to write test file: %v", err)
	}

	analyzer := NewAnalyzer()
	ctx := context.Background()

	res, err := analyzer.Analyze(ctx, filePath)
	if err != nil {
		t.Fatalf("Analyze failed: %v", err)
	}

	foundTitle := false
	foundH1 := false
	foundH2 := false

	for _, sym := range res.Symbols {
		if sym.Name == "title" && sym.Content == "Test Page" {
			foundTitle = true
		}
		if sym.Name == "h1" && sym.Content == "Welcome" {
			foundH1 = true
		}
		if sym.Name == "h2" && sym.Content == "About Us" {
			foundH2 = true
		}
	}

	if !foundTitle || !foundH1 || !foundH2 {
		t.Errorf("Missing symbols: title=%v, h1=%v, h2=%v", foundTitle, foundH1, foundH2)
	}
}

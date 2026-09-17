package html

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// Elements with an id or class become symbols, so a page can be found by
// the hooks its CSS/JS use and not only by its prose.
func TestAnalyzerEmitsElementSymbols(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "page.html")
	src := `<html><body>
<h1 id="top">Title</h1>
<div id="checkout-form" class="form wide">Pay here</div>
<span class="price-badge">9.99</span>
<span class="price-badge">1.00</span>
<p>no hooks</p>
</body></html>`
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	res, err := NewAnalyzer().Analyze(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, s := range res.Symbols {
		if s.Type == "element" {
			got[s.Name] = s.Signature
		}
	}
	want := map[string]string{
		"top":              `<h1 id="top">`,
		"checkout-form":    `<div id="checkout-form" class="form wide">`,
		"span.price-badge": `<span class="price-badge">`, // deduplicated
	}
	if len(got) != len(want) {
		t.Fatalf("element symbols = %v, want %v", got, want)
	}
	for name, sig := range want {
		if got[name] != sig {
			t.Errorf("%s: signature %q, want %q", name, got[name], sig)
		}
	}
}

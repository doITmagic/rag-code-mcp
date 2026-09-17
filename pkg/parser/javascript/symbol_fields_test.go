package javascript

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Every symbol carries its source (AST context and search snippets need it)
// and the module name as package, like the Python analyzer.
func TestSymbolsCarrySourceAndModulePackage(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "cart.js")
	src := strings.Join([]string{
		"export function addItem(cart, item) { return [...cart, item]; }",
		"",
		"export class Cart {",
		"  add(item) { this.items = addItem(this.items, item); }",
		"}",
	}, "\n")
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	res, err := NewCodeAnalyzer().Analyze(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{"addItem": "function addItem", "Cart": "class Cart", "Cart.add": "add(item)"}
	for _, s := range res.Symbols {
		if s.Package != "cart" {
			t.Errorf("%s: package %q, want cart", s.Name, s.Package)
		}
		if frag, ok := want[s.Name]; ok {
			if !strings.Contains(s.Content, frag) {
				t.Errorf("%s: content %q lacks %q", s.Name, s.Content, frag)
			}
			delete(want, s.Name)
		}
	}
	if len(want) != 0 {
		t.Fatalf("symbols missing: %v", want)
	}
}

// Calls made in a body become "calls" relations, so rag_find_usages and
// rag_call_hierarchy work for JS/TS as they do for Go, Python and PHP.
// Functions and an arrow are checked in TypeScript; the class in JavaScript,
// since tree-sitter does not yet yield classes with methods from .ts files.
func TestSymbolsCarryCallRelations(t *testing.T) {
	dir := t.TempDir()
	files := map[string]string{
		"shop.ts": strings.Join([]string{
			"function tax(v: number) { return v * 1.2; }",
			"export function total(items: number[]) { return tax(items.reduce((a, b) => a + b, 0)); }",
			"const quick = (x: number) => tax(x);",
		}, "\n"),
		"shop.js": strings.Join([]string{
			"export class Shop {",
			"  checkout(items) { this.log.info(total(items)); return new Date(); }",
			"}",
		}, "\n"),
	}
	calls := map[string][]string{}
	for name, src := range files {
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
		res, err := NewCodeAnalyzer().Analyze(context.Background(), path)
		if err != nil {
			t.Fatal(err)
		}
		for _, s := range res.Symbols {
			for _, r := range s.Relations {
				if r.Type == "calls" {
					calls[s.Name] = append(calls[s.Name], r.TargetName)
				}
			}
		}
	}
	want := map[string][]string{
		"total":         {"tax", "reduce"},
		"Shop.checkout": {"info", "total", "Date"},
		"quick":         {"tax"},
	}
	for name, exp := range want {
		if got := strings.Join(calls[name], ","); got != strings.Join(exp, ",") {
			t.Errorf("%s calls = %q, want %q", name, got, strings.Join(exp, ","))
		}
	}
}

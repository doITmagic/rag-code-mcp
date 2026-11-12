package parser

import "testing"

// Two symbols with one qualified name on one line (minified JS, a PHP
// property and method of the same name, CR-only line endings) keep distinct,
// stable IDs; the first one keeps the plain SymbolID.
func TestFinalizeSameNameSameLineDistinctIDs(t *testing.T) {
	mk := func() []Symbol {
		return Finalize(File{Path: "/ws/a.js", Language: "javascript"}, []Symbol{
			{Name: "g", Module: "a", StartLine: 1},
			{Name: "g", Module: "a", StartLine: 1},
			{Name: "g", Module: "a", StartLine: 1},
		})
	}
	a, b := mk(), mk()
	if a[0].ID != SymbolID("/ws/a.js", "a.g", 1) {
		t.Errorf("first symbol lost its plain ID")
	}
	if a[0].ID == a[1].ID || a[1].ID == a[2].ID || a[0].ID == a[2].ID {
		t.Errorf("IDs not distinct: %s %s %s", a[0].ID, a[1].ID, a[2].ID)
	}
	for i := range a {
		if a[i].ID != b[i].ID {
			t.Errorf("ID %d not stable between runs", i)
		}
	}
}

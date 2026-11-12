package python

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// A class whose last method is cut off mid-edit: its methods may be lost,
// but none may come out as a module-level function.
func TestBrokenClassMethodIsNoFunction(t *testing.T) {
	src := "class A:\n    def m(self):\n        pass\n    def n(self):\n        x = (\n\nclass B:\n    def k(self): pass\n"
	for _, s := range analyze(t, "/ws/a.py", "/ws", src) {
		if owner, ok := map[string]string{"m": "A", "n": "A", "k": "B"}[s.Name]; ok {
			assert.Equal(t, owner, s.Owner, "%s is a method of %s, got %s %s", s.Name, owner, s.Kind, s.QualifiedName)
		}
	}
}

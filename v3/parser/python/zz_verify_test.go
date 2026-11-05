package python

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/doITmagic/rag-code-mcp/v3/parser"
)

// Independent verification fixture: a fresh file exercising every import
// form and resolution rule from the spec.
const verifySrc = `import os
import a.b
import p.q as pq
import m1, m2
from pkg.util import helper, Klass as K
from a import x
from . import sib
from .. import up
from ..far import thing
from .local import loc
from .local import loc2 as L2

LIMIT = 10
_PRIVATE_MAX = 3
counter = 0
first = second = 1
typed: int = 5
_ = 0


@dataclass(frozen=True)
class Point:
    """A point.

    With detail.
    """
    x: int = 0

    def norm(self):
        return abs(self.x)


class Child(Point, K, a.b.Thing, sib.S, Missing):
    def go(self):
        self.norm()
        Point.norm(self)
        K.build()
        super().go()
        os.getcwd()
        a.b.run()
        a.foo()
        pq.call()
        m1.f()
        helper()
        x()
        x.attr()
        sib.s()
        up.u()
        thing.t()
        loc.l()
        L2.l()
        unknown.m()
        print("hi")
        free()
        self.x.y()

        def inner():
            self.deep()
            return [cb(i) for i in range(2)]
        return inner()


def free():
    """Esc\tape and \"quotes\"."""
    pass


async def afree(a: int) -> None:
    await free()
`

func verifyAnalyze(t *testing.T, path, root, src string) map[string]parser.Symbol {
	t.Helper()
	syms, err := analyzer{}.Analyze(parser.File{Path: path, Root: root, Source: []byte(src), Language: "python"})
	require.NoError(t, err)
	return byQN(syms)
}

func TestVerifyImportTable(t *testing.T) {
	m := verifyAnalyze(t, "/ws/pkg/sub/mod.py", "/ws", verifySrc)
	go_, ok := m["pkg.sub.mod.Child.go"]
	require.True(t, ok)
	assert.Equal(t, parser.KindMethod, go_.Kind)
	assert.Equal(t, "Child", go_.Owner)

	assert.Equal(t, map[string]string{
		"norm":   "pkg.sub.mod.Point.norm", // last write wins in the map; both checked below
		"build":  "pkg.util.Klass.build",   // from pkg.util import Klass as K
		"go":     "",                       // super().go()
		"getcwd": "os.getcwd",              // import os
		"run":    "a.b.run",                // import a.b -> a
		"foo":    "a.foo",                  // import a.b on line 2 puts a in the table
		"call":   "p.q.call",               // import p.q as pq
		"f":      "m1.f",                   // import m1, m2
		"helper": "pkg.util.helper",        // from pkg.util import helper
		"x":      "a.x",                    // from a import x
		"attr":   "a.x.attr",               // x.attr() with x an imported symbol
		"s":      "pkg.sub.sib.s",          // from . import sib
		"u":      "pkg.up.u",               // from .. import up
		"t":      "pkg.far.thing.t",        // from ..far import thing
		"l":      "pkg.sub.local.loc2.l",   // loc.l() and L2.l() share TargetName; see below
		"m":      "",                       // unknown receiver
		"print":  "",                       // builtin
		"free":   "pkg.sub.mod.free",       // module-level def
		"y":      "",                       // self.x.y() -> attribute chain, unknown
		"deep":   "pkg.sub.mod.Child.deep", // self.deep() inside nested def
		"cb":     "",                       // comprehension inside nested def
		"range":  "",                       // builtin in comprehension
		"inner":  "",                       // local nested def, not module-level
	}, targets(go_, parser.RelCall))

	// Both loc.l() and L2.l() resolve, and no "super" call is reported.
	var locs []string
	for _, r := range go_.Relations {
		assert.NotEqual(t, "super", r.TargetName)
		if r.TargetName == "l" {
			locs = append(locs, r.TargetQualifiedName)
		}
	}
	assert.ElementsMatch(t, []string{"pkg.sub.local.loc.l", "pkg.sub.local.loc2.l"}, locs)

	// Point.norm(self): ClassName.m() with ClassName in this file.
	var pointNorm []string
	for _, r := range go_.Relations {
		if r.TargetName == "norm" {
			pointNorm = append(pointNorm, r.TargetQualifiedName)
		}
	}
	assert.ElementsMatch(t, []string{"pkg.sub.mod.Child.norm", "pkg.sub.mod.Point.norm"}, pointNorm)

	// Extends through imports, local defs and unknown.
	assert.Equal(t, map[string]string{
		"Point":   "pkg.sub.mod.Point",
		"K":       "pkg.util.Klass",
		"Thing":   "a.b.Thing",
		"S":       "pkg.sub.sib.S",
		"Missing": "",
	}, targets(m["pkg.sub.mod.Child"], parser.RelExtends))
	for _, r := range m["pkg.sub.mod.Child"].Relations {
		assert.Equal(t, 33, r.Line)
	}
}

func TestVerifySymbols(t *testing.T) {
	m := verifyAnalyze(t, "/ws/pkg/sub/mod.py", "/ws", verifySrc)
	cases := []struct {
		qn       string
		kind     parser.Kind
		owner    string
		exported bool
	}{
		{"pkg.sub.mod.LIMIT", parser.KindConstant, "", true},
		{"pkg.sub.mod._PRIVATE_MAX", parser.KindConstant, "", false},
		{"pkg.sub.mod.counter", parser.KindVariable, "", true},
		{"pkg.sub.mod.first", parser.KindVariable, "", true},
		{"pkg.sub.mod.typed", parser.KindVariable, "", true},
		{"pkg.sub.mod._", parser.KindVariable, "", false},
		{"pkg.sub.mod.Point", parser.KindClass, "", true},
		{"pkg.sub.mod.Point.norm", parser.KindMethod, "Point", true},
		{"pkg.sub.mod.Child", parser.KindClass, "", true},
		{"pkg.sub.mod.free", parser.KindFunction, "", true},
		{"pkg.sub.mod.afree", parser.KindFunction, "", true},
	}
	for _, tc := range cases {
		s, ok := m[tc.qn]
		require.True(t, ok, tc.qn)
		assert.Equal(t, tc.kind, s.Kind, tc.qn)
		assert.Equal(t, tc.owner, s.Owner, tc.qn)
		assert.Equal(t, tc.exported, s.Exported, tc.qn)
		assert.Equal(t, parser.Qualify(s.Module, s.Owner, s.Name), s.QualifiedName)
	}
	// Chained assignment yields only the first target; "second" is not a symbol.
	_, hasSecond := m["pkg.sub.mod.second"]
	t.Logf("chained assignment second target present: %v", hasSecond)

	p := m["pkg.sub.mod.Point"]
	assert.Equal(t, "dataclass(frozen=True)", p.Metadata["decorators"])
	assert.Equal(t, "class Point", p.Signature)
	assert.Equal(t, "A point.\n\nWith detail.", p.Doc)
	assert.True(t, strings.HasPrefix(p.Content, "@dataclass"))
	assert.Equal(t, 21, p.StartLine)
	assert.Equal(t, 30, p.EndLine)
	// Class attribute x is not a symbol.
	_, hasX := m["pkg.sub.mod.Point.x"]
	assert.False(t, hasX)

	assert.Equal(t, "async def afree(a: int) -> None", m["pkg.sub.mod.afree"].Signature)
	assert.Equal(t, map[string]string{"free": "pkg.sub.mod.free"}, targets(m["pkg.sub.mod.afree"], parser.RelCall))
	t.Logf("docstring with escapes: %q", m["pkg.sub.mod.free"].Doc)
}

// from q import x must not make q callable as a module.
func TestVerifyFromDoesNotImportModule(t *testing.T) {
	m := verifyAnalyze(t, "/ws/mod.py", "/ws", "from q import x\nfrom r.s import y as z\ndef f():\n    q.foo()\n    r.s.bar()\n    r.bar()\n    z()\n    y()\n")
	assert.Equal(t, map[string]string{"foo": "", "bar": "", "z": "r.s.y", "y": ""}, targets(m["mod.f"], parser.RelCall))
}

func TestVerifyPyiAndRootModule(t *testing.T) {
	m := verifyAnalyze(t, "/ws/top.pyi", "/ws", "from . import nb\nfrom .k import kk\nclass A: ...\nclass B(A):\n    def m(self):\n        nb.f()\n        kk()\n")
	assert.Equal(t, map[string]string{"A": "top.A"}, targets(m["top.B"], parser.RelExtends))
	assert.Equal(t, map[string]string{"f": "nb.f", "kk": "k.kk"}, targets(m["top.B.m"], parser.RelCall))
	// Relative import above the root does not panic.
	m2 := verifyAnalyze(t, "/ws/top.py", "/ws", "from ...zz import q\ndef f():\n    q()\n")
	t.Logf("beyond-top relative: %v", targets(m2["top.f"], parser.RelCall))
}

func TestVerifyRobust(t *testing.T) {
	inputs := map[string]string{
		"one byte":   "x",
		"binary":     string([]byte{0, 1, 2, 0xff, 0xfe, '\n', 0x80}),
		"truncated":  "class A:\n    def m(self):\n        return foo(",
		"bad indent": "def f():\nreturn 1\n  def g():\n      x = 1\n    y = 2\nclass C:\n  def m(self):\n self.z()\n",
		"only deco":  "@dec\n",
		"deep":       strings.Repeat("(", 3000) + "1" + strings.Repeat(")", 3000),
		"tabs":       "def f():\n\tif True:\n\t\tg()\n\t\t\th()\n",
	}
	for name, src := range inputs {
		t.Run(name, func(t *testing.T) {
			done := make(chan struct{})
			go func() {
				defer close(done)
				syms, err := analyzer{}.Analyze(parser.File{Path: "/ws/r.py", Root: "/ws", Source: []byte(src), Language: "python"})
				if err != nil {
					t.Logf("err: %v", err)
					return
				}
				for _, s := range syms {
					assert.NotEmpty(t, s.QualifiedName)
					assert.NotEmpty(t, s.Name, "symbol with empty name: %q", s.Signature)
					assert.GreaterOrEqual(t, s.StartLine, 1)
					assert.GreaterOrEqual(t, s.EndLine, s.StartLine)
				}
			}()
			select {
			case <-done:
			case <-time.After(5 * time.Second):
				t.Fatal("hang")
			}
		})
	}
}

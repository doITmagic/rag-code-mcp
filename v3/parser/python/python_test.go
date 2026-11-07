package python

import (
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/doITmagic/rag-code-mcp/v3/parser"
)

const sample = `"""Module docstring."""
import os.path
import a.b as c
from x.y import z, w as v
from . import sib
from ..up import far
from .k import *

MAX_N: int = 2
name = "x"
_hidden, Pair = 1, 2


@dec(1)
@other
async def fetch(url: str, *args,
                timeout=2, **kw) -> str:
    """Fetch it.

        Indented detail.
    """
    foo()
    c.bar()
    os.path.join()
    z()
    v.run()
    sib.go()
    far.away()
    helper()
    Base.make()
    len(url)
    return (lambda: inner())()


def helper():
    [cb(i) for i in range(3)]

    def nested():
        deep()


class Base(c.Other, z, Unknown, Generic[T], metaclass=Meta):
    """Base class."""

    def _m(self):
        self.n()
        cls.p()
        super().m()
        Base.q()
        Outer.Inner.r()
        obj.z()

    @classmethod
    def p(cls):
        cls.n()


class Outer:
    class Inner(Base):
        def r(self):
            self.s()


@overload
def over(x: int) -> int: ...
@overload
def over(x: str) -> str: ...
def over(x): return x
`

func analyze(t *testing.T, path, root, src string) []parser.Symbol {
	t.Helper()
	syms, err := analyzer{}.Analyze(parser.File{Path: path, Root: root, Source: []byte(src), Language: "python"})
	require.NoError(t, err)
	return syms
}

func byQN(syms []parser.Symbol) map[string]parser.Symbol {
	m := map[string]parser.Symbol{}
	for _, s := range syms {
		if _, dup := m[s.QualifiedName]; !dup {
			m[s.QualifiedName] = s
		}
	}
	return m
}

func targets(s parser.Symbol, kind parser.RelationKind) map[string]string {
	m := map[string]string{}
	for _, r := range s.Relations {
		if r.Kind == kind {
			m[r.TargetName] = r.TargetQualifiedName
		}
	}
	return m
}

func TestSymbols(t *testing.T) {
	syms := analyze(t, "/ws/pkg/sub/mod.py", "/ws", sample)
	m := byQN(syms)

	cases := []struct {
		qn       string
		kind     parser.Kind
		owner    string
		exported bool
		start    int
		end      int
	}{
		{"pkg.sub.mod.MAX_N", parser.KindConstant, "", true, 9, 9},
		{"pkg.sub.mod.name", parser.KindVariable, "", true, 10, 10},
		{"pkg.sub.mod._hidden", parser.KindVariable, "", false, 11, 11},
		{"pkg.sub.mod.Pair", parser.KindVariable, "", true, 11, 11},
		{"pkg.sub.mod.fetch", parser.KindFunction, "", true, 14, 32},
		{"pkg.sub.mod.helper", parser.KindFunction, "", true, 35, 39},
		{"pkg.sub.mod.Base", parser.KindClass, "", true, 42, 55},
		{"pkg.sub.mod.Base._m", parser.KindMethod, "Base", false, 45, 51},
		{"pkg.sub.mod.Base.p", parser.KindMethod, "Base", true, 53, 55},
		{"pkg.sub.mod.Outer", parser.KindClass, "", true, 58, 61},
		{"pkg.sub.mod.Outer.Inner", parser.KindClass, "Outer", true, 59, 61},
		{"pkg.sub.mod.Outer.Inner.r", parser.KindMethod, "Outer.Inner", true, 60, 61},
	}
	for _, tc := range cases {
		t.Run(tc.qn, func(t *testing.T) {
			s, ok := m[tc.qn]
			require.True(t, ok, "missing %s", tc.qn)
			assert.Equal(t, tc.kind, s.Kind)
			assert.Equal(t, tc.owner, s.Owner)
			assert.Equal(t, tc.exported, s.Exported)
			assert.Equal(t, tc.start, s.StartLine)
			assert.Equal(t, tc.end, s.EndLine)
			assert.Equal(t, "pkg.sub.mod", s.Module)
			assert.Equal(t, "python", s.Language)
			assert.NotEmpty(t, s.ID)
		})
	}

	f := m["pkg.sub.mod.fetch"]
	assert.Equal(t, "async def fetch(url: str, *args, timeout=2, **kw) -> str", f.Signature)
	assert.Equal(t, "Fetch it.\n\nIndented detail.", f.Doc)
	assert.Equal(t, "dec(1),other", f.Metadata["decorators"])
	assert.True(t, len(f.Content) > 0 && f.Content[0] == '@', "content starts at the decorator")
	assert.Equal(t, "class Base(c.Other, z, Unknown, Generic[T], metaclass=Meta)", m["pkg.sub.mod.Base"].Signature)
	assert.Equal(t, "Base class.", m["pkg.sub.mod.Base"].Doc)
	assert.Equal(t, "MAX_N: int", m["pkg.sub.mod.MAX_N"].Signature)
	assert.Equal(t, "classmethod", m["pkg.sub.mod.Base.p"].Metadata["decorators"])

	// Overloads stay separate symbols with distinct IDs.
	var overs []parser.Symbol
	for _, s := range syms {
		if s.Name == "over" {
			overs = append(overs, s)
		}
	}
	require.Len(t, overs, 3)
	assert.NotEqual(t, overs[0].ID, overs[1].ID)
	assert.Equal(t, "overload", overs[0].Metadata["decorators"])
	assert.Empty(t, overs[2].Metadata)
}

func TestCalls(t *testing.T) {
	m := byQN(analyze(t, "/ws/pkg/sub/mod.py", "/ws", sample))

	assert.Equal(t, map[string]string{
		"foo":    "",                   // unknown free name
		"bar":    "a.b.bar",            // import a.b as c
		"join":   "os.path.join",       // import os.path
		"z":      "x.y.z",              // from x.y import z
		"run":    "x.y.w.run",          // from x.y import w as v
		"go":     "pkg.sub.sib.go",     // from . import sib
		"away":   "pkg.up.far.away",    // from ..up import far
		"helper": "pkg.sub.mod.helper", // defined later in this file
		"make":   "pkg.sub.mod.Base.make",
		"len":    "", // builtin
		"inner":  "", // inside a lambda, still attributed to fetch
	}, targets(m["pkg.sub.mod.fetch"], parser.RelCall))

	helper := targets(m["pkg.sub.mod.helper"], parser.RelCall)
	assert.Equal(t, "", helper["cb"], "comprehension call counted")
	assert.Equal(t, "", helper["deep"], "nested def call counted")
	assert.Contains(t, helper, "range")

	assert.Equal(t, map[string]string{
		"n": "pkg.sub.mod.Base.n",
		"p": "pkg.sub.mod.Base.p",
		"m": "", // super()
		"q": "pkg.sub.mod.Base.q",
		"r": "pkg.sub.mod.Outer.Inner.r",
		"z": "", // unknown receiver
	}, targets(m["pkg.sub.mod.Base._m"], parser.RelCall))
	assert.Equal(t, map[string]string{"n": "pkg.sub.mod.Base.n"}, targets(m["pkg.sub.mod.Base.p"], parser.RelCall))
	assert.Equal(t, map[string]string{"s": "pkg.sub.mod.Outer.Inner.s"}, targets(m["pkg.sub.mod.Outer.Inner.r"], parser.RelCall))

	// Call lines point at the call, not the function.
	for _, r := range m["pkg.sub.mod.fetch"].Relations {
		if r.TargetName == "foo" {
			assert.Equal(t, 22, r.Line)
		}
	}
}

func TestExtends(t *testing.T) {
	m := byQN(analyze(t, "/ws/pkg/sub/mod.py", "/ws", sample))
	assert.Equal(t, map[string]string{
		"Other":   "a.b.Other",
		"z":       "x.y.z",
		"Unknown": "",
		"Generic": "",
	}, targets(m["pkg.sub.mod.Base"], parser.RelExtends))
	base := m["pkg.sub.mod.Base"]
	require.NotEmpty(t, base.Relations)
	assert.Equal(t, 42, base.Relations[0].Line)
	assert.Equal(t, map[string]string{"Base": "pkg.sub.mod.Base"}, targets(m["pkg.sub.mod.Outer.Inner"], parser.RelExtends))
}

func TestModuleName(t *testing.T) {
	cases := []struct {
		path, root, module string
		pkg                bool
	}{
		{"/ws/pkg/mod.py", "/ws", "pkg.mod", false},
		{"/ws/pkg/__init__.py", "/ws", "pkg", true},
		{"/ws/__init__.py", "/ws", "", true},
		{"/ws/top.pyi", "/ws", "top", false},
		{"/elsewhere/x.py", "/ws", "x", false},
		{"/ws/x.py", "", "x", false},
	}
	for _, tc := range cases {
		mod, pkg := moduleName(parser.File{Path: tc.path, Root: tc.root})
		assert.Equal(t, tc.module, mod, tc.path)
		assert.Equal(t, tc.pkg, pkg, tc.path)
	}
}

func TestRelativeImportsFromInit(t *testing.T) {
	src := "from . import a\nfrom .b import c\nfrom .. import d\n\ndef f():\n    a.x()\n    c()\n    d.y()\n"
	m := byQN(analyze(t, "/ws/pkg/sub/__init__.py", "/ws", src))
	f, ok := m["pkg.sub.f"]
	require.True(t, ok)
	assert.Equal(t, map[string]string{"x": "pkg.sub.a.x", "c": "pkg.sub.b.c", "y": "pkg.d.y"}, targets(f, parser.RelCall))
}

func TestMalformed(t *testing.T) {
	src := "def ok():\n    pass\n\ndef  broken(:\n  x\n   y = 1\n class Z(:\n\tdef m(self): self.a()\n"
	syms := analyze(t, "/ws/m.py", "/ws", src)
	m := byQN(syms)
	ok, found := m["m.ok"]
	require.True(t, found)
	assert.Equal(t, parser.KindFunction, ok.Kind)
	assert.Equal(t, 1, ok.StartLine)
	for _, s := range syms {
		assert.NotEmpty(t, s.QualifiedName)
		assert.LessOrEqual(t, s.StartLine, s.EndLine)
	}
	empty := analyze(t, "/ws/e.py", "/ws", "")
	assert.Empty(t, empty)
	garbage := analyze(t, "/ws/g.py", "/ws", "\x00\xff(((")
	for _, s := range garbage {
		assert.NotEmpty(t, s.QualifiedName)
	}
}

func TestRegistered(t *testing.T) {
	a := parser.ForFile("/ws/x.py")
	require.NotNil(t, a)
	assert.Equal(t, "python", a.Language())
	assert.NotNil(t, parser.ForFile("/ws/x.PYI"))
	syms, err := parser.Analyze(parser.File{Path: "/ws/x.py", Root: "/ws", Source: []byte("def f(): pass\n")})
	require.NoError(t, err)
	require.Len(t, syms, 1)
	assert.Equal(t, "python", syms[0].Language)
}

func TestConcurrent(t *testing.T) {
	want := analyze(t, "/ws/pkg/sub/mod.py", "/ws", sample)
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 5; j++ {
				got := analyze(t, "/ws/pkg/sub/mod.py", "/ws", sample)
				assert.Equal(t, want, got)
			}
		}()
	}
	wg.Wait()
}

func TestPackageReexport(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, "app", "models"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(root, "app", "models", "__init__.py"), []byte("from .user import User as User\n"), 0o644))
	syms := analyze(t, filepath.Join(root, "views.py"), root, `
import app.models
from app.models import User
from app import models

def v():
    User.find()
    models.User()
    app.models.User.save()

class Admin(User): pass
`)
	got := byQN(syms)
	calls := map[string]bool{}
	for _, r := range got["views.v"].Relations {
		calls[r.TargetQualifiedName] = true
	}
	assert.Equal(t, map[string]bool{"app.models.user.User.find": true, "app.models.user.User": true, "app.models.user.User.save": true}, calls)
	assert.Equal(t, map[string]string{"User": "app.models.user.User"}, targets(got["views.Admin"], parser.RelExtends))
}

// A package under a source root below the workspace root (src/ layout) is
// still followed, and a symlinked __init__.py is never read: it may point at
// /dev/zero.
func TestPackageReexportSourceRoot(t *testing.T) {
	root := t.TempDir()
	pkg := filepath.Join(root, "src", "mypkg")
	require.NoError(t, os.MkdirAll(filepath.Join(pkg, "dev"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(pkg, "__init__.py"), []byte("from .user import User\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(root, "link.py"), []byte("from .impl import x\n"), 0o644))
	require.NoError(t, os.Symlink(filepath.Join(root, "link.py"), filepath.Join(pkg, "dev", "__init__.py")))
	syms := analyze(t, filepath.Join(pkg, "api.py"), root, `
from mypkg import User
from mypkg import dev

def v():
    User.find()
    dev.x()
`)
	assert.Equal(t, map[string]string{"find": "src.mypkg.user.User.find", "x": "mypkg.dev.x"}, targets(byQN(syms)["src.mypkg.api.v"], parser.RelCall))
}

// The parsed __init__.py table is shared process-wide by mtime+size: a
// rewrite with the same size and mtime is served from the cache (no
// re-parse), a size change is re-parsed.
func TestPackageReexportCached(t *testing.T) {
	root := t.TempDir()
	init := filepath.Join(root, "app", "__init__.py")
	require.NoError(t, os.MkdirAll(filepath.Dir(init), 0o755))
	require.NoError(t, os.WriteFile(init, []byte("from .user import User\n"), 0o644))
	find := func() string {
		syms := analyze(t, filepath.Join(root, "a.py"), root, "from app import User\n\ndef v():\n    User.find()\n")
		return targets(byQN(syms)["a.v"], parser.RelCall)["find"]
	}
	assert.Equal(t, "app.user.User.find", find())
	info, err := os.Lstat(init)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(init, []byte("from .usar import User\n"), 0o644))
	require.NoError(t, os.Chtimes(init, info.ModTime(), info.ModTime()))
	assert.Equal(t, "app.user.User.find", find(), "same mtime+size: cached table reused")
	require.NoError(t, os.WriteFile(init, []byte("from .person import User\n"), 0o644))
	assert.Equal(t, "app.person.User.find", find(), "size changed: re-parsed")
}

func TestLocalsShadowImports(t *testing.T) {
	m := byQN(analyze(t, "/ws/mod.py", "/ws", `from disk import remove
import os

def invoke(remove): remove()
def f(os): os.remove()
def typed(os: Foo = None, *remove): os.x(); remove()
def assigned():
    os = make()
    os.remove()
    for remove in items: remove()
    with open() as os: os.y()
def real():
    remove()
    os.stat()
def lam(): return lambda remove: remove()
class K:
    def m(self, os):
        self.n()
        os.z()
    def n(self): pass
`))
	call := func(fn string) map[string]string { return targets(m["mod."+fn], parser.RelCall) }
	assert.Equal(t, "", call("invoke")["remove"])
	assert.Equal(t, "", call("f")["remove"])
	assert.Equal(t, "", call("typed")["x"])
	assert.Equal(t, "", call("typed")["remove"])
	assert.Equal(t, "", call("assigned")["remove"])
	assert.Equal(t, "", call("assigned")["y"])
	assert.Equal(t, "disk.remove", call("real")["remove"])
	assert.Equal(t, "os.stat", call("real")["stat"])
	assert.Equal(t, "", call("lam")["remove"])
	assert.Equal(t, "", call("K.m")["z"])
	assert.Equal(t, "mod.K.n", call("K.m")["n"])
}

// An import inside a function resolves the calls of that function only; a
// call in a class body outside the methods (a field default) belongs to the
// class.
func TestLocalImportsAndClassBodyCalls(t *testing.T) {
	src := `from helpers import guids

def activate(context_id):
    from agent import AgentContext
    return AgentContext.get(context_id)

def other():
    return AgentContext.get(1)

class Task(Model):
    uuid: str = Field(default_factory=lambda: guids.generate_id())

    def run(self):
        return guids.other()
`
	m := byQN(analyze(t, "/ws/pkg/projects.py", "/ws", src))
	rels := func(qn string) map[string]string {
		out := map[string]string{}
		for _, r := range m[qn].Relations {
			out[r.TargetName] = r.TargetQualifiedName
		}
		return out
	}
	assert.Equal(t, "agent.AgentContext.get", rels("pkg.projects.activate")["get"])
	assert.Equal(t, "", rels("pkg.projects.other")["get"], "the import was local to activate")
	task := rels("pkg.projects.Task")
	assert.Equal(t, "helpers.guids.generate_id", task["generate_id"])
	assert.Contains(t, task, "Field", "unknown, but still a call of the class")
	assert.NotContains(t, task, "other", "the method's calls are its own")
	assert.Equal(t, "helpers.guids.other", rels("pkg.projects.Task.run")["other"])
}

// Comments between the colon and the first statement are not the signature.
func TestSignatureStopsAtColon(t *testing.T) {
	src := "class W:\n    def get_variables(self, x: dict[str, int] = {}) -> dict[str, Any]:\n        # agent = kwargs.get('_agent')\n        # return {}\n        return {}\n"
	m := byQN(analyze(t, "/ws/w.py", "/ws", src))
	assert.Equal(t, "def get_variables(self, x: dict[str, int] = {}) -> dict[str, Any]", m["w.W.get_variables"].Signature)
	assert.Equal(t, "class W", m["w.W"].Signature)
}

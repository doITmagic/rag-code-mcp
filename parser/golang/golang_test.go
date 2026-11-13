package golang

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/doITmagic/rag-code-mcp/v2/parser"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const fixture = `// Package svc is the fixture.
package svc

import (
	"fmt"
	str "strings"
	. "math"
	_ "embed"
	"github.com/acme/app/pkg/db"
)

// Server doc.
// Second line.
type Server[K comparable] struct {
	Base
	*db.Conn
	Name string
}

/* Handler doc */
type Handler interface {
	fmt.Stringer
	Handle(ctx int) error
	Close()
}

type (
	// ID doc
	ID = int
	Names []string
)

var x, y = 1, helper()

//go:embed data
var blob string

const (
	// First is one.
	First = iota
	second
)

// Run doc
func Run[V any](a int, b ...string) (int, error) {
	helper()
	str.ToUpper("a")
	a.Do()
	Server.Start(nil)
	(*Server).Stop(nil)
	Server{}.Start()
	s.Inner.Do()
	_ = fmt.Sprintf("%d", 1)
	_ = []byte("x")
	_ = str.Builder{}
	_ = db.Conn{}
	_ = Names{}
	f := func() { later() }
	f()
	return len(b), nil
}

func (s *Server[K]) Start() {
	s.Stop()
	h := s.Start
	h()
	s.Conn.Query()
}

func (_ Server[K]) Stop() {}

func (Server[K]) noRecv() {}
`

func analyze(t *testing.T, root, path, src string) map[string]parser.Symbol {
	t.Helper()
	syms, err := Analyzer{}.Analyze(parser.File{Path: path, Root: root, Source: []byte(src), Language: "go"})
	require.NoError(t, err)
	out := map[string]parser.Symbol{}
	for _, s := range syms {
		assert.NotEmpty(t, s.ID)
		assert.Equal(t, path, s.FilePath)
		assert.Equal(t, "go", s.Language)
		out[s.QualifiedName] = s
	}
	return out
}

func rel(kind parser.RelationKind, name, tqn string, line int) parser.Relation {
	return parser.Relation{Kind: kind, TargetName: name, TargetQualifiedName: tqn, Line: line}
}

func TestFixtureSymbols(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "go.mod"), []byte("module github.com/acme/app\n\ngo 1.25\n"), 0o644))
	require.NoError(t, os.MkdirAll(filepath.Join(root, "internal", "svc"), 0o755))
	path := filepath.Join(root, "internal", "svc", "svc.go")
	syms := analyze(t, root, path, fixture)
	const mod = "github.com/acme/app/internal/svc"

	tests := []struct {
		qn       string
		kind     parser.Kind
		owner    string
		exported bool
		start    int
		end      int
		sig      string
		doc      string
	}{
		{mod + ".Server", parser.KindClass, "", true, 14, 18, "type Server[K comparable] struct", "Server doc.\nSecond line."},
		{mod + ".Handler", parser.KindInterface, "", true, 21, 25, "type Handler interface", "Handler doc"},
		{mod + ".ID", parser.KindType, "", true, 29, 29, "ID = int", "ID doc"},
		{mod + ".Names", parser.KindType, "", true, 30, 30, "Names []string", ""},
		{mod + ".x", parser.KindVariable, "", false, 33, 33, "var x, y = 1, helper()", ""},
		{mod + ".y", parser.KindVariable, "", false, 33, 33, "var x, y = 1, helper()", ""},
		{mod + ".blob", parser.KindVariable, "", false, 36, 36, "var blob string", ""},
		{mod + ".First", parser.KindConstant, "", true, 40, 40, "First = iota", "First is one."},
		{mod + ".second", parser.KindConstant, "", false, 41, 41, "second", ""},
		{mod + ".Run", parser.KindFunction, "", true, 45, 61, "func Run[V any](a int, b ...string) (int, error)", "Run doc"},
		{mod + ".Server.Start", parser.KindMethod, "Server", true, 63, 68, "func (s *Server[K]) Start()", ""},
		{mod + ".Server.Stop", parser.KindMethod, "Server", true, 70, 70, "func (_ Server[K]) Stop()", ""},
		{mod + ".Server.noRecv", parser.KindMethod, "Server", false, 72, 72, "func (Server[K]) noRecv()", ""},
	}
	assert.Len(t, syms, len(tests))
	for _, tc := range tests {
		t.Run(tc.qn, func(t *testing.T) {
			s, ok := syms[tc.qn]
			require.True(t, ok, "missing symbol %s", tc.qn)
			assert.Equal(t, tc.kind, s.Kind)
			assert.Equal(t, mod, s.Module)
			assert.Equal(t, tc.owner, s.Owner)
			assert.Equal(t, tc.exported, s.Exported)
			assert.Equal(t, tc.start, s.StartLine)
			assert.Equal(t, tc.end, s.EndLine)
			assert.Equal(t, tc.sig, s.Signature)
			assert.Equal(t, tc.doc, s.Doc)
			assert.Equal(t, parser.SymbolID(path, tc.qn, tc.start), s.ID)
		})
	}
	assert.Equal(t, "type Server[K comparable] struct {\n\tBase\n\t*db.Conn\n\tName string\n}", syms[mod+".Server"].Content)
	assert.Equal(t, "var x, y = 1, helper()", syms[mod+".x"].Content)
	assert.Equal(t, "second", syms[mod+".second"].Content)
	assert.Equal(t, "Handle,Close", syms[mod+".Handler"].Metadata["methods"])
	assert.Nil(t, syms[mod+".Server"].Metadata)
}

func TestFixtureRelations(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "go.mod"), []byte("module github.com/acme/app\n"), 0o644))
	path := filepath.Join(root, "svc.go")
	syms := analyze(t, root, path, fixture)
	const mod = "github.com/acme/app"

	tests := []struct {
		qn   string
		want []parser.Relation
	}{
		{mod + ".Server", []parser.Relation{
			rel(parser.RelExtends, "Base", mod+".Base", 15),
			rel(parser.RelExtends, "Conn", "github.com/acme/app/pkg/db.Conn", 16),
		}},
		{mod + ".Handler", []parser.Relation{
			rel(parser.RelExtends, "Stringer", "fmt.Stringer", 22),
		}},
		{mod + ".x", []parser.Relation{rel(parser.RelCall, "helper", mod+".helper", 33)}},
		{mod + ".y", []parser.Relation{rel(parser.RelCall, "helper", mod+".helper", 33)}},
		{mod + ".Run", []parser.Relation{
			rel(parser.RelCall, "helper", mod+".helper", 46),
			rel(parser.RelCall, "ToUpper", "strings.ToUpper", 47),
			rel(parser.RelCall, "Do", "", 48),
			rel(parser.RelCall, "Start", mod+".Server.Start", 49),
			rel(parser.RelCall, "Stop", mod+".Server.Stop", 50),
			rel(parser.RelCall, "Start", mod+".Server.Start", 51),
			rel(parser.RelUsesType, "Server", mod+".Server", 51),
			rel(parser.RelCall, "Do", "", 52),
			rel(parser.RelCall, "Sprintf", "fmt.Sprintf", 53),
			rel(parser.RelUsesType, "Builder", "strings.Builder", 55),
			rel(parser.RelUsesType, "Conn", "github.com/acme/app/pkg/db.Conn", 56),
			rel(parser.RelUsesType, "Names", mod+".Names", 57),
			rel(parser.RelCall, "later", mod+".later", 58),
		}},
		{mod + ".Server.Start", []parser.Relation{
			rel(parser.RelCall, "Stop", mod+".Server.Stop", 64),
			{Kind: parser.RelCall, TargetName: "Start", TargetQualifiedName: mod + ".Server.Start", Line: 65, Value: true}, // h := s.Start
			rel(parser.RelCall, "Query", "", 67),
		}},
		{mod + ".Server.Stop", nil},
	}
	for _, tc := range tests {
		t.Run(tc.qn, func(t *testing.T) {
			s, ok := syms[tc.qn]
			require.True(t, ok, "missing symbol %s", tc.qn)
			assert.Equal(t, tc.want, s.Relations)
		})
	}
}

func TestNoGoMod(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "a", "b", "f.go")
	syms := analyze(t, root, path, "package b\n\nfunc F() {}\n")
	require.Contains(t, syms, "a/b.F")
	assert.Equal(t, "a/b", syms["a/b.F"].Module)

	// File directly in the root: module is the empty string.
	syms = analyze(t, root, filepath.Join(root, "f.go"), "package r\n\nfunc G() {}\n")
	require.Contains(t, syms, "G")
	assert.Equal(t, "", syms["G"].Module)
}

func TestNestedGoModStopsAtRoot(t *testing.T) {
	outer := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(outer, "go.mod"), []byte("module outer\n"), 0o644))
	root := filepath.Join(outer, "ws")
	require.NoError(t, os.MkdirAll(filepath.Join(root, "sub"), 0o755))
	syms := analyze(t, root, filepath.Join(root, "sub", "f.go"), "package sub\n\nfunc F() {}\n")
	assert.Contains(t, syms, "sub.F", "go.mod above the root must be ignored")

	// A go.mod at the root wins over the outer one (fresh dir: the lookup is cached per directory).
	require.NoError(t, os.MkdirAll(filepath.Join(root, "sub2"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(root, "go.mod"), []byte("module \"example.com/ws\"\n"), 0o644))
	syms = analyze(t, root, filepath.Join(root, "sub2", "g.go"), "package sub\n\nfunc G() {}\n")
	assert.Contains(t, syms, "example.com/ws/sub2.G")
}

// A renamed module shows up without a restart: the cached lookup is checked
// against go.mod's mtime.
func TestGoModRenameSeen(t *testing.T) {
	root := t.TempDir()
	gomod := filepath.Join(root, "go.mod")
	require.NoError(t, os.WriteFile(gomod, []byte("module old.example/app\n"), 0o644))
	path := filepath.Join(root, "pkg", "f.go")
	assert.Contains(t, analyze(t, root, path, "package pkg\n\nfunc F() {}\n"), "old.example/app/pkg.F")

	require.NoError(t, os.WriteFile(gomod, []byte("module new.example/app\n"), 0o644))
	require.NoError(t, os.Chtimes(gomod, time.Now(), time.Now().Add(time.Second)))
	assert.Contains(t, analyze(t, root, path, "package pkg\n\nfunc F() {}\n"), "new.example/app/pkg.F")
}

// A go.mod rewritten with the same mtime within two seconds of its read
// (coarse timestamps, cp -p) is read again: a racy mtime is not cached.
func TestGoModRacyRewriteSeen(t *testing.T) {
	root := t.TempDir()
	gomod := filepath.Join(root, "go.mod")
	now := time.Now().Truncate(time.Second) // whole seconds: ecryptfs drops the rest of a Chtimes
	require.NoError(t, os.WriteFile(gomod, []byte("module aaa.example/app\n"), 0o644))
	require.NoError(t, os.Chtimes(gomod, now, now))
	path := filepath.Join(root, "pkg", "f.go")
	assert.Contains(t, analyze(t, root, path, "package pkg\n\nfunc F() {}\n"), "aaa.example/app/pkg.F")

	require.NoError(t, os.WriteFile(gomod, []byte("module bbb.example/app\n"), 0o644))
	require.NoError(t, os.Chtimes(gomod, now, now))
	assert.Contains(t, analyze(t, root, path, "package pkg\n\nfunc F() {}\n"), "bbb.example/app/pkg.F")
}

func TestMalformed(t *testing.T) {
	tests := []struct {
		name string
		src  string
	}{
		{"empty", ""},
		{"garbage", "}}}} func ( {{{"},
		{"unterminated", "package p\n\nfunc Broken(a int {\n\tfoo(\n\nfunc OK() {}\n"},
		{"no name", "package p\n\nfunc () {}\ntype struct{}\nvar = 1\n"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			syms, err := Analyzer{}.Analyze(parser.File{Path: "/x/p.go", Root: "/x", Source: []byte(tc.src), Language: "go"})
			require.NoError(t, err)
			for _, s := range syms {
				assert.NotEmpty(t, s.Name)
				assert.GreaterOrEqual(t, s.EndLine, s.StartLine)
			}
		})
	}
	syms, err := Analyzer{}.Analyze(parser.File{Path: "/x/p.go", Root: "/x", Source: []byte("package p\n\nfunc Broken(a int {\n\tfoo(\n}\n\nfunc OK() {}\n"), Language: "go"})
	require.NoError(t, err)
	names := map[string]bool{}
	for _, s := range syms {
		names[s.Name] = true
	}
	assert.True(t, names["OK"], "declarations after a broken one are still found: %v", names)
}

func TestSelfParserFile(t *testing.T) {
	_, this, _, ok := runtime.Caller(0)
	require.True(t, ok)
	root := filepath.Clean(filepath.Join(filepath.Dir(this), "..", ".."))
	path := filepath.Join(root, "parser", "parser.go")
	src, err := os.ReadFile(path)
	require.NoError(t, err)
	syms := analyze(t, root, path, string(src))

	const mod = "github.com/doITmagic/rag-code-mcp/v2/parser"
	q, ok := syms[mod+".Qualify"]
	require.True(t, ok)
	assert.Equal(t, mod, q.Module)
	assert.Equal(t, parser.KindFunction, q.Kind)
	assert.True(t, q.Exported)

	id, ok := syms[mod+".SymbolID"]
	require.True(t, ok)
	var found bool
	for _, r := range id.Relations {
		if r.Kind == parser.RelCall && r.TargetName == "Sum256" {
			assert.Equal(t, "crypto/sha256.Sum256", r.TargetQualifiedName)
			found = true
		}
	}
	assert.True(t, found, "SymbolID must call sha256.Sum256: %+v", id.Relations)

	fin := syms[mod+".Finalize"]
	assert.Contains(t, fin.Relations, rel(parser.RelCall, "SymbolID", mod+".SymbolID", fin.EndLine-12))
	assert.Equal(t, parser.KindInterface, syms[mod+".Analyzer"].Kind)
	assert.Equal(t, "Language,Extensions,Analyze", syms[mod+".Analyzer"].Metadata["methods"])
	assert.Equal(t, parser.KindConstant, syms[mod+".KindFunction"].Kind)
}

func TestRegistered(t *testing.T) {
	a := parser.ForFile("/x/y.GO")
	require.NotNil(t, a)
	assert.Equal(t, "go", a.Language())
	assert.Equal(t, []string{".go"}, a.Extensions())
}

func TestConcurrent(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "go.mod"), []byte("module m\n"), 0o644))
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 5; j++ {
				syms, err := Analyzer{}.Analyze(parser.File{Path: filepath.Join(root, "f.go"), Root: root, Source: []byte(fixture), Language: "go"})
				assert.NoError(t, err)
				assert.Len(t, syms, 13)
			}
		}()
	}
	wg.Wait()
}

func TestGenerics(t *testing.T) {
	root := t.TempDir()
	src := `package g

import "github.com/acme/app/pkg/db"

type Box[T any] struct{}

func F[T any](x T) {
	F[int](1)
	db.Open[int](x)
	_ = Box[int](x)
	_ = db.T[int]{}
	_ = (*db.T[int])(x)
	_ = int64(x)
}
`
	syms := analyze(t, root, filepath.Join(root, "g.go"), src)
	require.Contains(t, syms, "F")
	assert.Equal(t, []parser.Relation{
		rel(parser.RelCall, "F", "F", 8),
		rel(parser.RelCall, "Open", "github.com/acme/app/pkg/db.Open", 9),
		rel(parser.RelUsesType, "Box", "Box", 10),
		rel(parser.RelUsesType, "T", "github.com/acme/app/pkg/db.T", 11),
		rel(parser.RelUsesType, "T", "github.com/acme/app/pkg/db.T", 12),
	}, syms["F"].Relations)
}

// A parameter inside a func type is no local, and a closure's parameter types
// no use outside that closure: s.Get() there is on the package-level s.
func TestClosureParamTypesOnlyItsClosure(t *testing.T) {
	root := t.TempDir()
	src := `package g

var s = &Store{}

func Do(fn func(s *Server)) int {
	return s.Get()
}

func Later() int {
	f := func(s *Server) int { return s.Get() }
	_ = f
	return s.Get()
}

func Inner() {
	_ = func(s *Server) { s.Get() }
}
`
	syms := analyze(t, root, filepath.Join(root, "g.go"), src)
	assert.Contains(t, syms["Do"].Relations, rel(parser.RelCall, "Get", "", 6))
	assert.Contains(t, syms["Later"].Relations, rel(parser.RelCall, "Get", "", 10))
	assert.Contains(t, syms["Later"].Relations, rel(parser.RelCall, "Get", "", 12))
	assert.Contains(t, syms["Inner"].Relations, rel(parser.RelCall, "Get", "Server.Get", 16))
}

// A parameter or local named like an import shadows it: its method call
// targets the local's declared type, not the package function of that name,
// and has no target when the type is not declared (x := f()).
func TestLocalShadowsImport(t *testing.T) {
	root := t.TempDir()
	src := `package g

import "github.com/acme/app/pkg/db"

func F(db *Conn) {
	db.Close()
}

func G() {
	db.Close()
}

func H() {
	db := open()
	db.Close()
}
`
	syms := analyze(t, root, filepath.Join(root, "g.go"), src)
	assert.Contains(t, syms["F"].Relations, rel(parser.RelCall, "Close", "Conn.Close", 6))
	assert.Contains(t, syms["H"].Relations, rel(parser.RelCall, "Close", "", 15))
	assert.Equal(t, []parser.Relation{rel(parser.RelCall, "Close", "github.com/acme/app/pkg/db.Close", 10)}, syms["G"].Relations)
}

// A local named like the receiver shadows it: its method call has an
// unknown target, not the receiver type's method; the receiver still binds
// where nothing shadows it.
func TestLocalShadowsReceiver(t *testing.T) {
	root := t.TempDir()
	src := `package g

type A struct{}

func (s *A) X(bs []*B) {
	for _, s := range bs {
		s.Close()
	}
}

func (s *A) Y() {
	s.Close()
}
`
	syms := analyze(t, root, filepath.Join(root, "g.go"), src)
	var x, y parser.Symbol
	for q, s := range syms {
		switch {
		case strings.HasSuffix(q, "A.X"):
			x = s
		case strings.HasSuffix(q, "A.Y"):
			y = s
		}
	}
	require.NotEmpty(t, x.Relations)
	for _, r := range x.Relations {
		if r.Kind == parser.RelCall {
			assert.Equal(t, rel(parser.RelCall, "Close", "", 7), r)
		}
	}
	require.Len(t, y.Relations, 1)
	assert.True(t, strings.HasSuffix(y.Relations[0].TargetQualifiedName, "A.Close"), y.Relations[0].TargetQualifiedName)
}

// Only the package API counts as exported: not test files, not the methods
// and fields of unexported types.
func TestExportedIsPackageAPI(t *testing.T) {
	src := "package p\n\ntype Pub struct{ Field int }\n\nfunc (Pub) Method() {}\n\ntype hidden struct{ Field int }\n\nfunc (hidden) Method() {}\n\nfunc (h *hidden) Close() error { return nil }\n"
	syms := analyze(t, "/x", "/x/p/p.go", src)
	exported := func(q string) bool { s, ok := syms[q]; require.True(t, ok, q); return s.Exported }
	assert.True(t, exported("p.Pub"))
	assert.True(t, exported("p.Pub.Method"))
	assert.False(t, exported("p.hidden.Method"))
	assert.False(t, exported("p.hidden.Close"))

	tests := analyze(t, "/x", "/x/p/p_test.go", "package p\n\nfunc TestX(t *testing.T) {}\n\nfunc BenchmarkY(b *testing.B) {}\n\ntype Helper struct{}\n")
	for q, s := range tests {
		assert.False(t, s.Exported, q)
	}
}

// A call through a parameter or a local variable is not a call to a package
// function of that name; only the value uses of helper (fn := helper) name it.
func TestCallThroughLocalIsNotPackageFunc(t *testing.T) {
	src := `package p

func helper() {}

func Wrap(next func(), more ...func()) func() {
	fn := helper
	var cb, other = helper, helper
	for _, m := range more {
		m()
	}
	return func() { next(); fn(); cb(); other(); helper() }
}
`
	syms := analyze(t, "/x", "/x/p/p.go", src)
	var calls []string
	for _, r := range syms["p.Wrap"].Relations {
		if r.Kind == parser.RelCall {
			calls = append(calls, fmt.Sprintf("%s=%s:%d value=%v", r.TargetName, r.TargetQualifiedName, r.Line, r.Value))
		}
	}
	assert.Equal(t, []string{"helper=p.helper:6 value=true", "helper=p.helper:7 value=true", "helper=p.helper:7 value=true", "helper=p.helper:11 value=false"}, calls)
}

// An unqualified type name is either predeclared, a type parameter, or
// declared in the same package, in this file or another one.
func TestSamePackageTypeUses(t *testing.T) {
	root := t.TempDir()
	src := `package llm

type Adapter struct {
	provider Provider
	Base
}

func NewAdapter(p Provider) *Adapter { return &Adapter{provider: p} }

func (a *Adapter) Run() (Result, error) { return Result{}, nil }

func Map[T comparable](x T) T { return x }
`
	syms := analyze(t, root, filepath.Join(root, "llm", "adapter.go"), src)
	mod := "llm."
	assert.Equal(t, []parser.Relation{
		rel(parser.RelUsesType, "Provider", mod+"Provider", 4),
		rel(parser.RelExtends, "Base", mod+"Base", 5),
	}, syms["llm.Adapter"].Relations, "no relation to itself")
	assert.Equal(t, []parser.Relation{
		rel(parser.RelUsesType, "Provider", mod+"Provider", 8),
		rel(parser.RelUsesType, "Adapter", mod+"Adapter", 8),
		rel(parser.RelUsesType, "Adapter", mod+"Adapter", 8),
	}, syms["llm.NewAdapter"].Relations)
	assert.Equal(t, []parser.Relation{
		rel(parser.RelUsesType, "Result", mod+"Result", 10),
		rel(parser.RelUsesType, "Result", mod+"Result", 10),
	}, syms["llm.Adapter.Run"].Relations, "the receiver is the owner, not a use")
	assert.Empty(t, syms["llm.Map"].Relations, "type parameters and predeclared types")
}

// A blank var (Ginkgo's var _ = Describe(...), interface assertions) keeps
// its relations: they are the only record of those calls.
func TestBlankVarKeepsRelations(t *testing.T) {
	root := t.TempDir()
	src := `package tests

import "github.com/acme/app/pkg/llm"

var _ = Describe("registry", func() {
	llm.Register("x", nil)
})

var _ llm.Provider = (*fake)(nil)

var _ = 1
`
	syms, err := Analyzer{}.Analyze(parser.File{Path: filepath.Join(root, "tests", "r_test.go"), Root: root, Source: []byte(src), Language: "go"})
	require.NoError(t, err)
	var blanks []parser.Symbol
	for _, s := range syms {
		if s.Name == "_" {
			blanks = append(blanks, s)
		}
	}
	require.Len(t, blanks, 2, "the one without relations is dropped")
	assert.Contains(t, blanks[0].Relations, rel(parser.RelCall, "Register", "github.com/acme/app/pkg/llm.Register", 6))
	assert.Contains(t, blanks[1].Relations, rel(parser.RelUsesType, "Provider", "github.com/acme/app/pkg/llm.Provider", 9))
	assert.False(t, blanks[0].Exported)
}

// An import's assumed package name follows goimports: no .vN or /vN major
// suffix, no go- prefix (gopkg.in/yaml.v3 is yaml, .../go-isatty is isatty).
func TestImportAssumedName(t *testing.T) {
	root := t.TempDir()
	src := `package p

import (
	"gopkg.in/yaml.v3"
	"github.com/mattn/go-isatty"
	"github.com/acme/kit/v2"
)

func F() { yaml.Unmarshal(nil, nil); isatty.IsTerminal(0); kit.Run() }
`
	f := analyze(t, root, filepath.Join(root, "p.go"), src)["F"]
	got := map[string]string{}
	for _, r := range f.Relations {
		got[r.TargetName] = r.TargetQualifiedName
	}
	assert.Equal(t, "gopkg.in/yaml.v3.Unmarshal", got["Unmarshal"])
	assert.Equal(t, "github.com/mattn/go-isatty.IsTerminal", got["IsTerminal"])
	assert.Equal(t, "github.com/acme/kit/v2.Run", got["Run"])
}

// A call in a chain split over lines is on the line of its method name.
func TestChainedCallLine(t *testing.T) {
	root := t.TempDir()
	syms := analyze(t, root, filepath.Join(root, "g.go"), "package g\n\nfunc F(b *B) {\n\tb.\n\t\tWhere().\n\t\tFirst()\n}\n")
	lines := map[string]int{}
	for _, r := range syms["F"].Relations {
		if r.Kind == parser.RelCall {
			lines[r.TargetName] = r.Line
		}
	}
	assert.Equal(t, map[string]int{"Where": 5, "First": 6}, lines)
}

// An external test package (package p_test) is its own package: its
// declarations do not share qualified names with package p.
func TestExternalTestPackage(t *testing.T) {
	root := t.TempDir()
	in := analyze(t, root, filepath.Join(root, "p", "a_test.go"), "package p\n\nfunc helper() {}\n")
	ext := analyze(t, root, filepath.Join(root, "p", "b_test.go"), "package p_test\n\nfunc helper() {}\n\nfunc TestB() { helper() }\n")
	var inQN, extQN string
	for q := range in {
		inQN = q
	}
	for q, s := range ext {
		if s.Name == "helper" {
			extQN = q
		}
	}
	assert.True(t, strings.HasSuffix(extQN, "p_test.helper"), extQN)
	assert.NotEqual(t, inQN, extQN)
	for _, s := range ext {
		if s.Name == "TestB" {
			assert.Equal(t, extQN, s.Relations[0].TargetQualifiedName)
		}
	}
}

// A function or method named as a value (a handler passed to HandleFunc,
// assigned, put in a composite literal, returned) is a candidate usage,
// marked Value, with the target a call on the same expression would get,
// declared in this file or not. The index keeps it only when it links
// exactly to a function or method. What the file shows is no function (a
// local, a parameter, a var, const, type or struct field declared here) or
// what has no qualified target (an operand of unknown type) is not emitted.
func TestFuncValueIsCandidateUsage(t *testing.T) {
	src := `package api

import "net/http"

type cfg struct{ name, Model string }

const route = "/x"

var shared = 1

func (s *server) routes(mux *http.ServeMux, c cfg) {
	mux.HandleFunc("GET /a", s.firmaJSON)
	mux.Handle(route, nil)
	use(shared, c.name, c.Model, mux, http.MethodGet)
}

func main() {
	s := &server{}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /firma.json", s.firmaJSON)
	http.HandleFunc(route, handler)
	h := struct{ f http.HandlerFunc }{f: handler}
	g := s.firmaJSON
	var k = handler
	m := map[string]http.HandlerFunc{"a": handler}
	list := []func(){helper}
	x := unknown()
	use(x.Name, g, k, h, m, list, nil, true, len)
	s.cb = s.firmaJSON
}

func pick() http.HandlerFunc { return handler }

func local(v any, ch chan func()) {
	switch handler := v.(type) {
	default:
		use(handler)
	}
	select {
	case helper := <-ch:
		use(helper)
	}
}
`
	syms := analyze(t, "/x", "/x/api/main.go", src)
	values := func(qn string) []parser.Relation {
		var out []parser.Relation
		for _, r := range syms[qn].Relations {
			if r.Value {
				out = append(out, r)
			}
		}
		return out
	}
	val := func(name, tqn string, line int) parser.Relation {
		r := rel(parser.RelCall, name, tqn, line)
		r.Value = true
		return r
	}
	assert.Equal(t, []parser.Relation{
		val("firmaJSON", "api.server.firmaJSON", 12),
		val("MethodGet", "net/http.MethodGet", 14),
	}, values("api.server.routes"), "receiver method value; an import's member is left to the index")
	assert.Equal(t, []parser.Relation{
		val("firmaJSON", "api.server.firmaJSON", 20),
		val("handler", "api.handler", 21),
		val("handler", "api.handler", 22),
		val("firmaJSON", "api.server.firmaJSON", 23),
		val("handler", "api.handler", 24),
		val("handler", "api.handler", 25),
		val("helper", "api.helper", 26),
		val("firmaJSON", "api.server.firmaJSON", 29),
	}, values("api.main"), "argument, keyed and map literal values, :=, var, literal element, assignment")
	assert.Equal(t, []parser.Relation{val("handler", "api.handler", 32)}, values("api.pick"), "return value")
	assert.Empty(t, values("api.local"), "a type switch or select local is no package function")
}

// go.mod may separate "module" and the path with a tab and end the line with a comment.
func TestModuleDirectiveWithTabOrComment(t *testing.T) {
	for name, directive := range map[string]string{
		"inline_comment": "module example.com/repro // module comment\n",
		"tab_separator":  "module\texample.com/repro\n",
	} {
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte(directive), 0600); err != nil {
				t.Fatal(err)
			}
			syms, err := (Analyzer{}).Analyze(parser.File{Path: filepath.Join(root, "main.go"), Root: root, Source: []byte("package repro\nfunc F() {}\n")})
			if err != nil {
				t.Fatal(err)
			}
			if len(syms) != 1 {
				t.Fatalf("got %d symbols; want one function", len(syms))
			}
			if got := syms[0].QualifiedName; got != "example.com/repro.F" {
				t.Fatalf("qualified name = %q; want %q", got, "example.com/repro.F")
			}
		})
	}
}

// A go.mod that is a symlink to a file outside the workspace root is treated
// as absent: its module line never reaches a symbol. A go.mod symlinked to a
// file inside the root is still read.
func TestGoModSymlinkOutsideRootIgnored(t *testing.T) {
	for _, tc := range []struct {
		name, target, want string
	}{
		{"outside", filepath.Join("..", "outside", "go.mod"), ""},
		{"inside", filepath.Join("mods", "go.mod"), "example.com/inside"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			base := t.TempDir()
			ws := filepath.Join(base, "ws")
			require.NoError(t, os.MkdirAll(filepath.Join(ws, "mods"), 0o755))
			require.NoError(t, os.MkdirAll(filepath.Join(base, "outside"), 0o755))
			require.NoError(t, os.WriteFile(filepath.Join(base, "outside", "go.mod"), []byte("module secret.example/outside\n"), 0o600))
			require.NoError(t, os.WriteFile(filepath.Join(ws, "mods", "go.mod"), []byte("module example.com/inside\n"), 0o600))
			if err := os.Symlink(filepath.Join(ws, tc.target), filepath.Join(ws, "go.mod")); err != nil {
				t.Skipf("symlink not available: %v", err)
			}
			src := "package main\nfunc Run() {}\n"
			goFile := filepath.Join(ws, "main.go")
			require.NoError(t, os.WriteFile(goFile, []byte(src), 0o644))
			syms, err := Analyzer{}.Analyze(parser.File{Path: goFile, Root: ws, Source: []byte(src), Language: "go"})
			require.NoError(t, err)
			require.Len(t, syms, 1)
			assert.Equal(t, tc.want, syms[0].Module)
			assert.Equal(t, parser.Qualify(tc.want, "", "Run"), syms[0].QualifiedName)
		})
	}
}

// A call on a local or parameter whose single type the file declares targets
// that type's method, for same-package and imported types; a local set from
// a call or given two types keeps an empty target.
func TestCallOnDeclaredTypeOperand(t *testing.T) {
	root := t.TempDir()
	src := `package g

import "github.com/labstack/echo/v4"

func F(c echo.Context, r Repo) {
	c.JSON(200, nil)
	r.Find()
	var s Store
	s.Save()
	u := &User{}
	u.Save()
	v := User{}
	v.Load()
	x := open()
	x.Close()
	y := &User{}
	y = &Admin{}
	y.Save()
	go func(e echo.Context) { e.String(200, "") }(c)
}
`
	syms := analyze(t, root, filepath.Join(root, "g.go"), src)
	calls := map[string]string{}
	for _, r := range syms["F"].Relations {
		if r.Kind == parser.RelCall {
			calls[fmt.Sprintf("%s@%d", r.TargetName, r.Line)] = r.TargetQualifiedName
		}
	}
	assert.Equal(t, map[string]string{
		"JSON@6":    "github.com/labstack/echo/v4.Context.JSON",
		"Find@7":    "Repo.Find",
		"Save@9":    "Store.Save",
		"Save@11":   "User.Save",
		"Load@13":   "User.Load",
		"open@14":   "open",
		"Close@15":  "",
		"Save@18":   "",
		"String@19": "github.com/labstack/echo/v4.Context.String",
	}, calls)
}

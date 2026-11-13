package golang

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/doITmagic/rag-code-mcp/v2/parser"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Independent verification probes; kept next to the implementer's tests.

const probe = `package probe

import (
	"context"
	y "gopkg.in/yaml.v3"
	"gopkg.in/yaml.v3"
	. "math"
	"github.com/acme/app/v2"
	"github.com/acme/app/pkg/db"
)

type Repo struct {
	Base
	*db.Conn
	ctx context.Context
}

type Base struct{}

func (b Base) Ping() {}

type Number interface {
	~int | ~float64
}

func New[T any](ctx context.Context) *Repo {
	r := &Repo{}
	r.Ping()
	Repo{}.Ping()
	(*Repo).Ping(r)
	New[int](ctx)
	db.Open[int](ctx)
	_ = db.Conn(nil)
	_ = y.Marshal
	y.Marshal(r)
	yaml.Marshal(r)
	v2.Thing()
	app.Thing()
	Sqrt(2)
	_ = &db.Conn{}
	go func() { defer db.Close(); fire() }()
	return r
}

func (r *Repo) Save(ctx context.Context) error {
	r.Conn.Exec()
	r.Base.Ping()
	r.Ping()
	ctx.Done()
	return nil
}

func take(r *Repo) { r.Save(nil) }
`

func TestProbeResolution(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.com/m\n"), 0o644))
	require.NoError(t, os.MkdirAll(filepath.Join(root, "p"), 0o755))
	path := filepath.Join(root, "p", "probe.go")
	syms := analyze(t, root, path, probe)
	const mod = "example.com/m/p"

	for _, qn := range []string{mod + ".Repo", mod + ".Base", mod + ".Base.Ping", mod + ".Number", mod + ".New", mod + ".Repo.Save", mod + ".take"} {
		assert.Contains(t, syms, qn)
	}
	assert.Equal(t, parser.KindInterface, syms[mod+".Number"].Kind)
	assert.Equal(t, "Base", syms[mod+".Base.Ping"].Owner)
	assert.Equal(t, "Repo", syms[mod+".Repo.Save"].Owner)
	assert.False(t, syms[mod+".take"].Exported)

	assert.Equal(t, []parser.Relation{
		rel(parser.RelExtends, "Base", mod+".Base", 13),
		rel(parser.RelExtends, "Conn", "github.com/acme/app/pkg/db.Conn", 14),
		rel(parser.RelUsesType, "Context", "context.Context", 15),
	}, syms[mod+".Repo"].Relations)

	byLine := func(s parser.Symbol) map[int][]parser.Relation {
		m := map[int][]parser.Relation{}
		for _, r := range s.Relations {
			m[r.Line] = append(m[r.Line], r)
		}
		return m
	}
	n := byLine(syms[mod+".New"])
	t.Logf("New relations:\n%s", dump(syms[mod+".New"].Relations))
	assert.Contains(t, n[26], rel(parser.RelUsesType, "Context", "context.Context", 26))
	assert.Contains(t, n[27], rel(parser.RelUsesType, "Repo", mod+".Repo", 27))
	assert.Contains(t, n[28], rel(parser.RelCall, "Ping", mod+".Repo.Ping", 28), "r := &Repo{} declares its type")
	assert.Contains(t, n[29], rel(parser.RelCall, "Ping", mod+".Repo.Ping", 29))
	assert.Contains(t, n[30], rel(parser.RelCall, "Ping", mod+".Repo.Ping", 30))
	assert.Contains(t, n[31], rel(parser.RelCall, "New", mod+".New", 31), "generic call New[int]()")
	assert.Contains(t, n[32], rel(parser.RelCall, "Open", "github.com/acme/app/pkg/db.Open", 32), "generic call db.Open[int]()")
	// pkg.T(x): spec says RelUsesType via imports; without types it is ambiguous with a call.
	t.Logf("line 33 (db.Conn(nil)): %+v", n[33])
	for _, r := range n[33] {
		assert.Equal(t, "github.com/acme/app/pkg/db.Conn", r.TargetQualifiedName)
	}
	assert.Contains(t, n[35], rel(parser.RelCall, "Marshal", "gopkg.in/yaml.v3.Marshal", 35), "alias y")
	t.Logf("line 36 (yaml.Marshal, default name of gopkg.in/yaml.v3): %+v", n[36])
	t.Logf("line 37 (v2.Thing): %+v", n[37])
	t.Logf("line 38 (app.Thing): %+v", n[38])
	t.Logf("line 39 (dot-import Sqrt): %+v", n[39])
	assert.Contains(t, n[40], rel(parser.RelUsesType, "Conn", "github.com/acme/app/pkg/db.Conn", 40))
	assert.Contains(t, n[41], rel(parser.RelCall, "Close", "github.com/acme/app/pkg/db.Close", 41))
	assert.Contains(t, n[41], rel(parser.RelCall, "fire", mod+".fire", 41))

	s := byLine(syms[mod+".Repo.Save"])
	assert.Equal(t, []parser.Relation{rel(parser.RelCall, "Exec", "", 46)}, s[46])
	assert.Equal(t, []parser.Relation{rel(parser.RelCall, "Ping", "", 47)}, s[47])
	assert.Equal(t, []parser.Relation{rel(parser.RelCall, "Ping", mod+".Repo.Ping", 48)}, s[48])
	assert.Equal(t, []parser.Relation{rel(parser.RelCall, "Done", "context.Context.Done", 49)}, s[49], "ctx is declared context.Context")

	tk := byLine(syms[mod+".take"])
	assert.Contains(t, tk[53], rel(parser.RelCall, "Save", mod+".Repo.Save", 53), "a parameter declared *Repo is no guess")
	assert.Contains(t, tk[53], rel(parser.RelUsesType, "Repo", mod+".Repo", 53))
}

func dump(rs []parser.Relation) string {
	var b strings.Builder
	for _, r := range rs {
		b.WriteString(string(r.Kind) + " " + r.TargetName + " -> " + r.TargetQualifiedName + " @" + strconv.Itoa(r.Line) + "\n")
	}
	return b.String()
}

func TestProbeUncleanRoot(t *testing.T) {
	outer := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(outer, "go.mod"), []byte("module outer\n"), 0o644))
	root := filepath.Join(outer, "ws")
	require.NoError(t, os.MkdirAll(filepath.Join(root, "sub"), 0o755))
	syms := analyze(t, root+string(filepath.Separator), filepath.Join(root, "sub", "f.go"), "package sub\n\nfunc F() {}\n")
	keys := make([]string, 0, len(syms))
	for k := range syms {
		keys = append(keys, k)
	}
	assert.Contains(t, syms, "sub.F", "trailing slash on Root must not let the lookup climb above it: %v", keys)
}

func TestProbeRobust(t *testing.T) {
	bin := make([]byte, 4096)
	for i := range bin {
		bin[i] = byte(i*7 + 13)
	}
	src, err := os.ReadFile(filepath.Join("..", "parser.go"))
	require.NoError(t, err)
	tests := []struct {
		name string
		src  []byte
	}{
		{"one byte", []byte("x")},
		{"nul", []byte{0}},
		{"binary", bin},
		{"truncated", src[:len(src)/2]},
		{"missing type", []byte("package p\ntype Foo\nfunc OK() {}\n")},
		{"missing func field", []byte("package p\nfunc F() { (); a.(); .b() }\n")},
		{"iface no name", []byte("package p\ntype I interface { (int) }\n")},
		{"embedded only", []byte("package p\ntype S struct { pkg.T; *U; []int }\n")},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			done := make(chan struct{})
			go func() {
				defer close(done)
				assert.NotPanics(t, func() {
					_, err := Analyzer{}.Analyze(parser.File{Path: "/x/p.go", Root: "/x", Source: tc.src, Language: "go"})
					assert.NoError(t, err)
				})
			}()
			select {
			case <-done:
			case <-time.After(5 * time.Second):
				t.Fatal("hang")
			}
		})
	}
}

// ---- second verification round ----

const probe2 = `package p2

import (
	"fmt"
	"github.com/acme/app/pkg/db"
)

type (
	// Stack doc
	Stack[T any] struct {
		items []T
		Logger
	}
	Logger interface {
		fmt.Stringer
		Base
		Log(msg string)
	}
	Base interface{ Ping() }
	Level int
)

// Push doc
func (s *Stack[T]) Push(
	v T,
) error {
	s.items = append(s.items, v)
	s.Log("x")
	db.Conn{}.Exec()
	(&db.Conn{}).Exec()
	_ = Level(1)
	_ = new(Stack[T])
	return nil
}

var handler = func() { fmt.Println(helper()) }

func Ärger() {}

func ärger() {}

// detached comment

func Detached() {}

const (
	A Level = iota
	B
)
`

func TestProbeRound2(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.com/m\n"), 0o644))
	// nested module inside the root wins for files under it
	require.NoError(t, os.MkdirAll(filepath.Join(root, "sub", "x"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(root, "sub", "go.mod"), []byte("module nested.io/n\n"), 0o644))
	syms := analyze(t, root, filepath.Join(root, "sub", "x", "f.go"), probe2)
	const mod = "nested.io/n/x"

	keys := make([]string, 0, len(syms))
	for k := range syms {
		keys = append(keys, k)
	}
	t.Logf("symbols: %v", keys)

	st := syms[mod+".Stack"]
	assert.Equal(t, parser.KindClass, st.Kind)
	assert.Equal(t, 10, st.StartLine)
	assert.Equal(t, 13, st.EndLine)
	assert.Equal(t, "Stack doc", st.Doc)
	assert.Equal(t, "Stack[T any] struct {\n\t\titems []T\n\t\tLogger\n\t}", st.Content)
	assert.Equal(t, []parser.Relation{rel(parser.RelExtends, "Logger", mod+".Logger", 12)}, st.Relations)

	lg := syms[mod+".Logger"]
	assert.Equal(t, parser.KindInterface, lg.Kind)
	assert.Equal(t, "Log", lg.Metadata["methods"])
	assert.Equal(t, []parser.Relation{
		rel(parser.RelExtends, "Stringer", "fmt.Stringer", 15),
		rel(parser.RelExtends, "Base", mod+".Base", 16),
	}, lg.Relations)
	assert.Equal(t, parser.KindInterface, syms[mod+".Base"].Kind)
	assert.Equal(t, "Ping", syms[mod+".Base"].Metadata["methods"])
	assert.Equal(t, parser.KindType, syms[mod+".Level"].Kind)

	push := syms[mod+".Stack.Push"]
	assert.Equal(t, parser.KindMethod, push.Kind)
	assert.Equal(t, "Stack", push.Owner)
	assert.Equal(t, "Push doc", push.Doc)
	assert.Equal(t, "func (s *Stack[T]) Push(\n\tv T,\n) error", push.Signature)
	assert.Equal(t, 24, push.StartLine)
	assert.Equal(t, 34, push.EndLine)
	t.Logf("Push relations:\n%s", dump(push.Relations))
	assert.Equal(t, []parser.Relation{
		rel(parser.RelCall, "Log", mod+".Stack.Log", 28),
		rel(parser.RelCall, "Exec", "github.com/acme/app/pkg/db.Conn.Exec", 29),
		rel(parser.RelUsesType, "Conn", "github.com/acme/app/pkg/db.Conn", 29),
		rel(parser.RelCall, "Exec", "github.com/acme/app/pkg/db.Conn.Exec", 30),
		rel(parser.RelUsesType, "Conn", "github.com/acme/app/pkg/db.Conn", 30),
		rel(parser.RelUsesType, "Level", mod+".Level", 31),
		rel(parser.RelUsesType, "Stack", mod+".Stack", 32), // new(Stack[T]); T is the receiver's type parameter
	}, push.Relations)

	h := syms[mod+".handler"]
	assert.Equal(t, parser.KindVariable, h.Kind)
	assert.Equal(t, []parser.Relation{
		rel(parser.RelCall, "Println", "fmt.Println", 36),
		rel(parser.RelCall, "helper", mod+".helper", 36),
	}, h.Relations)

	assert.True(t, syms[mod+".Ärger"].Exported)
	assert.False(t, syms[mod+".ärger"].Exported)
	assert.Equal(t, "", syms[mod+".Detached"].Doc, "comment separated by a blank line is not a doc")

	assert.Equal(t, parser.KindConstant, syms[mod+".A"].Kind)
	assert.Equal(t, parser.KindConstant, syms[mod+".B"].Kind)
	assert.Equal(t, 48, syms[mod+".B"].StartLine)
}

func TestProbeBigAndDeep(t *testing.T) {
	var b strings.Builder
	b.WriteString("package big\n\n")
	for i := 0; i < 1500; i++ {
		b.WriteString("func F" + strconv.Itoa(i) + "(a int) int { return G" + strconv.Itoa(i) + "(a) + len(x.y.z()) }\n")
	}
	deep := "package deep\n\nfunc D() int { return " + strings.Repeat("(", 2000) + "f()" + strings.Repeat(")", 2000) + " }\n"
	for name, src := range map[string]string{"big": b.String(), "deep": deep} {
		t.Run(name, func(t *testing.T) {
			done := make(chan int)
			go func() {
				syms, err := Analyzer{}.Analyze(parser.File{Path: "/x/p.go", Root: "/x", Source: []byte(src), Language: "go"})
				assert.NoError(t, err) // deep is valid code: parsed, only a broken file that deep is refused
				done <- len(syms)
			}()
			select {
			case n := <-done:
				t.Logf("%s: %d symbols", name, n)
				if name == "big" {
					assert.Equal(t, 1500, n)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("hang")
			}
		})
	}
}

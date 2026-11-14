package index

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/doITmagic/rag-code-mcp/v2/parser"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const goRepoIface = `package api

type Repo interface {
	Find(id int) string
	OnlyIface()
}

type Reader interface{ Read() }

type RW interface {
	Reader
	Write()
}
`

const goRepoImpl = `package api

type sqlRepo struct{}

func (sqlRepo) Find(id int) string { return "" }

func newRepo() *sqlRepo { return &sqlRepo{} }
`

// goRelsOf maps each call (by target name) of fn in file to its resolution and target.
func goRelsOf(t *testing.T, e *env, file, fn string) map[string][2]string {
	t.Helper()
	out := map[string][2]string{}
	for _, r := range e.symbols(t, file)[fn].Relations {
		if r.Kind != parser.RelCall {
			continue
		}
		target := ""
		if r.TargetID != "" {
			target = e.byID(t, r.TargetID).QualifiedName
		}
		out[r.TargetName] = [2]string{string(r.Resolution), target}
	}
	return out
}

// A call on a parameter typed with a workspace interface (r Repo; r.Find())
// links exactly to the interface's method, and one of an embedded interface
// (rw RW; rw.Read()) to the embedded interface's method.
func TestGoInterfaceMethodCallExact(t *testing.T) {
	parser.Register(realGo)
	t.Cleanup(func() { parser.Register(lineAnalyzer{"go", ".go"}) })
	e := newEnv(t)
	e.write(t, "go.mod", "module example.com/app\n\ngo 1.25\n")
	e.write(t, "api/repo.go", goRepoIface)
	e.write(t, "api/impl.go", goRepoImpl)
	use := e.write(t, "api/use.go", "package api\n\nfunc UseParam(r Repo, rw RW) {\n\tr.Find(1)\n\trw.Read()\n}\n")
	_, err := e.run(t, nil)
	require.NoError(t, err)
	assert.Equal(t, map[string][2]string{
		"Find": {string(parser.ResolutionExact), "example.com/app/api.Repo.Find"},
		"Read": {string(parser.ResolutionExact), "example.com/app/api.Reader.Read"},
	}, goRelsOf(t, e, use, "UseParam"))
}

// A call matched by name alone never takes an interface method: on an
// operand of unknown type x.Find() stays unique to the one implementation,
// and a name only an interface declares stays unresolved; also in a files
// run, where the interface's method comes from the store.
func TestGoNameOnlySkipsInterfaceMethods(t *testing.T) {
	parser.Register(realGo)
	t.Cleanup(func() { parser.Register(lineAnalyzer{"go", ".go"}) })
	e := newEnv(t)
	e.write(t, "go.mod", "module example.com/app\n\ngo 1.25\n")
	e.write(t, "api/repo.go", goRepoIface)
	e.write(t, "api/impl.go", goRepoImpl)
	src := "package api\n\nfunc UseLocal() {\n\tx := newRepo()\n\tx.Find(1)\n\tx.OnlyIface()\n}\n"
	use := e.write(t, "api/use.go", src)
	_, err := e.run(t, nil)
	require.NoError(t, err)
	want := map[string][2]string{
		"newRepo":   {string(parser.ResolutionExact), "example.com/app/api.newRepo"},
		"Find":      {string(parser.ResolutionUnique), "example.com/app/api.sqlRepo.Find"},
		"OnlyIface": {"", ""},
	}
	assert.Equal(t, want, goRelsOf(t, e, use, "UseLocal"), "full run")

	e.write(t, "api/use.go", src+"\n")
	_, err = e.run(t, nil)
	require.NoError(t, err)
	assert.Equal(t, want, goRelsOf(t, e, use, "UseLocal"), "files run, the interface from the store")
}

// A call through a field of an external type (s.db.Exec() with db *sql.DB)
// is no usage of a workspace method that only shares the name, and one
// through a field typed with a workspace interface embedding an external one
// is exact for the interface's own method, unresolved for the external one's.
func TestGoFieldCallTargetsFieldType(t *testing.T) {
	parser.Register(realGo)
	t.Cleanup(func() { parser.Register(lineAnalyzer{"go", ".go"}) })
	e := newEnv(t)
	e.write(t, "go.mod", "module example.com/app\n\ngo 1.25\n")
	e.write(t, "api/local.go", "package api\n\ntype local struct{}\n\nfunc (local) Exec(q string) {}\n\nfunc (local) Close() error { return nil }\n\nfunc (local) Save() {}\n")
	svc := e.write(t, "api/svc.go", `package api

import (
	"database/sql"
	"io"
)

type Store interface {
	io.Closer
	Save()
}

type Svc struct {
	db *sql.DB
	st Store
}

func (s *Svc) Run() {
	s.db.Exec("")
	s.st.Save()
	_ = s.st.Close()
}
`)
	_, err := e.run(t, nil)
	require.NoError(t, err)
	assert.Equal(t, map[string][2]string{
		"Exec":  {"", ""},
		"Save":  {string(parser.ResolutionExact), "example.com/app/api.Store.Save"},
		"Close": {"", ""},
	}, goRelsOf(t, e, svc, "Run"))
}

// Two interface methods of the run sharing the name do not make a name-only
// call ambiguous, since it never binds to them: a files run of their file
// still asks the store, where the one implementation lies in an unchanged file.
func TestGoNameOnlyAsksStorePastInterfaceMethods(t *testing.T) {
	parser.Register(realGo)
	t.Cleanup(func() { parser.Register(lineAnalyzer{"go", ".go"}) })
	e := newEnv(t)
	e.write(t, "go.mod", "module example.com/app\n\ngo 1.25\n")
	e.write(t, "api/impl.go", goRepoImpl)
	src := "package api\n\ntype A interface{ Find(id int) string }\n\ntype B interface{ Find(id int) string }\n\nfunc UseLocal() {\n\tx := newRepo()\n\tx.Find(1)\n}\n"
	use := e.write(t, "api/use.go", src)
	_, err := e.run(t, nil)
	require.NoError(t, err)
	want := map[string][2]string{
		"newRepo": {string(parser.ResolutionExact), "example.com/app/api.newRepo"},
		"Find":    {string(parser.ResolutionUnique), "example.com/app/api.sqlRepo.Find"},
	}
	assert.Equal(t, want, goRelsOf(t, e, use, "UseLocal"), "full run")

	e.write(t, "api/use.go", src+"\n")
	_, err = e.run(t, nil)
	require.NoError(t, err)
	assert.Equal(t, want, goRelsOf(t, e, use, "UseLocal"), "files run, the implementation from the store")
}

// Many interface methods of one name in the store do not hide the one
// implementation from a name-only call: the lookup's first page of 20 holds
// only candidates it drops (or stale points of the run's own files), so it
// reads the rest. A files run of the caller and one of the interfaces' file
// both keep the link a full run makes.
func TestGoNameOnlyReadsPastAPageOfInterfaceMethods(t *testing.T) {
	parser.Register(realGo)
	t.Cleanup(func() { parser.Register(lineAnalyzer{"go", ".go"}) })
	e := newEnv(t)
	e.write(t, "go.mod", "module example.com/app\n\ngo 1.25\n")
	var b strings.Builder
	b.WriteString("package api\n")
	for i := range 400 {
		fmt.Fprintf(&b, "\ntype I%d interface{ Find(id int) string }\n", i)
	}
	e.write(t, "api/ifaces.go", b.String())
	e.write(t, "api/impl.go", goRepoImpl)
	src := "package api\n\nfunc UseLocal() {\n\tx := newRepo()\n\tx.Find(1)\n}\n"
	use := e.write(t, "api/use.go", src)
	_, err := e.run(t, nil)
	require.NoError(t, err)
	want := [2]string{string(parser.ResolutionUnique), "example.com/app/api.sqlRepo.Find"}
	assert.Equal(t, want, goRelsOf(t, e, use, "UseLocal")["Find"], "full run")

	e.write(t, "api/use.go", src+"\n")
	_, err = e.run(t, nil)
	require.NoError(t, err)
	assert.Equal(t, want, goRelsOf(t, e, use, "UseLocal")["Find"], "files run of the caller")

	e.write(t, "api/ifaces.go", b.String()+"\nfunc UseToo() {\n\tx := newRepo()\n\tx.Find(2)\n}\n")
	_, err = e.run(t, nil)
	require.NoError(t, err)
	assert.Equal(t, want, goRelsOf(t, e, filepath.Join(filepath.Dir(use), "ifaces.go"), "UseToo")["Find"], "files run of the interfaces' file")
}

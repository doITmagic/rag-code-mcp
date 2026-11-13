package tools

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	_ "github.com/doITmagic/rag-code-mcp/v2/parser/golang"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const goMod = "module example.com/app\n\ngo 1.25\n"

// hierarchyOf maps the children of the root of a depth 1 rag_call_hierarchy
// answer in direction (incoming or outgoing) to their via markers.
func hierarchyOf(t *testing.T, v *env, symbol, direction string) map[string]string {
	t.Helper()
	var out struct {
		Incoming *Node `json:"incoming"`
		Outgoing *Node `json:"outgoing"`
	}
	r, _ := v.call(t, "rag_call_hierarchy", map[string]any{"workspace_root": v.root, "symbol": symbol, "direction": direction, "depth": 1})
	require.Equal(t, "ok", r.Status, r.Message)
	data(t, r, &out)
	root := out.Incoming
	if direction == "outgoing" {
		root = out.Outgoing
	}
	require.NotNil(t, root, r.Message)
	got := map[string]string{}
	for _, c := range root.Children {
		got[c.QualifiedName] = c.Via
	}
	return got
}

// A call through a parameter typed with a workspace interface is also a
// usage of the method of every workspace type whose method set covers the
// interface's (names only), marked via; the interface's own method keeps its
// usages, and a type that lacks one of the interface's methods gets none.
func TestUsagesOfGoMethodThroughInterface(t *testing.T) {
	v := newEnv(t, false, nil)
	impl := "api/impl.go"
	v.addFiles(t, map[string]string{
		"go.mod":      goMod,
		"api/repo.go": "package api\n\ntype Repo interface {\n\tFind(id int) string\n\tOnlyIface()\n}\n",
		impl: "package api\n\ntype sqlRepo struct{}\n\nfunc (sqlRepo) Find(id int) string { return \"\" }\n\nfunc (*sqlRepo) OnlyIface() {}\n\n" +
			"type Unrelated struct{}\n\nfunc (Unrelated) Find(id int) string { return \"\" }\n",
		"api/use.go": "package api\n\nfunc UseParam(r Repo) { r.Find(1) }\n",
		"api/two.go": "package api\n\ntype Store interface{ Save() }\n\ntype memStore struct{}\n\nfunc (memStore) Save() {}\n\n" +
			"type diskStore struct{}\n\nfunc (*diskStore) Save() {}\n\nfunc Run(st Store) { st.Save() }\n",
	})
	const mod = "example.com/app/api"
	check := func() {
		t.Helper()
		want := map[string]string{mod + ".UseParam": mod + ".Repo"}
		assert.Equal(t, want, viaOf(usagesOf(t, v, "api.sqlRepo.Find")))
		assert.Equal(t, map[string]string{mod + ".UseParam": ""}, viaOf(usagesOf(t, v, "api.Repo.Find")))
		assert.Empty(t, usagesOf(t, v, "api.Unrelated.Find"), "Unrelated has no OnlyIface: it is no Repo")
		assert.Equal(t, want, hierarchyOf(t, v, "api.sqlRepo.Find", "incoming"))

		run := map[string]string{mod + ".Run": mod + ".Store"}
		for _, s := range []string{"api.memStore.Save", "api.diskStore.Save"} {
			r, _ := v.call(t, "rag_find_usages", map[string]any{"workspace_root": v.root, "symbol": s})
			require.Equal(t, "ok", r.Status, r.Message)
			var out struct {
				Files     []FileUsages `json:"files"`
				Ambiguous int          `json:"ambiguous_by_name"`
			}
			data(t, r, &out)
			assert.Equal(t, run, viaOf(out.Files), s)
			assert.Zero(t, out.Ambiguous, r.Message)
			assert.Equal(t, run, hierarchyOf(t, v, s, "incoming"), s)
		}
		assert.Equal(t, map[string]string{mod + ".Run": ""}, viaOf(usagesOf(t, v, "api.Store.Save")))
		assert.Equal(t, map[string]string{mod + ".Store.Save": ""}, hierarchyOf(t, v, "api.Run", "outgoing"))
	}
	check()

	// A files run of the implementation alone gives the same answer.
	w, err := v.e.Workspace(v.root)
	require.NoError(t, err)
	p := filepath.Join(v.root, impl)
	src, err := os.ReadFile(p)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(p, append(src, '\n'), 0o644))
	require.NoError(t, os.Chtimes(p, time.Now(), time.Now().Add(time.Second)))
	w.IndexFiles([]string{p}, nil)
	require.Eventually(t, func() bool { s := w.Status(); return !s.Running && !s.Queued }, 10*time.Second, 20*time.Millisecond)
	check()
}

// A method promoted from an embedded struct counts in the embedding type's
// method set (barou's agent tasks): SessionTask implements Task with the
// Name of BaseTask, so the interface's callers are usages of both methods.
func TestGoPromotedMethodImplementsInterface(t *testing.T) {
	v := newEnv(t, false, nil)
	v.addFiles(t, map[string]string{
		"go.mod": goMod,
		"agent/task.go": "package agent\n\ntype Task interface {\n\tName() string\n\tExecute()\n}\n\n" +
			"type BaseTask struct{}\n\nfunc (BaseTask) Name() string { return \"\" }\n\n" +
			"type SessionTask struct {\n\tBaseTask\n}\n\nfunc (*SessionTask) Execute() {}\n\n" +
			"func Run(t Task) {\n\t_ = t.Name()\n\tt.Execute()\n}\n",
	})
	want := map[string]string{"example.com/app/agent.Run": "example.com/app/agent.Task"}
	assert.Equal(t, want, viaOf(usagesOf(t, v, "agent.SessionTask.Execute")))
	assert.Equal(t, want, viaOf(usagesOf(t, v, "agent.BaseTask.Name")))
}

// S embeds Base and declares its own Find: a call through the interface
// reaches S.Find, never Base.Find. A call matched by name alone (an operand
// of unknown type) stays by_name, without via.
func TestGoOverrideHasNoInterfaceUsage(t *testing.T) {
	v := newEnv(t, false, nil)
	v.addFiles(t, map[string]string{
		"go.mod": goMod,
		"app/all.go": "package app\n\ntype Finder interface {\n\tFind()\n\tOther()\n}\n\n" +
			"type Base struct{}\n\nfunc (Base) Find() {}\n\ntype S struct{ Base }\n\nfunc (S) Find() {}\n\nfunc (S) Other() {}\n\n" +
			"func run(f Finder) { f.Find() }\n\nfunc mk() S { return S{} }\n\nfunc bare() {\n\tx := mk()\n\tx.Other()\n}\n",
	})
	assert.Empty(t, usagesOf(t, v, "app.Base.Find"), "S overrides Base.Find")
	assert.Equal(t, map[string]string{"example.com/app/app.run": "example.com/app/app.Finder"}, viaOf(usagesOf(t, v, "app.S.Find")))
	files := usagesOf(t, v, "app.S.Other")
	require.Len(t, files, 1)
	require.Len(t, files[0].Usages, 1)
	assert.Equal(t, "example.com/app/app.bare", files[0].Usages[0].Caller)
	assert.True(t, files[0].Usages[0].ByName)
	assert.Empty(t, files[0].Usages[0].Via)
}

// gotify's shape: the handler ApplicationAPI.CreateApplication calls
// a.DB.CreateApplication() through a field typed with the workspace
// interface ApplicationDatabase, and a test fake shares the name; the
// implementation lists the call via the interface instead of answering 0
// with unresolved relations.
func TestUsagesOfGoFieldThroughInterface(t *testing.T) {
	v := newEnv(t, false, nil)
	v.addFiles(t, map[string]string{
		"go.mod": goMod,
		"api/application.go": "package api\n\ntype ApplicationDatabase interface {\n\tCreateApplication(name string) error\n}\n\n" +
			"type ApplicationAPI struct {\n\tDB ApplicationDatabase\n}\n\nfunc (a *ApplicationAPI) CreateApplication() {\n\t_ = a.DB.CreateApplication(\"x\")\n}\n",
		"database/application.go": "package database\n\ntype GormDatabase struct{}\n\nfunc (d *GormDatabase) CreateApplication(name string) error { return nil }\n",
		"test/fake.go":            "package test\n\ntype failingDB struct{}\n\nfunc (failingDB) CreateApplication(name string) error { return nil }\n",
	})
	r, _ := v.call(t, "rag_find_usages", map[string]any{"workspace_root": v.root, "symbol": "database.GormDatabase.CreateApplication"})
	require.Equal(t, "ok", r.Status, r.Message)
	var out struct {
		Files     []FileUsages `json:"files"`
		Ambiguous int          `json:"ambiguous_by_name"`
	}
	data(t, r, &out)
	assert.Equal(t, map[string]string{"example.com/app/api.ApplicationAPI.CreateApplication": "example.com/app/api.ApplicationDatabase"}, viaOf(out.Files), r.Message)
	assert.Zero(t, out.Ambiguous, r.Message)
}

// rag_list_package_exports leaves a Go interface's methods to the interface,
// whose content lists them: as rows of their own they filled the 300 cap of a
// large package (wakapi services) and pushed classes and functions out.
func TestExportsLeaveOutGoInterfaceMethods(t *testing.T) {
	v := newEnv(t, false, nil)
	v.addFiles(t, map[string]string{
		"go.mod":   goMod,
		"api/a.go": "package api\n\ntype Repo interface {\n\tFind(id int) string\n\tSave()\n}\n\ntype Sql struct{}\n\nfunc (Sql) Find(id int) string { return \"\" }\n",
	})
	var out struct {
		Exports map[string][]Export `json:"exports"`
	}
	r, _ := v.call(t, "rag_list_package_exports", map[string]any{"workspace_root": v.root, "package": "example.com/app/api"})
	require.Equal(t, "ok", r.Status, r.Message)
	data(t, r, &out)
	var methods []string
	for _, e := range out.Exports["method"] {
		methods = append(methods, e.QualifiedName)
	}
	assert.Equal(t, []string{"example.com/app/api.Sql.Find"}, methods)
	assert.Len(t, out.Exports["interface"], 1)
	assert.Contains(t, r.Message, "3 exported symbols")
}

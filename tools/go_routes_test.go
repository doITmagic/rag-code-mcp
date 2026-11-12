package tools

import (
	"testing"

	_ "github.com/doITmagic/rag-code-mcp/v2/parser/golang"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// An Echo route registration is a route symbol: rag_search with kinds=route
// and language=go finds it, and the handler's usages list the route.
func TestGoEchoRouteSearchAndUsage(t *testing.T) {
	v := newEnv(t, false, nil)
	v.addFiles(t, map[string]string{
		"go.mod": "module example.com/app\n\ngo 1.25\n",
		"controllers/users.go": `package controllers

import "github.com/labstack/echo/v4"

type Users struct{}

func (u *Users) Index(c echo.Context) error { return nil }
`,
		"routes/routes.go": `package routes

import (
	"example.com/app/controllers"
	"github.com/labstack/echo/v4"
)

func Load(e *echo.Echo) {
	users := &controllers.Users{}
	admin := e.Group("/admin")
	admin.GET("/users", users.Index)
}
`,
	})

	var found struct {
		Results []SearchHit `json:"results"`
	}
	r, _ := v.call(t, "rag_search", map[string]any{"workspace_root": v.root, "query": "/admin/users", "kinds": []string{"route"}, "language": "go"})
	require.Equal(t, "ok", r.Status, r.Message)
	data(t, r, &found)
	require.NotEmpty(t, found.Results, r.Message)
	assert.Equal(t, "example.com/app/routes.Load./admin/users", found.Results[0].QualifiedName)
	assert.Equal(t, "route", found.Results[0].Kind)

	var used struct {
		Files []FileUsages `json:"files"`
	}
	r, _ = v.call(t, "rag_find_usages", map[string]any{"workspace_root": v.root, "symbol": "example.com/app/controllers.Users.Index"})
	require.Equal(t, "ok", r.Status, r.Message)
	data(t, r, &used)
	var callers []string
	for _, f := range used.Files {
		for _, u := range f.Usages {
			callers = append(callers, u.Caller)
		}
	}
	assert.Contains(t, callers, "example.com/app/routes.Load./admin/users", r.Message)
}

// A route's path is part of its qualified name, not a name segment: the
// function registering "/" (example.com/app/routes.Load./) is still the one
// symbol named routes.Load, a function users is not confused with the route
// /users, and the routes stay findable by their path.
func TestGoRoutePathIsNotANameSegment(t *testing.T) {
	v := newEnv(t, false, nil)
	v.addFiles(t, map[string]string{
		"go.mod": "module example.com/app\n\ngo 1.25\n",
		"main.go": `package main

import (
	"net/http"

	"example.com/app/routes"
)

func main() {
	mux := http.NewServeMux()
	routes.Load(mux)
}
`,
		"routes/routes.go": `package routes

import "net/http"

func Load(mux *http.ServeMux) {
	mux.HandleFunc("/", home)
	mux.HandleFunc("/users", users)
}

func home(w http.ResponseWriter, r *http.Request) {}

func users(w http.ResponseWriter, r *http.Request) {}
`,
	})

	var used struct {
		Files []FileUsages `json:"files"`
		Total int          `json:"total"`
	}
	r, _ := v.call(t, "rag_find_usages", map[string]any{"workspace_root": v.root, "symbol": "example.com/app/routes.Load"})
	require.Equal(t, "ok", r.Status, r.Message)
	data(t, r, &used)
	require.Equal(t, 1, used.Total, r.Message)
	assert.Equal(t, "example.com/app.main", used.Files[0].Usages[0].Caller)

	r, _ = v.call(t, "rag_call_hierarchy", map[string]any{"workspace_root": v.root, "symbol": "Load", "file_path": "routes/routes.go"})
	require.Equal(t, "ok", r.Status, r.Message)

	used.Files, used.Total = nil, 0
	r, _ = v.call(t, "rag_find_usages", map[string]any{"workspace_root": v.root, "symbol": "users"})
	require.Equal(t, "ok", r.Status, r.Message)
	data(t, r, &used)
	require.Equal(t, 2, used.Total, r.Message) // Load names it, and so does the route /users

	var found struct {
		Results []SearchHit `json:"results"`
	}
	r, _ = v.call(t, "rag_search", map[string]any{"workspace_root": v.root, "query": "example.com/app/routes.Load"})
	require.Equal(t, "ok", r.Status, r.Message)
	data(t, r, &found)
	var exact []string
	for _, h := range found.Results {
		if h.Match == "exact" {
			exact = append(exact, h.QualifiedName)
		}
	}
	assert.Equal(t, []string{"example.com/app/routes.Load"}, exact)

	found.Results = nil
	r, _ = v.call(t, "rag_search", map[string]any{"workspace_root": v.root, "query": "/users", "kinds": []string{"route"}})
	require.Equal(t, "ok", r.Status, r.Message)
	data(t, r, &found)
	require.Len(t, found.Results, 1, r.Message)
	assert.Equal(t, "example.com/app/routes.Load./users", found.Results[0].QualifiedName)
	assert.Equal(t, "exact", found.Results[0].Match)
}

// GET /users and DELETE /users registered in one function share a qualified
// name and a file, but they are two routes, never twins: both are exports
// and search hits, each keeps only its own handler, and a lookup by their
// qualified name lists both without advising a name or file that cannot
// tell them apart.
func TestGoRoutesOfOnePathAreNotTwins(t *testing.T) {
	v := newEnv(t, false, nil)
	v.addFiles(t, map[string]string{
		"go.mod": "module acme.test/app\n\ngo 1.26\n",
		"web/routes.go": `package web

import "github.com/gin-gonic/gin"

func ListUsers(c *gin.Context)  {}
func DeleteUser(c *gin.Context) {}

func Register(r *gin.Engine) {
	r.GET("/users", ListUsers)
	r.DELETE("/users", DeleteUser)
}
`,
	})
	const qn = "acme.test/app/web.Register./users"

	r, _ := v.call(t, "rag_list_package_exports", map[string]any{"workspace_root": v.root, "package": "acme.test/app/web", "kinds": []string{"route"}})
	require.Equal(t, "ok", r.Status, r.Message)
	var exp struct{ Exports map[string][]Export }
	data(t, r, &exp)
	var sigs []string
	for _, e := range exp.Exports["route"] {
		sigs = append(sigs, e.Signature)
	}
	assert.ElementsMatch(t, []string{`r.GET("/users", ListUsers)`, `r.DELETE("/users", DeleteUser)`}, sigs)

	r, _ = v.call(t, "rag_search", map[string]any{"workspace_root": v.root, "query": qn, "kinds": []string{"route"}})
	require.Equal(t, "ok", r.Status, r.Message)
	var found struct {
		Results []SearchHit `json:"results"`
	}
	data(t, r, &found)
	assert.Len(t, found.Results, 2, r.Message)

	r, _ = v.call(t, "rag_call_hierarchy", map[string]any{"workspace_root": v.root, "symbol": qn, "direction": "outgoing"})
	require.Equal(t, "ambiguous", r.Status, r.Message)
	assert.NotContains(t, r.Message, "file_path")
	var amb struct{ Candidates []Candidate }
	data(t, r, &amb)
	assert.Len(t, amb.Candidates, 2)

	r, _ = v.call(t, "rag_call_hierarchy", map[string]any{"workspace_root": v.root, "symbol": "acme.test/app/web.ListUsers", "direction": "incoming"})
	require.Equal(t, "ok", r.Status, r.Message)
	var tree struct{ Text string }
	data(t, r, &tree)
	assert.Contains(t, tree.Text, qn+" (")
	assert.Contains(t, tree.Text, "routes.go:9)", "the GET route on line 9 calls ListUsers")
	assert.NotContains(t, tree.Text, "routes.go:10)", "the DELETE route on line 10 does not call ListUsers")
}

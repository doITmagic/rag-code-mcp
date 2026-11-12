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

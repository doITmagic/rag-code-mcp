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

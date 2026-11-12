package golang

import (
	"path/filepath"
	"testing"

	"github.com/doITmagic/rag-code-mcp/v2/parser"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// routeList analyzes src and lists its routes as
// "framework METHOD qualified-name -> handler target" in source order.
func routeList(t *testing.T, name, src string) []string {
	t.Helper()
	root := t.TempDir()
	syms, err := Analyzer{}.Analyze(parser.File{Path: filepath.Join(root, name), Root: root, Source: []byte(src), Language: "go"})
	require.NoError(t, err)
	var out []string
	for _, s := range syms {
		if s.Kind != parser.KindRoute {
			continue
		}
		line := s.Metadata["framework"] + " " + s.Metadata["method"] + " " + s.QualifiedName + " ->"
		for _, r := range s.Relations {
			line += " " + r.TargetQualifiedName
			assert.True(t, r.Value)
			assert.Equal(t, parser.RelCall, r.Kind)
		}
		out = append(out, line)
	}
	return out
}

func TestEchoRoutes(t *testing.T) {
	src := `package web

import (
	"net/http"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/acme/app/handlers"
)

type Users struct{}

func (u *Users) Index(c echo.Context) error { return nil }

func health(c echo.Context) error { return nil }

type server struct{ api *echo.Group }

func Load(e *echo.Echo, s *server, h *Users) {
	users := &Users{}
	e.GET("/health", health)
	e.POST("/login", handlers.Login, mw())
	e.GET("/inline", func(c echo.Context) error { return c.NoContent(http.StatusOK) })
	admin := e.Group("/admin", mw())
	admin.GET("", users.Index)
	v1 := admin.Group("/v1")
	v1.PUT("/users/:id", h.Index)
	s.api = e.Group("/api")
	s.api.DELETE("/x", health)
	e.Match([]string{"GET", "POST"}, "/m", health)
	e.Add(http.MethodPatch, "/a", health)
	e.Static("/assets", "public")
	e.Group("/uploads", mw()).Static("/", "up")
	e.File("/favicon.ico", "f.png")
	more(v1)
	c := echo.New()
	c.Response().Header().Add("X-Key", "v")
	time.Now().Add(time.Second)
}

func more(g *echo.Group) {
	g.Any("/more", health)
}

func mw() echo.MiddlewareFunc { return nil }
`
	assert.Equal(t, []string{
		"echo GET web.Load./health -> web.health",
		"echo POST web.Load./login -> github.com/acme/app/handlers.Login",
		"echo GET web.Load./inline ->",
		"echo GET web.Load./admin -> web.Users.Index",
		"echo PUT web.Load./admin/v1/users/:id -> web.Users.Index", // h *Users
		"echo DELETE web.Load./api/x -> web.health",
		"echo Match web.Load./m -> web.health",
		"echo Add web.Load./a -> web.health",
		"echo Static web.Load./assets ->",
		"echo Static web.Load./uploads/ ->",
		"echo File web.Load./favicon.ico ->",
		"echo Any web.more./admin/v1/more -> web.health",
	}, routeList(t, "web/routes.go", src))
}

// A group passed to a function from two prefixes keeps the path as written.
func TestEchoGroupPrefixUnknown(t *testing.T) {
	src := `package web

import "github.com/labstack/echo"

func Load(e *echo.Echo, ext *echo.Group) {
	reg(e.Group("/a"))
	b := e.Group("/b")
	reg(b)
	ext.GET("/ext", h)
}

func reg(g *echo.Group) { g.GET("/x", h) }

func h(c echo.Context) error { return nil }
`
	assert.Equal(t, []string{"echo GET web.Load./ext -> web.h", "echo GET web.reg./x -> web.h"}, routeList(t, "web/r.go", src))
}

// A method named GET is no route in a file that imports no router, and a
// test file's router setup is no route of the app.
func TestRoutesNeedRouterImport(t *testing.T) {
	src := `package cache

type Store struct{}

func (s *Store) GET(key string, f func()) {}

func Use(s *Store) { s.GET("/k", Use2) }

func Use2() {}
`
	assert.Empty(t, routeList(t, "cache/c.go", src))
	test := `package web

import "github.com/labstack/echo/v4"

func TestX(e *echo.Echo) { e.GET("/t", h) }
`
	assert.Empty(t, routeList(t, "web/x_test.go", test))
}

func TestChiRoutes(t *testing.T) {
	src := `package api

import (
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/acme/app/handlers"
)

type articles struct{}

func (a articles) List(w http.ResponseWriter, r *http.Request) {}

func show(w http.ResponseWriter, r *http.Request) {}

func Router(auth func(http.Handler) http.Handler) http.Handler {
	r := chi.NewRouter()
	a := articles{}
	r.Get("/", show)
	r.Route("/articles", func(r chi.Router) {
		r.Get("/", a.List)
		r.With(auth).Post("/{id}", handlers.Save)
		r.Route("/{id}/comments", func(c chi.Router) {
			c.Delete("/{cid}", show)
		})
	})
	r.Group(func(g chi.Router) {
		g.Use(auth)
		g.Method(http.MethodPut, "/put", http.HandlerFunc(show))
		g.HandleFunc("/hf", func(w http.ResponseWriter, r *http.Request) {})
	})
	r.Mount("/admin", adminRouter())
	r.Get("/after", show) // the outer r again, no prefix
	return r
}

func adminRouter() http.Handler { return nil }
`
	assert.Equal(t, []string{
		"chi Get api.Router./ -> api.show",
		"chi Get api.Router./articles/ -> api.articles.List",
		"chi Post api.Router./articles/{id} -> github.com/acme/app/handlers.Save",
		"chi Delete api.Router./articles/{id}/comments/{cid} -> api.show",
		"chi Method api.Router./put ->",
		"chi HandleFunc api.Router./hf ->",
		"chi Mount api.Router./admin ->",
		"chi Get api.Router./after -> api.show",
	}, routeList(t, "api/r.go", src))
}

func TestNetHTTPRoutes(t *testing.T) {
	src := `package main

import (
	"net/http"

	"github.com/acme/app/handlers"
)

type server struct{}

func (s *server) item(w http.ResponseWriter, r *http.Request) {}

func health(w http.ResponseWriter, r *http.Request) {}

func main() {
	s := &server{}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /items/{id}", s.item)
	mux.Handle("/static/", http.StripPrefix("/static/", http.FileServer(http.Dir("."))))
	mux.HandleFunc("POST  /login", handlers.Login)
	mux.HandleFunc("example.com/", health)
	http.HandleFunc("/health", health)
	http.HandleFunc("/inline", func(w http.ResponseWriter, r *http.Request) {})
	w := http.Header{}
	w.Add("X-Key", "v")
	_ = http.ListenAndServe(":8080", mux)
}
`
	assert.Equal(t, []string{
		"net/http GET main.main./items/{id} -> main.server.item",
		"net/http Handle main.main./static/ ->",
		"net/http POST main.main./login -> github.com/acme/app/handlers.Login",
		"net/http HandleFunc main.main.example.com/ -> main.health",
		"net/http HandleFunc main.main./health -> main.health",
		"net/http HandleFunc main.main./inline ->",
	}, routeList(t, "main/main.go", src))
}

func TestGinRoutes(t *testing.T) {
	src := `package api

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/acme/app/handlers"
)

func ping(c *gin.Context) {}

func auth() gin.HandlerFunc { return nil }

func Setup() *gin.Engine {
	r := gin.Default()
	r.GET("/ping", ping)
	v1 := r.Group("/v1", auth())
	{
		v1.POST("/login", auth(), handlers.Login)
		admin := v1.Group("/admin")
		admin.Handle(http.MethodPut, "/users/:id", ping)
		admin.Any("/any", func(c *gin.Context) {})
	}
	r.Static("/assets", "./assets")
	r.StaticFile("/favicon.ico", "./f.ico")
	c := &gin.Context{}
	c.Header("X-Key", "v")
	return r
}
`
	assert.Equal(t, []string{
		"gin GET api.Setup./ping -> api.ping",
		"gin POST api.Setup./v1/login -> github.com/acme/app/handlers.Login",
		"gin Handle api.Setup./v1/admin/users/:id -> api.ping",
		"gin Any api.Setup./v1/admin/any ->",
		"gin Static api.Setup./assets ->",
		"gin StaticFile api.Setup./favicon.ico ->",
	}, routeList(t, "api/r.go", src))
}

// A group variable shadowed inside a block must not change the prefix of the
// outer variable after the block ends.
func TestShadowedGroupKeepsOuterPrefix(t *testing.T) {
	src := `package web

import "github.com/gin-gonic/gin"

func list(c *gin.Context) {}

func Load(r *gin.Engine) {
	api := r.Group("/api")
	if true {
		api := r.Group("/internal")
		api.GET("/stats", list)
	}
	api.GET("/users", list)
}
`
	assert.Equal(t, []string{
		"gin GET Load./internal/stats -> list",
		"gin GET Load./api/users -> list",
	}, routeList(t, "r.go", src))
}

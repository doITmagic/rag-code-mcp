package golang

import (
	"fmt"
	"net/http"
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

// Gin cleans the joined path of a group and a route (no double slash, a
// trailing slash kept), and a route without a handler argument is served by
// the group's middleware: still a route.
func TestGinJoinedPathsAndRoutesWithoutHandler(t *testing.T) {
	src := `package api

import "github.com/gin-gonic/gin"

func h(c *gin.Context) {}

func Load() {
	g := gin.New()
	g.Use(h)
	root := g.Group("/")
	root.GET("/index.html", h)
	p := g.Group("/plugin/")
	p.GET("/:id/config", h)
	p.GET("/list/", h)
	p.Group("/deep").GET("", h)
	g.OPTIONS("/*any")
	g.HEAD("/ping")
}
`
	assert.Equal(t, []string{
		"gin GET api.Load./index.html -> api.h",
		"gin GET api.Load./plugin/:id/config -> api.h",
		"gin GET api.Load./plugin/list/ -> api.h",
		"gin GET api.Load./plugin/deep -> api.h",
		"gin OPTIONS api.Load./*any ->",
		"gin HEAD api.Load./ping ->",
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

// echo.Context.File serves a file inside a handler: the receiver is no
// router, so no route.
func TestEchoContextFileIsNoRoute(t *testing.T) {
	src := `package web

import "github.com/labstack/echo/v4"

func index(c echo.Context) error {
	return c.File("/var/www/index.html")
}
`
	assert.Empty(t, routeList(t, "r.go", src))
}

// A chi file: a cache Get with a key starting with "/" is no route, the
// receiver is no router the file shows.
func TestChiCacheGetIsNoRoute(t *testing.T) {
	src := `package web

import "github.com/go-chi/chi/v5"

var r = chi.NewRouter()

func load(cache *Cache, out *Doc) {
	cache.Get("/docs/1", out)
}
`
	assert.Empty(t, routeList(t, "r.go", src))
}

// The receivers a route may have besides a local: a top-level var set from
// a constructor and a struct field of a router type. A parameter of another
// type is no router, even one with a registration method's name.
func TestRouteReceiverEvidence(t *testing.T) {
	src := `package web

import "github.com/labstack/echo/v4"

var e = echo.New()

type server struct{ r *echo.Echo }

func h(c echo.Context) error { return nil }

func init() { e.GET("/top", h) }

func (s *server) load(store *Store) {
	s.r.POST("/field", h)
	store.GET("/key", h)
}
`
	assert.Equal(t, []string{
		"echo GET web.init./top -> web.h",
		"echo POST web.load./field -> web.h",
	}, routeList(t, "web/r.go", src))
}

func TestRouteHandlersOnOneLine(t *testing.T) {
	src := `package routes
import "net/http"
type First struct{}
type Second struct{}
func (First) Handle(http.ResponseWriter, *http.Request) {}
func (Second) Handle(http.ResponseWriter, *http.Request) {}
func Register(mux *http.ServeMux, a First, b Second) {
    mux.HandleFunc("/first", a.Handle); mux.HandleFunc("/second", b.Handle)
}
`
	assert.Equal(t, []string{
		"net/http HandleFunc routes.Register./first -> routes.First.Handle",
		"net/http HandleFunc routes.Register./second -> routes.Second.Handle",
	}, routeList(t, "routes/routes.go", src))
}

func TestNetHTTPPatternsServeMuxAccepts(t *testing.T) {
	for pattern, want := range map[string]string{
		"localhost/status": "net/http HandleFunc routes.Register.localhost/status -> routes.handler",
		"get /status":      "net/http get routes.Register./status -> routes.handler", // a method of its own, case kept
		"GET\t/status":     "net/http GET routes.Register./status -> routes.handler",
	} {
		t.Run(pattern, func(t *testing.T) {
			// Registration validates the pattern without opening any socket.
			http.NewServeMux().HandleFunc(pattern, func(http.ResponseWriter, *http.Request) {})
			src := fmt.Sprintf(`package routes
import "net/http"
func handler(http.ResponseWriter, *http.Request) {}
func Register(mux *http.ServeMux) { mux.HandleFunc(%q, handler) }
`, pattern)
			assert.Equal(t, []string{want}, routeList(t, "routes/routes.go", src))
		})
	}
}

func TestRouterHeldInVarDeclaration(t *testing.T) {
	src := `package rv
import (
	"net/http"
	"github.com/labstack/echo/v4"
)
func hello(c echo.Context) error { return nil }
func serve(http.ResponseWriter, *http.Request) {}
func EchoVar() {
	var e = echo.New()
	e.GET("/var", hello)
	var g = e.Group("/g2")
	g.GET("/items", hello)
	var (
		h = e.Group("/g3")
	)
	h.GET("/items", hello)
	var t *echo.Group = e.Group("/g4")
	t.GET("/items", hello)
	var mux = http.NewServeMux()
	mux.HandleFunc("/mux-var", serve)
	{
		var g = 1 // shadows the group inside the block only
		_ = g
	}
	g.GET("/after", hello)
}
`
	assert.Equal(t, []string{
		"echo GET rv.EchoVar./var -> rv.hello",
		"echo GET rv.EchoVar./g2/items -> rv.hello",
		"echo GET rv.EchoVar./g3/items -> rv.hello",
		"echo GET rv.EchoVar./g4/items -> rv.hello",
		"net/http HandleFunc rv.EchoVar./mux-var -> rv.serve",
		"echo GET rv.EchoVar./g2/after -> rv.hello",
	}, routeList(t, "rv/rv.go", src))
}

// http.DefaultServeMux is the router http.HandleFunc registers on: a call on
// it, or on a local holding it, is a route.
func TestNetHTTPDefaultServeMux(t *testing.T) {
	src := `package main

import "net/http"

func health(w http.ResponseWriter, r *http.Request) {}

func main() {
	http.DefaultServeMux.HandleFunc("/health", health)
	mux := http.DefaultServeMux
	mux.Handle("GET /ready", http.HandlerFunc(health))
	var m = http.DefaultServeMux
	m.HandleFunc("/live", health)
}
`
	assert.Equal(t, []string{
		"net/http HandleFunc main.main./health -> main.health",
		"net/http GET main.main./ready ->",
		"net/http HandleFunc main.main./live -> main.health",
	}, routeList(t, "main/main.go", src))
}

// Gin joins a path written without a leading slash to the group's base path
// ("/" for the engine), so GET("users") is /users and Group("api") +
// GET("ping") is /api/ping. Echo concatenates the group prefix and the path,
// then adds a missing leading slash: GET("users") is /users, Group("/api") +
// GET("ping") is /apiping. Chi panics on such a path, so in a file importing
// chi no router's path without the slash is a route.
func TestRoutePathsWithoutLeadingSlash(t *testing.T) {
	src := `package api

import "github.com/gin-gonic/gin"

func h(c *gin.Context) {}

func Load() {
	r := gin.New()
	r.GET("users", h)
	api := r.Group("api")
	api.GET("ping", h)
	api.Group("v1/").POST("items/", h)
	r.Static("assets", "./assets")
}
`
	assert.Equal(t, []string{
		"gin GET api.Load./users -> api.h",
		"gin GET api.Load./api/ping -> api.h",
		"gin POST api.Load./api/v1/items/ -> api.h",
		"gin Static api.Load./assets ->",
	}, routeList(t, "api/r.go", src))

	mixed := `package api

import (
	"github.com/gin-gonic/gin"
	"github.com/labstack/echo/v4"
)

func eh(c echo.Context) error { return nil }

func Load() {
	e := echo.New()
	e.GET("users", eh)
	e.GET("/ok", eh)
	_ = gin.New()
}
`
	assert.Len(t, routeList(t, "api/r.go", mixed), 2)

	echoSrc := `package api

import "github.com/labstack/echo/v4"

func eh(c echo.Context) error { return nil }

func Load() {
	e := echo.New()
	e.GET("users", eh)
	g := e.Group("/api")
	g.GET("ping", eh)
	e.Group("v2").GET("/x", eh)
}
`
	assert.Equal(t, []string{
		"echo GET api.Load./users -> api.eh",
		"echo GET api.Load./apiping -> api.eh",
		"echo GET api.Load./v2/x -> api.eh",
	}, routeList(t, "api/r.go", echoSrc))

	withChi := `package api

import (
	"github.com/go-chi/chi/v5"
	"github.com/labstack/echo/v4"
)

func eh(c echo.Context) error { return nil }

func Load() {
	e := echo.New()
	e.GET("users", eh)
	e.GET("/ok", eh)
	_ = chi.NewRouter()
}
`
	assert.Equal(t, []string{"echo GET api.Load./users -> api.eh", "echo GET api.Load./ok -> api.eh"}, routeList(t, "api/r.go", withChi),
		"e comes from echo.New: echo's path rule, whatever else the file imports")

	// A router whose framework the file does not show (a struct field) needs
	// every imported router to serve a path without the "/" (chi panics on it).
	untied := `package api

import (
	"github.com/go-chi/chi/v5"
	"github.com/labstack/echo/v4"
)

type S struct{ e *echo.Echo }

func eh(c echo.Context) error { return nil }

func (s *S) Load() {
	s.e.GET("users", eh)
	s.e.GET("/ok", eh)
	_ = chi.NewRouter()
}
`
	assert.Equal(t, []string{"echo GET api.Load./ok -> api.eh"}, routeList(t, "api/r.go", untied))

	// w2 (WSL, Z13): gin and echo groups in one function each keep their own
	// join; the gin ones were /apiping and /v1//index.
	both := `package api

import (
	"github.com/gin-gonic/gin"
	"github.com/labstack/echo/v4"
)

func h(c *gin.Context)         {}
func eh(c echo.Context) error { return nil }

func Load() {
	r := gin.Default()
	g := r.Group("/api")
	g.GET("ping", h)
	r.Group("v1/").GET("/index", h)
	e := echo.New()
	eg := e.Group("/api")
	eg.GET("ping", eh)
}

func Mount(rg *gin.RouterGroup) {
	rg.GET("x", h)
}
`
	assert.Equal(t, []string{
		"gin GET api.Load./api/ping -> api.h",
		"gin GET api.Load./v1/index -> api.h",
		"echo GET api.Load./apiping -> api.eh",
		"gin GET api.Mount./x -> api.h",
	}, routeList(t, "api/r.go", both))
}

func TestRoutePathFromConst(t *testing.T) {
	src := `package cmd

import (
	"path"

	"github.com/labstack/echo/v4"
)

const (
	limit    = 2000
	uriAdmin = "/admin"
)

var uriVar = "/var"

func h(c echo.Context) error { return nil }

func Register(e *echo.Echo) {
	g := e.Group("")
	g.GET(uriAdmin, h)
	g.GET(path.Join(uriAdmin, "/lists"), h)
	g.POST(path.Join(uriAdmin, "/lists/:id/"), h) // path.Join drops the trailing slash, as at run time
	a := e.Group(uriAdmin)
	a.GET("/x", h)
	g.GET(uriVar, h)   // a var may change: no route
	g.GET(path.Join(uriAdmin, name()), h) // run time: no route
}

func Shadow(e *echo.Echo, path string) {
	uriAdmin := "/other"
	e.GET(uriAdmin, h)             // the local, not the const: no route
	e.GET(path+"/x", h)            // no route
}

func name() string { return "" }
`
	assert.Equal(t, []string{
		"echo GET cmd.Register./admin -> cmd.h",
		"echo GET cmd.Register./admin/lists -> cmd.h",
		"echo POST cmd.Register./admin/lists/:id -> cmd.h",
		"echo GET cmd.Register./admin/x -> cmd.h",
	}, routeList(t, "cmd/handlers.go", src))
}

func TestGinUseChain(t *testing.T) {
	src := `package router

import "github.com/gin-gonic/gin"

func auth(c *gin.Context) {}
func create(c *gin.Context) {}

func Create(g *gin.Engine) {
	g.Use(func(ctx *gin.Context) { ctx.Next() })
	g.Group("/user").Use(auth).POST("", create)
	g.Group("/").Use(auth).POST("/message", create)
	api := g.Group("/api").Use(auth)
	api.GET("/x", create)
}
`
	assert.Equal(t, []string{
		"gin POST router.Create./user -> router.create",
		"gin POST router.Create./message -> router.create",
		"gin GET router.Create./api/x -> router.create",
	}, routeList(t, "router/router.go", src))
}

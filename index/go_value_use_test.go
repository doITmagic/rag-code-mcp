package index

import (
	"testing"

	"github.com/doITmagic/rag-code-mcp/v2/parser"
	_ "github.com/doITmagic/rag-code-mcp/v2/parser/golang"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// realGo is the Go analyzer registered by the import above, kept before
// TestMain replaces ".go" with a lineAnalyzer.
var realGo = parser.ForLanguage("go")

// A Go method declared in one file and only passed as a handler in another
// (mux.HandleFunc(p, s.firmaJSON)) is used, not dead code: the value use
// links exactly to it. A field, constant or variable read in the same
// positions leaves no relation at all, not even an unresolved one, also when
// a method of another type shares the field's name.
func TestGoValueUseAcrossFiles(t *testing.T) {
	parser.Register(realGo)
	t.Cleanup(func() { parser.Register(lineAnalyzer{"go", ".go"}) })
	e := newEnv(t)
	e.write(t, "go.mod", "module example.com/app\n\ngo 1.25\n")
	decl := e.write(t, "cmd/api/firma_json.go", `package main

import "net/http"

type server struct{ name string }

type cfg struct{ Model string }

type other struct{}

const route = "/x"

var shared = 1

func (s *server) firmaJSON(w http.ResponseWriter, r *http.Request) {}

func handler(w http.ResponseWriter, r *http.Request) {}

func (o other) Model() string { return "" }

func (o other) name() string { return "" }

func use(...any) {}
`)
	src := `package main

import "net/http"

func main() {
	s := &server{}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /firma.json", s.firmaJSON)
	http.HandleFunc("/x", handler)
	use(s.name, route, shared, http.MethodGet)
}

func (s *server) routes(mux *http.ServeMux, c cfg) {
	mux.HandleFunc("GET /a", s.firmaJSON)
	use(s.name, c.Model)
}
`
	main := e.write(t, "cmd/api/main.go", src)
	_, err := e.run(t, nil)
	require.NoError(t, err)

	check := func(why string) {
		t.Helper()
		ids := e.symbols(t, decl)
		type use struct {
			target string
			line   int
		}
		got := map[string][]use{}
		for _, fn := range []string{"main", "routes"} {
			for _, r := range e.symbols(t, main)[fn].Relations {
				switch r.TargetName {
				case "firmaJSON", "handler", "name", "route", "shared", "MethodGet", "Model":
					assert.Equal(t, parser.ResolutionExact, r.Resolution, why)
					got[fn] = append(got[fn], use{r.TargetID, r.Line})
				}
			}
		}
		assert.Equal(t, map[string][]use{
			"main":   {{ids["firmaJSON"].ID, 8}, {ids["handler"].ID, 9}},
			"routes": {{ids["firmaJSON"].ID, 14}},
		}, got, why)
	}
	check("full run")

	e.write(t, "cmd/api/main.go", src+"\n")
	_, err = e.run(t, nil)
	require.NoError(t, err)
	check("files run, targets from the store")
}

package tools

import (
	"path/filepath"
	"testing"

	_ "github.com/doITmagic/rag-code-mcp/v2/parser/golang"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A handler method declared in firma_json.go and only registered in main.go
// (mux.HandleFunc(p, s.firmaJSON)): rag_find_usages firmaJSON answered
// "0 usages", so the method read as dead code.
func TestUsagesOfGoMethodValueAcrossFiles(t *testing.T) {
	v := newEnv(t, false, nil)
	v.addFiles(t, map[string]string{
		"go.mod": "module example.com/app\n\ngo 1.25\n",
		"cmd/api/firma_json.go": `package main

import "net/http"

type server struct{}

func (s *server) firmaJSON(w http.ResponseWriter, r *http.Request) {}
`,
		"cmd/api/main.go": `package main

import "net/http"

func main() {
	s := &server{}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /firma.json", s.firmaJSON)
	_ = http.ListenAndServe(":8080", mux)
}
`,
	})

	var out struct {
		Files []FileUsages `json:"files"`
		Total int          `json:"total"`
	}
	r, _ := v.call(t, "rag_find_usages", map[string]any{"workspace_root": v.root, "symbol": "firmaJSON"})
	require.Equal(t, "ok", r.Status, r.Message)
	data(t, r, &out)
	// main names it, and so does the route symbol of the registration.
	require.Equal(t, 2, out.Total, r.Message)
	require.Len(t, out.Files, 1, r.Message)
	assert.Equal(t, filepath.Join(v.root, "cmd", "api", "main.go"), out.Files[0].FilePath, r.Message)
	var callers []string
	for _, u := range out.Files[0].Usages {
		callers = append(callers, u.Caller)
	}
	assert.ElementsMatch(t, []string{"example.com/app/cmd/api.main", "example.com/app/cmd/api.main./firma.json"}, callers)
}

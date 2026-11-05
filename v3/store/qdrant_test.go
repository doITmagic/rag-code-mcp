package store

import (
	"errors"
	"testing"

	"github.com/doITmagic/rag-code-mcp/v3/parser"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseQdrantURL(t *testing.T) {
	tests := []struct {
		in   string
		host string
		port int
		tls  bool
	}{
		{"", "localhost", 6334, false},
		{"http://localhost:6333", "localhost", 6334, false},
		{"http://qdrant", "qdrant", 6334, false},
		{"http://qdrant:6334", "qdrant", 6334, false},
		{"http://10.0.0.5:7001", "10.0.0.5", 7001, false},
		{"https://cloud.example:6333", "cloud.example", 6334, true},
		{"qdrant:6333", "qdrant", 6334, false},
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			host, port, tls, err := parseQdrantURL(tt.in)
			require.NoError(t, err)
			assert.Equal(t, tt.host, host)
			assert.Equal(t, tt.port, port)
			assert.Equal(t, tt.tls, tls)
		})
	}
	_, _, _, err := parseQdrantURL("http://h:abc")
	assert.Error(t, err)
}

func TestPointID(t *testing.T) {
	id := pointID("abc")
	assert.Len(t, id, 36)
	assert.Equal(t, byte('5'), id[14], "version nibble")
	assert.Contains(t, "89ab", string(id[19]), "variant nibble")
	assert.Equal(t, id, pointID("abc"))
	assert.NotEqual(t, id, pointID("abd"))
}

func TestQnSuffixesAndDirParts(t *testing.T) {
	assert.Equal(t, []string{"a.b.c", "b.c", "c"}, qnSuffixes("a.b.c"))
	assert.Equal(t, []string{"c"}, qnSuffixes("c"))
	assert.Nil(t, qnSuffixes(""))
	assert.Equal(t, []string{"/w/models", "/w", "/"}, dirParts("/w/models/user.go"))
	assert.Equal(t, []string{"/"}, dirParts("/user.go"))
	assert.Equal(t, []string{"x", "."}, dirParts("x/y.go"))
}

func TestPayloadRoundTrip(t *testing.T) {
	s := parser.Symbol{
		ID: "id1", QualifiedName: `App\Models\User::find`, Name: "find", Kind: parser.KindMethod,
		Module: `App\Models`, Owner: "User", Signature: "find(int $id)", Doc: "Finds.", Content: "func...\xff",
		FilePath: "/w/app/Models/User.php", StartLine: 10, EndLine: 20, Language: "php", Exported: true,
		Relations: []parser.Relation{{Kind: parser.RelCall, TargetName: "query", TargetQualifiedName: "App.DB.query", TargetID: "t1", Line: 12}},
		Metadata:  map[string]string{"framework": "laravel"},
	}
	p, err := toPayload(&s)
	require.NoError(t, err)
	assert.Len(t, p["qn_suffixes"].GetListValue().GetValues(), 4)
	assert.Equal(t, "find", p["qn_suffixes"].GetListValue().GetValues()[3].GetStringValue())
	assert.Equal(t, "t1", p["rel_target_id"].GetListValue().GetValues()[0].GetStringValue())
	assert.Equal(t, "/w/app/Models", p["dir_parts"].GetListValue().GetValues()[0].GetStringValue())

	got := fromPayload(p)
	s.Content = "func...�"
	assert.Equal(t, s, got)
}

func TestToQdrantFilter(t *testing.T) {
	assert.Nil(t, toQdrantFilter(Filter{}))
	f := toQdrantFilter(Filter{
		Language: "go", FilePath: "/f", PathPrefix: "/w/models/", Name: "n", QualifiedName: "q",
		QualifiedSuffix: "User::find", Module: "m", Kinds: []parser.Kind{parser.KindMethod},
		TargetID: "t", TargetQualifiedName: "tq", TargetName: "tn",
	})
	require.NotNil(t, f)
	assert.Len(t, f.Must, 11)
	assert.Equal(t, "/w/models", f.Must[2].GetField().GetMatch().GetKeyword())
	assert.Equal(t, "User.find", f.Must[5].GetField().GetMatch().GetKeyword())
	assert.Equal(t, []string{"method"}, f.Must[7].GetField().GetMatch().GetKeywords().GetStrings())
}

func TestWrap(t *testing.T) {
	assert.NoError(t, wrap(nil))
	assert.ErrorIs(t, wrap(errors.New("GetCollectionInfo() failed: c: rpc error: code = NotFound desc = Collection `c` doesn't exist!")), ErrNotFound)
	other := errors.New("rpc error: code = Unavailable desc = down")
	assert.Equal(t, other, wrap(other))
}

package index

import (
	"testing"

	"github.com/doITmagic/rag-code-mcp/v2/parser"
	_ "github.com/doITmagic/rag-code-mcp/v2/parser/javascript"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// realJS is the JavaScript analyzer registered by the import above, kept
// before a test replaces ".js" with a lineAnalyzer.
var realJS = parser.ForLanguage("javascript")

// `export default { getToken }` re-exports the module's own getToken as a
// member of the default object: JwtService.getToken() from an importer is a
// call of that function (rag_find_usages answered 0 usages).
func TestResolveDefaultObjectShorthandMember(t *testing.T) {
	parser.Register(realJS)
	e := newEnv(t)
	jwt := e.write(t, "src/common/jwt.service.js", `const ID_TOKEN_KEY = "id_token";
export const getToken = () => { return window.localStorage.getItem(ID_TOKEN_KEY); };
function drop() { window.localStorage.removeItem(ID_TOKEN_KEY); }
export default { getToken, destroyToken: drop };
`)
	api := e.write(t, "src/common/api.js", `const read = () => 1;
export const api = { read };
`)
	caller := e.write(t, "src/common/api.service.js", `import JwtService from "./jwt.service";
import { api } from "./api";
export function setHeader() { return JwtService.getToken(); }
export function logout() { JwtService.destroyToken(); }
export function load() { return api.read(); }
`)
	_, err := e.run(t, nil)
	require.NoError(t, err)

	target := func(name string) parser.Relation {
		t.Helper()
		rels := e.symbols(t, caller)[name].Relations
		require.Len(t, rels, 1, name)
		return rels[0]
	}
	for _, c := range []struct{ caller, path, callee, why string }{
		{"setHeader", jwt, "getToken", "shorthand member of export default {...}"},
		{"logout", jwt, "drop", "key: ident member of export default {...}"},
		{"load", api, "read", "shorthand member of an exported const object"},
	} {
		r := target(c.caller)
		assert.Equal(t, e.symbols(t, c.path)[c.callee].ID, r.TargetID, c.why)
		// Exact, not a by-name guess: a second "read" elsewhere must not make it ambiguous.
		assert.Equal(t, parser.ResolutionExact, r.Resolution, c.why)
	}
}

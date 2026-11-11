package tools

import (
	"path/filepath"
	"testing"

	_ "github.com/doITmagic/rag-code-mcp/v2/parser/javascript"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// `export default { getToken }` and `JwtService.getToken()` in an importer:
// rag_find_usages getToken answered "0 usages in 0 files".
func TestUsagesThroughDefaultObjectShorthand(t *testing.T) {
	v := newEnv(t, false, nil)
	v.addFiles(t, map[string]string{
		"src/common/jwt.service.js": `const ID_TOKEN_KEY = "id_token";
export const getToken = () => { return window.localStorage.getItem(ID_TOKEN_KEY); };
export const saveToken = (token) => { window.localStorage.setItem(ID_TOKEN_KEY, token); };
export const destroyToken = () => { window.localStorage.removeItem(ID_TOKEN_KEY); };
export default { getToken, saveToken, destroyToken };
`,
		"src/common/api.service.js": `import JwtService from "./jwt.service";
const ApiService = {
  setHeader() { return JwtService.getToken(); },
};
export default ApiService;
`,
		"src/store/auth.js": `import JwtService from "../common/jwt.service";
export function login(t) { JwtService.saveToken(t); return JwtService.getToken(); }
`,
	})

	var out struct {
		Files []FileUsages `json:"files"`
		Total int          `json:"total"`
	}
	r, _ := v.call(t, "rag_find_usages", map[string]any{"workspace_root": v.root, "symbol": "getToken"})
	require.Equal(t, "ok", r.Status, r.Message)
	data(t, r, &out)
	assert.Equal(t, 2, out.Total, r.Message)
	files := []string{}
	for _, f := range out.Files {
		files = append(files, f.FilePath)
	}
	assert.ElementsMatch(t, []string{filepath.Join(v.root, "src", "common", "api.service.js"), filepath.Join(v.root, "src", "store", "auth.js")}, files, r.Message)
}

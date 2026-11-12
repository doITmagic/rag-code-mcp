package tools

import (
	"path/filepath"
	"testing"

	_ "github.com/doITmagic/rag-code-mcp/v2/parser/php"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// usagesOf returns the usages rag_find_usages lists for symbol.
func usagesOf(t *testing.T, v *env, symbol string) []FileUsages {
	t.Helper()
	var out struct {
		Files []FileUsages `json:"files"`
	}
	r, _ := v.call(t, "rag_find_usages", map[string]any{"workspace_root": v.root, "symbol": symbol})
	require.Equal(t, "ok", r.Status, r.Message)
	data(t, r, &out)
	return out.Files
}

// Two controllers both define index; the route names one of them by its
// action string inside a namespaced group, so only that one has the route as
// a usage.
func TestUsagesOfLaravelControllerActionFromRoute(t *testing.T) {
	v := newEnv(t, false, nil)
	v.addFiles(t, map[string]string{
		"app/Http/Controllers/Backend/UsersController.php": "<?php\nnamespace App\\Http\\Controllers\\Backend;\nclass UsersController { public function index() {} }\n",
		"app/Http/Controllers/Main/UsersController.php":    "<?php\nnamespace App\\Http\\Controllers\\Main;\nclass UsersController { public function index() {} }\n",
		"routes/web.php": `<?php
Route::group(['prefix' => 'admin', 'namespace' => 'Backend'], function () {
    Route::get('/users', ['as' => 'users', 'uses' => 'UsersController@index']);
});
`,
	})
	files := usagesOf(t, v, `App\Http\Controllers\Backend\UsersController::index`)
	require.Len(t, files, 1)
	assert.Equal(t, filepath.Join(v.root, "routes", "web.php"), files[0].FilePath)
	require.Len(t, files[0].Usages, 1)
	assert.Equal(t, "/admin/users", files[0].Usages[0].Caller)
	assert.Equal(t, 3, files[0].Usages[0].Line)
	assert.Empty(t, usagesOf(t, v, `App\Http\Controllers\Main\UsersController::index`))
}

// The controller method returning view('users.show') is a usage of the
// template resources/views/users/show.blade.php.
func TestUsagesOfBladeTemplateFromController(t *testing.T) {
	v := newEnv(t, false, nil)
	v.addFiles(t, map[string]string{
		"resources/views/users/show.blade.php": "<h1>{{ $user->name }}</h1>\n",
		"app/Http/Controllers/UserController.php": `<?php
namespace App\Http\Controllers;
class UserController {
    public function show($id) {
        return view('users.show', ['id' => $id]);
    }
}
`,
	})
	files := usagesOf(t, v, "users.show")
	require.Len(t, files, 1)
	require.Len(t, files[0].Usages, 1)
	assert.Equal(t, `App\Http\Controllers.UserController.show`, files[0].Usages[0].Caller)
	assert.Equal(t, "imports", files[0].Usages[0].Kind)
	assert.False(t, files[0].Usages[0].ByName)
}

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

// An Eloquent relation naming its model as a string and the event map of
// the event provider are usages of those classes.
func TestUsagesOfLaravelModelAndListener(t *testing.T) {
	v := newEnv(t, false, nil)
	v.addFiles(t, map[string]string{
		"app/Lawyer.php":                      "<?php\nnamespace App;\nclass Lawyer {}\n",
		"app/Listeners/SendEmailListener.php": "<?php\nnamespace App\\Listeners;\nclass SendEmailListener {}\n",
		"app/Mandate.php": `<?php
namespace App;
class Mandate {
    public function lawyer() { return $this->belongsTo('App\Lawyer'); }
}
`,
		"app/Providers/EventServiceProvider.php": `<?php
namespace App\Providers;
class EventServiceProvider {
    protected $listen = ['App\Events\SendEmail' => ['App\Listeners\SendEmailListener']];
}
`,
	})
	files := usagesOf(t, v, `App\Lawyer`)
	require.Len(t, files, 1)
	assert.Equal(t, `App.Mandate.lawyer`, files[0].Usages[0].Caller)
	files = usagesOf(t, v, `App\Listeners\SendEmailListener`)
	require.Len(t, files, 1)
	assert.Equal(t, `App\Providers.EventServiceProvider.listen`, files[0].Usages[0].Caller)
}

// Two services define send; the controller calls it on a property the
// constructor assigns from a typed parameter, so the usage is exact and
// belongs to that service only.
func TestUsagesThroughTypedPhpProperty(t *testing.T) {
	v := newEnv(t, false, nil)
	v.addFiles(t, map[string]string{
		"app/Services/Mailer.php": "<?php\nnamespace App\\Services;\nclass Mailer { public function send() {} }\n",
		"app/Services/Sms.php":    "<?php\nnamespace App\\Services;\nclass Sms { public function send() {} }\n",
		"app/Http/Controllers/UserController.php": `<?php
namespace App\Http\Controllers;
use App\Services\Mailer;
class UserController {
    private $mailer;
    public function __construct(Mailer $mailer) { $this->mailer = $mailer; }
    public function store() { $this->mailer->send(); }
}
`,
	})
	files := usagesOf(t, v, `App\Services\Mailer::send`)
	require.Len(t, files, 1)
	require.Len(t, files[0].Usages, 1)
	assert.Equal(t, `App\Http\Controllers.UserController.store`, files[0].Usages[0].Caller)
	assert.False(t, files[0].Usages[0].ByName)
	assert.Empty(t, usagesOf(t, v, `App\Services\Sms::send`))
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

// A Laravel route registered inside a method takes the method's name as
// owner (App\Providers.map./): the method map is still the one symbol named
// map, and the route stays findable by its path.
func TestLaravelRoutePathIsNotANameSegment(t *testing.T) {
	v := newEnv(t, false, nil)
	v.addFiles(t, map[string]string{
		"app/Providers/RouteServiceProvider.php": `<?php
namespace App\Providers;
class RouteServiceProvider {
    public function boot() { $this->map(); }
    public function map() {
        Route::get('/', 'HomeController@index');
        Route::get('/about', 'HomeController@about');
    }
}
`,
	})
	files := usagesOf(t, v, "map")
	require.Len(t, files, 1)
	require.Len(t, files[0].Usages, 1)
	assert.Equal(t, `App\Providers.RouteServiceProvider.boot`, files[0].Usages[0].Caller)

	var found struct {
		Results []SearchHit `json:"results"`
	}
	r, _ := v.call(t, "rag_search", map[string]any{"workspace_root": v.root, "query": "/about", "kinds": []string{"route"}})
	require.Equal(t, "ok", r.Status, r.Message)
	data(t, r, &found)
	require.Len(t, found.Results, 1, r.Message)
	assert.Equal(t, `App\Providers.map./about`, found.Results[0].QualifiedName)
	assert.Equal(t, "exact", found.Results[0].Match)
}

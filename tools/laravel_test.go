package tools

import (
	"path/filepath"
	"strconv"
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
        Route::get('users', 'HomeController@users');
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

	// A path without a leading slash is the route Laravel registers: /users.
	r, _ = v.call(t, "rag_search", map[string]any{"workspace_root": v.root, "query": "/users", "kinds": []string{"route"}})
	require.Equal(t, "ok", r.Status, r.Message)
	data(t, r, &found)
	require.Len(t, found.Results, 1, r.Message)
	assert.Equal(t, `App\Providers.map./users`, found.Results[0].QualifiedName)
}

// A property and a method of one name are two symbols: the call
// $cart->items() is the method's usage, and the name picks the method.
func TestUsagesOfPhpMethodNamedLikeProperty(t *testing.T) {
	v := newEnv(t, false, nil)
	v.addFiles(t, map[string]string{
		"app/Cart.php": `<?php
namespace App;
class Cart {
    private array $items = [
        'sku_a',
        'sku_b',
    ];
    public function items(): array { return $this->items; }
}
`,
		"app/Checkout.php": `<?php
namespace App;
class Checkout {
    public function run(Cart $cart): array { return $cart->items(); }
}
`,
	})
	for _, q := range []string{`App\Cart::items`, "items"} {
		var out struct {
			Symbol Candidate    `json:"symbol"`
			Files  []FileUsages `json:"files"`
		}
		r, _ := v.call(t, "rag_find_usages", map[string]any{"workspace_root": v.root, "symbol": q})
		require.Equal(t, "ok", r.Status, r.Message)
		data(t, r, &out)
		assert.Equal(t, "method", out.Symbol.Kind, q)
		assert.Equal(t, 8, out.Symbol.StartLine, q)
		require.Len(t, out.Files, 1, q)
		require.Len(t, out.Files[0].Usages, 1, q)
		assert.Equal(t, "App.Checkout.run", out.Files[0].Usages[0].Caller, q)
		assert.False(t, out.Files[0].Usages[0].ByName, q)
	}
}

// A Livewire component tag or directive in a view is a usage of the
// component's class and of its view.
func TestUsagesOfLivewireComponent(t *testing.T) {
	v := newEnv(t, false, nil)
	v.addFiles(t, map[string]string{
		"app/Livewire/Likes.php":                   "<?php\n\nnamespace App\\Livewire;\n\nfinal class Likes extends Component\n{\n    public function mount(): void {}\n}\n",
		"app/Livewire/Forum/PostList.php":          "<?php\n\nnamespace App\\Livewire\\Forum;\n\nfinal class PostList extends Component {}\n",
		"app/Models/Counter.php":                   "<?php\n\nnamespace App\\Models;\n\nclass Counter {}\n",
		"resources/views/other.blade.php":          "<livewire:counter />\n",
		"resources/views/livewire/likes.blade.php": "<div>likes</div>\n",
		"resources/views/page.blade.php":           "<div>\n    <livewire:likes :subject=\"$a\" />\n    @livewire('likes', ['subject' => $a])\n    <livewire:forum.post-list />\n</div>\n",
	})
	// a class of the tag's name in another namespace is not the component
	r, _ := v.call(t, "rag_find_usages", map[string]any{"workspace_root": v.root, "symbol": `App\Models\Counter`})
	assert.Contains(t, r.Message, "0 usages", r.Message)
	for symbol, lines := range map[string]int{`App\Livewire\Likes`: 2, "livewire.likes": 2, `App\Livewire\Forum\PostList`: 1} {
		var out struct {
			Files []FileUsages `json:"files"`
		}
		r, _ := v.call(t, "rag_find_usages", map[string]any{"workspace_root": v.root, "symbol": symbol})
		require.Equal(t, "ok", r.Status, symbol+": "+r.Message)
		data(t, r, &out)
		require.Len(t, out.Files, 1, symbol+": "+r.Message)
		assert.Contains(t, r.Message, strconv.Itoa(lines)+" usages", symbol)
	}
}

// route('users.index') in a controller and in a Blade view are usages of the
// route named users.index.
func TestUsagesOfNamedLaravelRoute(t *testing.T) {
	v := newEnv(t, false, nil)
	v.addFiles(t, map[string]string{
		"routes/web.php": "<?php\nRoute::get('/users', 'UserController@index')->name('users.index');\n",
		"app/Http/Controllers/HomeController.php": "<?php\nnamespace App\\Http\\Controllers;\nclass HomeController { public function go() { return redirect()->route('users.index'); } }\n",
		"resources/views/home.blade.php":          "<a href=\"{{ route('users.index') }}\">Users</a>\n",
	})
	files := usagesOf(t, v, "/users")
	var callers []string
	for _, f := range files {
		for _, u := range f.Usages {
			callers = append(callers, u.Caller+":"+strconv.Itoa(u.Line))
		}
	}
	assert.ElementsMatch(t, []string{`App\Http\Controllers.HomeController.go:3`, "home:1"}, callers)
}

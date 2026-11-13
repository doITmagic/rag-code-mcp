package php

import (
	"testing"

	"github.com/doITmagic/rag-code-mcp/v2/parser"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const laravelRoutesSrc = `<?php
use Illuminate\Support\Facades\Route;
use App\Http\Controllers\UserController;

Route::group(['prefix' => 'admin', 'namespace' => 'Backend', 'middleware' => ['auth']], function () {
    Route::get('/sesiune/', ['as' => 'sesiune', 'uses' => 'StatisticsController@getSession']);
    Route::resource('/users', 'UsersController');
    Route::group(['prefix' => 'deep/'], function () {
        Route::post('x', '\App\Other\XController@store');
    });
    Route::get('/', ['as' => 'statistics', 'uses' => 'StatisticsController@index']);
});
Route::prefix('api')->middleware('auth')->group(function () {
    Route::get('/me', [UserController::class, 'me'])->name('me');
    Route::apiResource('photos', PhotoController::class);
});
Route::get('/', 'Main\MainController@index');
Route::get('users', 'Main\UserListController@index');
Route::prefix('panel/')->group(function () {
    Route::get('users', 'Main\PanelController@users');
});
Route::post('/invoke', UserController::class);
Route::view('/about', 'pages.about');
`

func TestLaravelRouteGroupsAndHandlers(t *testing.T) {
	syms := analyze(t, "/root/routes/web.php", laravelRoutesSrc)
	want := map[string]map[string]string{
		"/admin/sesiune": {"call getSession": `Backend.StatisticsController.getSession`},
		"/admin/users":   {"uses_type UsersController": `Backend.UsersController`},
		"/admin/deep/x":  {"call store": `App\Other.XController.store`},
		"/admin":         {"call index": `Backend.StatisticsController.index`},
		"/api/me":        {"call me": `App\Http\Controllers.UserController.me`},
		"/api/photos":    {"uses_type PhotoController": `PhotoController`},
		"/":              {"call index": `Main.MainController.index`},
		"/users":         {"call index": `Main.UserListController.index`},
		"/panel/users":   {"call users": `Main.PanelController.users`},
		"/invoke":        {"uses_type UserController": `App\Http\Controllers.UserController`},
		"/about":         {"imports pages.about": `pages.about`},
	}
	for name, rels := range want {
		s, ok := syms["route "+name]
		require.True(t, ok, "route %s; have %v", name, keys(syms))
		assert.Equal(t, rels, targets(s), name)
	}
	assert.Equal(t, "apiResource", syms["route /api/photos"].Metadata["method"])
}

const laravelViewSrc = `<?php
namespace App\Http\Controllers;

use Illuminate\Support\Facades\View;

class UserController extends Controller
{
    public function show($id)
    {
        if ($id) {
            return view('users.show', ['id' => $id]);
        }
        return View::make('users.empty');
    }

    public function other($name)
    {
        return view($name) . view('mail::message');
    }
}
`

const laravelModelSrc = `<?php
namespace App;

use Illuminate\Database\Eloquent\Model;
use App\Models\Tag;

class Post extends Model
{
    private ?Dispatcher $events;

    protected $casts = ['state' => \App\Casts\State::class];

    public function author() { return $this->belongsTo('App\Lawyer'); }
    public function comments() { return $this->hasMany(Comment::class, 'post_id'); }
    public function tags() { return $this->belongsToMany('\App\Models\Tag'); }
    public function through() { return $this->hasManyThrough('App\Deal', 'App\Lawyer'); }
    public function me() { return [self::class, static::class, $this->hasMany($cls), $this->hasMany('posts table')]; }
}

class EventServiceProvider extends ServiceProvider
{
    protected $listen = [
        'App\Events\SendEmail' => ['App\Listeners\SendEmailListener'],
        Registered::class => [SendWelcome::class],
    ];
}
`

// X::class, Eloquent relations naming the model as a string and the $listen
// map of an event provider are usages of those classes.
func TestLaravelClassReferences(t *testing.T) {
	syms := analyze(t, "/root/app/Post.php", laravelModelSrc)
	m := func(n string) map[string]string { return targets(syms["method App.Post."+n]) }
	assert.Equal(t, map[string]string{"call belongsTo": "App.Post.belongsTo", "uses_type Lawyer": "App.Lawyer"}, m("author"))
	assert.Equal(t, map[string]string{"call hasMany": "App.Post.hasMany", "uses_type Comment": "App.Comment"}, m("comments"))
	assert.Equal(t, map[string]string{"call belongsToMany": "App.Post.belongsToMany", "uses_type Tag": "App\\Models.Tag"}, m("tags"))
	assert.Equal(t, map[string]string{"call hasManyThrough": "App.Post.hasManyThrough", "uses_type Deal": "App.Deal", "uses_type Lawyer": "App.Lawyer"}, m("through"))
	assert.Equal(t, map[string]string{"call hasMany": "App.Post.hasMany"}, m("me"), "self/static::class, a dynamic or non-class string are no usage")
	assert.Equal(t, map[string]string{"uses_type Dispatcher": "App.Dispatcher"}, targets(syms["variable App.Post.events"]))
	assert.Equal(t, map[string]string{"uses_type State": "App\\Casts.State"}, targets(syms["variable App.Post.casts"]))
	assert.Equal(t, map[string]string{
		"uses_type SendEmail":         "App\\Events.SendEmail",
		"uses_type SendEmailListener": "App\\Listeners.SendEmailListener",
		"uses_type Registered":        "App.Registered",
		"uses_type SendWelcome":       "App.SendWelcome",
	}, targets(syms["variable App.EventServiceProvider.listen"]))
}

const typedReceiverSrc = `<?php
namespace App\Http\Controllers;

use App\Services\Mailer;
use App\Repositories\UserRepository;
use Illuminate\Http\Request;

class UserController extends Controller
{
    private ?Mailer $mailer;
    protected $untyped;
    private $repo;
    private $cache;

    public function __construct(private \App\Services\Billing $billing, UserRepository $users, $cache)
    {
        $this->repo = $users;
        $this->cache = $cache;
    }

    public function store(Request $request, int $id, self $other)
    {
        $this->mailer->send($request->input('x'));
        $this->billing?->charge();
        $this->repo->save();
        $this->untyped->run();
        $this->cache->get();
        $other->helper();
        $id->nope();
    }
}

function free(Mailer $m) { $m->queue(); }
`

// A call on a typed property, a promoted or assigned constructor parameter
// or a typed parameter takes the declared class as its target.
func TestTypedReceivers(t *testing.T) {
	syms := analyze(t, "/root/app/Http/Controllers/UserController.php", typedReceiverSrc)
	got := targets(syms[`method App\Http\Controllers.UserController.store`])
	assert.Equal(t, map[string]string{
		"call send":         `App\Services.Mailer.send`,
		"call input":        `Illuminate\Http.Request.input`,
		"call charge":       `App\Services.Billing.charge`,
		"call save":         `App\Repositories.UserRepository.save`,
		"call run":          "",
		"call get":          "",
		"call helper":       `App\Http\Controllers.UserController.helper`,
		"call nope":         "",
		"uses_type Request": `Illuminate\Http.Request`,
	}, got)
	assert.Equal(t, `App\Services.Mailer.queue`, targets(syms[`function App\Http\Controllers.free`])["call queue"])
}

// view('x.y') and View::make('x.y') name the template a controller renders.
func TestLaravelViewCallImportsTemplate(t *testing.T) {
	syms := analyze(t, "/root/app/Http/Controllers/UserController.php", laravelViewSrc)
	var imports []string
	for _, r := range syms[`method App\Http\Controllers.UserController.show`].Relations {
		if r.Kind == "imports" {
			imports = append(imports, r.TargetQualifiedName)
		}
	}
	assert.Equal(t, []string{"users.show", "users.empty"}, imports)
	for _, r := range syms[`method App\Http\Controllers.UserController.other`].Relations {
		assert.NotEqual(t, "imports", string(r.Kind), "a dynamic or namespaced view name is not a template of the workspace")
	}
}

// A closure or arrow function parameter shadows the outer variable of the
// same name, typed or not; use() and an arrow function bring the outer one in.
func TestClosureReceiverScope(t *testing.T) {
	syms := analyze(t, "/root/app.php", `<?php
namespace App;
class Mailer { public function a() {} public function b() {} public function c() {} public function d() {} public function e() {} public function f() {} }
class Sms { public function a() {} public function b() {} }
function outer(Mailer $service) {
    $x = function (Sms $service) { $service->a(); };
    $y = fn(Sms $service) => $service->b();
    $z = function ($service) { $service->c(); };
    $w = fn($service) => $service->d();
    $v = function () use ($service) { $service->e(); };
    $u = fn() => $service->f();
}
`)
	assert.Equal(t, map[string]string{
		"call a":           "App.Sms.a",
		"call b":           "App.Sms.b",
		"call c":           "",
		"call d":           "",
		"call e":           "App.Mailer.e",
		"call f":           "App.Mailer.f",
		"uses_type Mailer": "App.Mailer",
	}, targets(syms["function App.outer"]))
}

// A class string is read with PHP's escapes: 'App\\User' names App\User.
func TestEscapedClassStrings(t *testing.T) {
	syms := analyze(t, "/root/app.php", `<?php
namespace App;
use Illuminate\Support\Facades\Route;
class User extends \Illuminate\Database\Eloquent\Model {}
class Post extends \Illuminate\Database\Eloquent\Model {
    public function author() { return $this->belongsTo('App\\User'); }
    public function editor() { return $this->belongsTo("App\\Editor"); }
}
class Controller { public function show() {} }
Route::get('/user', 'App\\Controller@show');
`)
	assert.Equal(t, "App.User", parser.Normalize(targets(syms["method App.Post.author"])["uses_type User"]))
	assert.Equal(t, "App.Editor", parser.Normalize(targets(syms["method App.Post.editor"])["uses_type Editor"]))
	assert.Equal(t, "App.Controller.show", parser.Normalize(targets(syms["route App./user"])["call show"]))
}

func TestDoubleQuotedListenMap(t *testing.T) {
	syms := analyze(t, "/root/app/EventServiceProvider.php", `<?php
namespace App;
class EventServiceProvider {
    protected $listen = [
        "App\\Events\\OrderShipped" => ["App\\Listeners\\SendShipmentNotification"],
    ];
}
`)
	got := targets(syms["variable App.EventServiceProvider.listen"])
	assert.Equal(t, "App.Events.OrderShipped", parser.Normalize(got["uses_type OrderShipped"]))
	assert.Equal(t, "App.Listeners.SendShipmentNotification", parser.Normalize(got["uses_type SendShipmentNotification"]))
}

// A Mailable's $this->view('x') and ->markdown('x') name the template it
// renders, the same way view('x') does.
func TestMailableViewImportsTemplate(t *testing.T) {
	syms := analyze(t, "/root/app/Mail/OrderShipped.php", `<?php
namespace App\Mail;
class OrderShipped extends Mailable
{
    public function build()
    {
        $this->view('emails.orders.shipped');
        return $this->subject('Shipped')->markdown('emails.orders.md');
    }
    public function other($v) { return $this->view($v)->markdown('mail::message'); }
}
`)
	imports := func(qn string) []string {
		var out []string
		for _, r := range syms[qn].Relations {
			if r.Kind == parser.RelImports {
				out = append(out, r.TargetQualifiedName)
			}
		}
		return out
	}
	assert.Equal(t, []string{"emails.orders.shipped", "emails.orders.md"}, imports("method App\\Mail.OrderShipped.build"))
	assert.Empty(t, imports("method App\\Mail.OrderShipped.other"), "a dynamic or package view name is not a template of the workspace")
}

// A class written in the code is a receiver type: app(Foo::class)->m(), and
// a local whose every assignment in the function is new Foo(...). A local
// assigned anything else, or from a call, has none.
func TestWrittenReceiverTypes(t *testing.T) {
	syms := analyze(t, "/root/app/Jobs/Sync.php", `<?php
namespace App\Jobs;
use App\Services\Mailer;
use App\Models\User;
class Sync
{
    public function handle($p, Mailer $typed)
    {
        app(Mailer::class)->a();
        $m = new Mailer($p);
        $m->b();
        $u = User::find(1);
        $u->c();
        $twice = new Mailer();
        $twice = $this->other();
        $twice->d();
        $loop = new Mailer();
        foreach ($p as $loop) { $loop->e(); }
        $p = new Mailer();
        $p->f();
        $inner = function () { $x = new Mailer(); $x->g(); };
        $x->h();
        $s = new \App\Sms();
        $s->i();
    }
}
`)
	got := targets(syms[`method App\Jobs.Sync.handle`])
	for call, want := range map[string]string{
		"call a": `App\Services.Mailer.a`,
		"call b": `App\Services.Mailer.b`,
		"call c": "",
		"call d": "",
		"call e": "",
		"call f": "",
		"call g": `App\Services.Mailer.g`,
		"call h": "",
		"call i": `App.Sms.i`,
	} {
		assert.Equal(t, want, got[call], call)
	}
}

// Inside Route::controller(C::class)->group(...) (also chained with prefix,
// nested, or the array form 'controller' => C::class) a string action names
// a method of that controller.
func TestRouteControllerGroup(t *testing.T) {
	syms := analyze(t, "/root/routes/web.php", `<?php
use Illuminate\Support\Facades\Route;
use App\Http\Controllers\UserController;
use App\Http\Controllers\PostController;

Route::controller(UserController::class)->group(function () {
    Route::get('/users', 'index');
    Route::post('/users/new', [PostController::class, 'store']);
    Route::prefix('admin')->controller(PostController::class)->group(function () {
        Route::get('/posts', 'list');
    });
    Route::get('/legacy', 'Other\LegacyController@show');
});
Route::group(['controller' => PostController::class, 'prefix' => 'p'], function () {
    Route::get('/{id}', 'show');
});
Route::get('/plain', 'InvokableController');
`)
	want := map[string]map[string]string{
		"/users":       {"call index": `App\Http\Controllers.UserController.index`},
		"/users/new":   {"call store": `App\Http\Controllers.PostController.store`},
		"/admin/posts": {"call list": `App\Http\Controllers.PostController.list`},
		"/legacy":      {"call show": `Other.LegacyController.show`},
		"/p/{id}":      {"call show": `App\Http\Controllers.PostController.show`},
		"/plain":       {"uses_type InvokableController": `InvokableController`},
	}
	for name, rels := range want {
		s, ok := syms["route "+name]
		require.True(t, ok, "route %s; have %v", name, keys(syms))
		assert.Equal(t, rels, targets(s), name)
	}
}

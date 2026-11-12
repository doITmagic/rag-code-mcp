package php

import (
	"bytes"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/doITmagic/rag-code-mcp/v2/parser"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const verifySrc = `<?php
namespace App\Services;

use App\Models\User as U;
use App\Repo\{UserRepo, function helper, const LIMIT};
use Illuminate\Support\Facades\Route as R;
use Psr\Log\LoggerInterface;

interface Reporter extends \Countable, LoggerInterface {}

enum Level: int implements HasLabel
{
    case Low = 1;
}

/**
 * Service.
 */
class Svc extends Repo\Base implements Reporter
{
    use \App\Traits\Loggable;

    /** Run it. */
    public function run(int $n): void
    {
        U::find($n);
        UserRepo::make();
        helper();
        strlen('x');
        $u = new U();
        $r = new Repo\Other();
        Repo\Other::go();
        \Vendor\Lib::boot();
        $this->own();
        self::s();
        static::t();
        parent::run($n);
        $u->save();
        $u?->flush();
        $me = new self();
        $st = new static();
        $cb = function () { $this->inClosure(); UserRepo::inClosure(); };
        $arr = fn() => helper();
        R::get('/aliased', fn() => 1);
        Route::post('/plain', fn() => 1);
        add_filter('the_title', fn($t) => $t);
    }

    private function own(): void {}
    protected static function s(): void {}
}
`

func TestVerifyResolution(t *testing.T) {
	const ns = `App\Services`
	syms := analyze(t, "/root/app/Services/Svc.php", verifySrc)

	rep := syms["interface "+ns+".Reporter"]
	require.NotEmpty(t, rep.Name)
	assert.Equal(t, map[string]string{
		"extends Countable":       "Countable",
		"extends LoggerInterface": `Psr\Log.LoggerInterface`,
	}, targets(rep))

	lvl, ok := syms["class "+ns+".Level"]
	require.True(t, ok)
	assert.Equal(t, map[string]string{"implements HasLabel": ns + ".HasLabel"}, targets(lvl))
	assert.Equal(t, "Level", syms["constant "+ns+".Level.Low"].Owner)

	svc, ok := syms["class "+ns+".Svc"]
	require.True(t, ok)
	assert.Equal(t, "Service.", svc.Doc)
	assert.Equal(t, map[string]string{
		"extends Base":        ns + `\Repo.Base`,
		"implements Reporter": ns + ".Reporter",
		"uses_type Loggable":  `App\Traits.Loggable`,
	}, targets(svc))

	run, ok := syms["method "+ns+".Svc.run"]
	require.True(t, ok)
	assert.Equal(t, "Run it.", run.Doc)
	assert.True(t, run.Exported)
	assert.Equal(t, "public function run(int $n): void", run.Signature)
	got := targets(run)
	want := map[string]string{
		"call find":       `App\Models.User.find`,
		"call make":       `App\Repo.UserRepo.make`,
		"call helper":     `App\Repo.helper`,
		"call strlen":     "", // global fallback: name lookup
		"uses_type U":     `App\Models.User`,
		"uses_type Other": ns + `\Repo.Other`,
		"call go":         ns + `\Repo.Other.go`,
		"call boot":       `Vendor.Lib.boot`,
		"call own":        ns + ".Svc.own",
		"call s":          ns + ".Svc.s",
		"call t":          ns + ".Svc.t",
		"call run":        "",
		"call save":       "",
		"call flush":      "",
		"call inClosure":  `App\Repo.UserRepo.inClosure`, // last wins in the map; both checked below
		"call get":        `Illuminate\Support\Facades.Route.get`,
		"call post":       ns + ".Route.post",
		"call add_filter": "",
	}
	for k, v := range want {
		assert.Equal(t, v, got[k], k)
	}
	var self, static, closures []string
	for _, r := range run.Relations {
		switch {
		case r.Kind == parser.RelUsesType && r.TargetName == "self":
			self = append(self, r.TargetQualifiedName)
		case r.Kind == parser.RelUsesType && r.TargetName == "static":
			static = append(static, r.TargetQualifiedName)
		case r.TargetName == "inClosure":
			closures = append(closures, r.TargetQualifiedName)
		}
		assert.NotZero(t, r.Line)
	}
	t.Logf("new self -> %v, new static -> %v, inClosure -> %v", self, static, closures)
	assert.Equal(t, []string{ns + ".Svc.inClosure", `App\Repo.UserRepo.inClosure`}, closures)
	for _, s := range append(self, static...) {
		assert.Contains(t, []string{"", ns + ".Svc"}, s, "new self/static must be the owner or unknown, never a guess")
	}

	assert.False(t, syms["method "+ns+".Svc.own"].Exported)
	assert.False(t, syms["method "+ns+".Svc.s"].Exported)

	// Owner is the enclosing symbol Name ("run"), so the qualified name is ns.run.<arg>.
	for _, k := range []string{"route " + ns + ".run./aliased", "route " + ns + ".run./plain", "hook " + ns + ".run.the_title"} {
		s, ok := syms[k]
		assert.True(t, ok, "missing %s; have %v", k, keys(syms))
		assert.Equal(t, "run", s.Owner)
	}
	assert.Equal(t, 44, syms["route "+ns+".run./aliased"].StartLine)
}

func keys(m map[string]parser.Symbol) []string {
	var out []string
	for k := range m {
		if strings.HasPrefix(k, "route") || strings.HasPrefix(k, "hook") {
			out = append(out, k)
		}
	}
	return out
}

const wpSrc = `<?php
/*
Plugin Name: Demo
*/
if (!defined('ABSPATH')) exit;
define('DEMO_VERSION', '1.0');
add_action('init', 'demo_init');
add_filter('the_content', 'demo_content', 20);

function demo_init() {
    register_post_type('demo');
    add_action('admin_menu', 'demo_menu');
}
class Demo_Widget extends WP_Widget {
    public function __construct() { parent::__construct('demo', 'Demo'); add_shortcode('demo', [$this, 'render']); }
    public function render($atts) { return $this->wrap(shortcode_atts([], $atts)); }
}
`

func TestVerifyGlobalWordPress(t *testing.T) {
	syms := analyze(t, "/root/wp-content/plugins/demo/demo.php", wpSrc)
	for k, owner := range map[string]string{"hook init": "", "hook the_content": "", "hook demo_init.admin_menu": "demo_init"} {
		s, ok := syms[k]
		require.True(t, ok, k)
		assert.Equal(t, "", s.Module)
		assert.Equal(t, owner, s.Owner)
		assert.Equal(t, "wordpress", s.Metadata["framework"])
	}
	assert.Equal(t, "filter", syms["hook the_content"].Metadata["kind"])
	assert.Equal(t, 7, syms["hook init"].StartLine)
	c, ok := syms["constant DEMO_VERSION"]
	require.True(t, ok)
	assert.Equal(t, 6, c.StartLine)
	assert.Equal(t, map[string]string{"extends WP_Widget": "WP_Widget"}, targets(syms["class Demo_Widget"]))
	assert.Equal(t, map[string]string{"call __construct": "", "call add_shortcode": "add_shortcode"}, targets(syms["method Demo_Widget.__construct"]))
	assert.Equal(t, map[string]string{"call wrap": "Demo_Widget.wrap", "call shortcode_atts": "shortcode_atts"}, targets(syms["method Demo_Widget.render"]))
	assert.Equal(t, map[string]string{"call register_post_type": "register_post_type", "call add_action": "add_action"}, targets(syms["function demo_init"]))
}

func TestVerifyRobustness(t *testing.T) {
	big := bytes.Repeat([]byte("function f"+strings.Repeat("x", 10)+"() { return g(); }\n"), 20000)
	inputs := map[string][]byte{
		"one byte":   []byte("<"),
		"binary":     append([]byte("<?php\n"), bytes.Repeat([]byte{0x00, 0xff, 0xfe, 0x80}, 300)...),
		"truncated":  []byte(verifySrc[:len(verifySrc)/2]),
		"tag only":   []byte("<?php"),
		"use only":   []byte("<?php use ;use A\\{};use function ;"),
		"nulls":      bytes.Repeat([]byte{0}, 64),
		"big":        append([]byte("<?php\n"), big...),
		"deep nest":  []byte("<?php " + strings.Repeat("if(1){", 2000)),
		"blade 1b":   []byte("@"),
		"blade junk": []byte("@section(\n@extends(')\n@include('a', 'b')\n@component\n@endsection"),
	}
	for name, src := range inputs {
		t.Run(name, func(t *testing.T) {
			path := "/root/x.php"
			if strings.HasPrefix(name, "blade") {
				path = "/root/x.blade.php"
			}
			done := make(chan struct{})
			var err error
			var n int
			go func() {
				defer close(done)
				var syms []parser.Symbol
				syms, err = parser.Analyze(parser.File{Path: path, Source: src})
				n = len(syms)
			}()
			select {
			case <-done:
			case <-time.After(5 * time.Second):
				t.Fatal("hang")
			}
			if err != nil && (strings.Contains(err.Error(), "parse stopped") || strings.Contains(err.Error(), "brackets")) {
				err = nil // a timeout on a slow run or refused garbage is an honest failure, not a hang
			}
			assert.NoError(t, err)
			t.Logf("%s: %d symbols", name, n)
		})
	}
}

// ---- second verification pass: a fresh Laravel routes file, a mixed
// multi-namespace file and a Blade layout ----

const routesSrc = `<?php

use Illuminate\Support\Facades\Route;
use App\Http\Controllers\{UserController as UC, PostController};
use App\Models as M;
use function App\Support\{tap as t2, flash};
use const App\Support\MAX;

Route::get('/users', [UC::class, 'index']);
Route::match(['get', 'post'], '/mixed', [PostController::class, 'store'])->name('mixed');
Route::group(['prefix' => 'admin'], function () {
    Route::delete('/users/{id}', [UC::class, 'destroy']);
});
Route::resource('posts', PostController::class);
Route::view('/about', 'about');

function boot(): void {
    M\User::query();
    t2(1);
    flash('x');
    PostController::make();
    new UC();
}
`

func TestVerifyRoutesFile(t *testing.T) {
	syms := analyze(t, "/root/routes/web.php", routesSrc)
	for name, line := range map[string]int{"/users": 9, "/mixed": 10, "posts": 14} {
		s, ok := syms["route "+name]
		require.True(t, ok, "route %s; have %v", name, keys(syms))
		assert.Equal(t, "", s.Module)
		assert.Equal(t, "", s.Owner)
		assert.Equal(t, line, s.StartLine)
		assert.Equal(t, "laravel", s.Metadata["framework"])
		assert.True(t, s.Exported)
	}
	// group has no string-literal argument of its own: the nested delete is
	// the route, owned by nothing (top-level closure), with the group prefix.
	d, ok := syms["route /admin/users/{id}"]
	require.True(t, ok, "nested route; have %v", keys(syms))
	assert.Equal(t, "", d.Owner)
	assert.Equal(t, "delete", d.Metadata["method"])
	_, ok = syms["route /about"]
	assert.True(t, ok, "Route::view is a route")

	boot, ok := syms["function boot"]
	require.True(t, ok)
	assert.Equal(t, "boot", boot.QualifiedName)
	assert.Equal(t, map[string]string{
		"call query":   `App\Models.User.query`, // qualified name through the namespace alias M
		"call t2":      `App\Support.tap`,       // "use function ... as"
		"call flash":   `App\Support.flash`,     // group "use function"
		"call make":    `App\Http\Controllers.PostController.make`,
		"uses_type UC": `App\Http\Controllers.UserController`,
	}, targets(boot))
}

const multiSrc = `<?php
namespace A;
use B\Thing;
abstract class Base {
    public const int LIMIT = 3;
    var $legacy;
    abstract protected function tpl(): void;
    public static function make(): static { return new static(); }
}
namespace C;
use A\Base as AB;
interface Marker extends \Stringable, Other\Iface {}
trait Helper {
    public static function helper(): void { self::other(); static::other(); }
    private function other(): void {}
}
final class Impl extends AB implements Marker {
    use Helper { helper as protected aliased; }
    public function __construct(public readonly string $name) { parent::__construct(); }
    public function run($x) {
        $x?->go();
        $fn = static fn() => self::make();
        function nested() { leaked(); }
        Thing::create();
        AB::make();
        $this->other();
    }
}
`

func TestVerifyMultiNamespace(t *testing.T) {
	syms := analyze(t, "/root/src/multi.php", multiSrc)
	base, ok := syms["class A.Base"]
	require.True(t, ok)
	assert.Equal(t, "A", base.Module)
	assert.Equal(t, "abstract class Base", base.Signature)
	assert.Equal(t, "Base", syms["constant A.Base.LIMIT"].Owner)
	assert.True(t, syms["constant A.Base.LIMIT"].Exported)
	assert.True(t, syms["variable A.Base.legacy"].Exported, "var $x is public")
	assert.False(t, syms["method A.Base.tpl"].Exported)
	assert.Equal(t, "abstract protected function tpl(): void", syms["method A.Base.tpl"].Signature)
	assert.Equal(t, map[string]string{"uses_type static": "A.Base"}, targets(syms["method A.Base.make"]))

	m, ok := syms["interface C.Marker"]
	require.True(t, ok)
	assert.Equal(t, map[string]string{"extends Stringable": "Stringable", "extends Iface": `C\Other.Iface`}, targets(m))

	h, ok := syms["class C.Helper"]
	require.True(t, ok, "trait is a class")
	_ = h
	assert.Equal(t, map[string]string{"call other": "C.Helper.other"}, targets(syms["method C.Helper.helper"]))
	assert.False(t, syms["method C.Helper.other"].Exported)

	impl, ok := syms["class C.Impl"]
	require.True(t, ok)
	assert.Equal(t, map[string]string{
		"extends AB":        "A.Base", // alias from the second namespace's own import table
		"implements Marker": "C.Marker",
		"uses_type Helper":  "C.Helper",
	}, targets(impl))
	assert.Equal(t, "Impl", syms["variable C.Impl.name"].Owner)
	assert.True(t, syms["variable C.Impl.name"].Exported)
	assert.Equal(t, map[string]string{"call __construct": ""}, targets(syms["method C.Impl.__construct"]))

	run, ok := syms["method C.Impl.run"]
	require.True(t, ok)
	got := targets(run)
	assert.Equal(t, map[string]string{
		"call go":     "",
		"call make":   "A.Base.make", // last "make": AB::make(); self::make() inside the static closure is checked below
		"call create": "C.Thing",     // placeholder, replaced below
		"call other":  "C.Impl.other",
	}["call go"], got["call go"])
	assert.Equal(t, "C.Impl.other", got["call other"])
	assert.Equal(t, "A.Base.make", got["call make"])
	// "use B\Thing" belongs to namespace A; in C the import table was reset.
	assert.Equal(t, "C.Thing.create", got["call create"])
	assert.NotContains(t, got, "call leaked", "nested function declaration must not leak")
	var makes []string
	for _, r := range run.Relations {
		if r.TargetName == "make" {
			makes = append(makes, r.TargetQualifiedName)
		}
	}
	assert.Equal(t, []string{"C.Impl.make", "A.Base.make"}, makes)
	_, ok = syms["function C.nested"]
	assert.False(t, ok, "declaration nested in a method is not a top-level symbol")
}

const layoutSrc = "<!doctype html>\r\n@section(\"sidebar\")\r\n  @includeWhen($x, 'partials.x')\r\n  @includeFirst(['a', 'b'])\r\n  @yield('content')\r\n@show\r\n@component('card', ['t' => 1])\r\n  @section('inner')\r\n  @endsection\r\n@endcomponent\r\n@include(\"partials.footer\")\r\n"

func TestVerifyBladeLayout(t *testing.T) {
	syms, err := parser.Analyze(parser.File{Path: "/root/resources/views/layouts/app.blade.php", Source: []byte(layoutSrc)})
	require.NoError(t, err)
	byQN := map[string]parser.Symbol{}
	for _, s := range syms {
		assert.Equal(t, parser.KindTemplate, s.Kind)
		byQN[s.QualifiedName] = s
	}
	file := byQN["layouts.app"]
	assert.Equal(t, "app", file.Name)
	assert.Equal(t, "layouts", file.Module)
	assert.Equal(t, []parser.Relation{{Kind: parser.RelImports, TargetName: "partials.footer", TargetQualifiedName: "partials.footer", Line: 11}}, file.Relations,
		"@includeWhen/@includeFirst/@yield are not imports; @include with double quotes is")
	sb, ok := byQN["layouts.app.sidebar"]
	require.True(t, ok)
	assert.Equal(t, 2, sb.StartLine)
	assert.Equal(t, 6, sb.EndLine, "@show closes a section")
	card, ok := byQN["layouts.app.card"]
	require.True(t, ok)
	assert.Equal(t, 7, card.StartLine)
	t.Logf("card ends at %d (nested @section inside @component)", card.EndLine)
	inner, ok := byQN["layouts.app.inner"]
	require.True(t, ok)
	assert.Equal(t, 8, inner.StartLine)
	assert.Equal(t, 9, inner.EndLine)
	assert.Len(t, syms, 4)
}

func TestVerifyNoSharedState(t *testing.T) {
	// A file with imports followed by one without: the second must not see the
	// first's import table (no state survives Analyze).
	a := "<?php\nnamespace X;\nuse Y\\Z;\nfunction f() { Z::a(); }\n"
	b := "<?php\nnamespace X;\nfunction g() { Z::b(); }\n"
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 20; j++ {
				sa := analyzeQuiet(t, "/root/a.php", a)
				sb := analyzeQuiet(t, "/root/b.php", b)
				assert.Equal(t, `Y.Z.a`, targets(sa["function X.f"])["call a"])
				assert.Equal(t, `X.Z.b`, targets(sb["function X.g"])["call b"])
			}
		}()
	}
	wg.Wait()
}

func analyzeQuiet(t *testing.T, path, src string) map[string]parser.Symbol {
	syms, err := parser.Analyze(parser.File{Path: path, Source: []byte(src)})
	assert.NoError(t, err)
	out := map[string]parser.Symbol{}
	for _, s := range syms {
		out[string(s.Kind)+" "+s.QualifiedName] = s
	}
	return out
}

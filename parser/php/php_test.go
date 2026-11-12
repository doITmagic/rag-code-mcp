package php

import (
	"maps"
	"slices"
	"sync"
	"testing"

	"github.com/doITmagic/rag-code-mcp/v2/parser"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const controllerSrc = `<?php

declare(strict_types=1);

namespace App\Http\Controllers;

use App\Models\User;
use App\Services\{Mailer, Logger as Log};
use Illuminate\Http\Request as Req;
use function App\Helpers\format_name;
use const App\Helpers\MAX_ITEMS;
use Illuminate\Support\Facades\Route;

const TOP = 1;
define('OTHER', 2);

/**
 * User controller.
 * Handles users.
 */
#[Attr]
final class UserController extends Controller implements Countable, \JsonSerializable
{
    use HasFactory, Loggable;

    public const VERSION = '1.0';
    private static ?int $count = null;
    protected string $name = 'x';
    public readonly int $id;

    /** Show a user. */
    public static function show(Req $request, int $id): \Illuminate\View\View
    {
        $user = User::find($id);
        $m = new Mailer();
        $l = new Log();
        $this->validate($request);
        self::helper();
        static::other();
        parent::show($request);
        $request->input('x');
        format_name($user);
        strlen('a');
        $fn = function () use ($user) { $user->save(); helper2(); };
        $arrow = fn($x) => bar($x);
        $cls = new class { public function inner() { innerCall(); } };
        return view('users.show');
    }

    abstract protected function helper();

    public function __construct(private Mailer $mailer) {}
}

interface Countable { public function count(): int; }
trait Loggable { public function log(string $m): void { Log::info($m); } }
enum Status: string { case Active = 'active'; const X = 1; public function label(): string { return 'a'; } }

function free_fn(array $a = []) { return add_action('init', 'cb'); }

Route::get('/users', [UserController::class, 'index']);
Route::post('/users', fn() => 1)->name('x');
Route::group(['prefix' => 'admin'], function () { Route::resource('photos', PhotoController::class); });
add_action('init', 'my_init', 10, 2);
add_filter('the_content', [$this, 'filter']);
`

func analyze(t *testing.T, path, src string) map[string]parser.Symbol {
	t.Helper()
	syms, err := parser.Analyze(parser.File{Path: path, Root: "/root", Source: []byte(src)})
	require.NoError(t, err)
	byQN := map[string]parser.Symbol{}
	for _, s := range syms {
		assert.Equal(t, "php", s.Language)
		assert.Equal(t, path, s.FilePath)
		assert.NotEmpty(t, s.ID)
		assert.LessOrEqual(t, s.StartLine, s.EndLine)
		byQN[string(s.Kind)+" "+s.QualifiedName] = s
	}
	return byQN
}

func targets(s parser.Symbol) map[string]string {
	out := map[string]string{}
	for _, r := range s.Relations {
		out[string(r.Kind)+" "+r.TargetName] = r.TargetQualifiedName
	}
	return out
}

func TestRegistration(t *testing.T) {
	assert.NotNil(t, parser.ForFile("/a/b.php"))
	assert.NotNil(t, parser.ForFile("/a/B.Blade.PHP"))
	assert.Equal(t, "php", parser.ForLanguage("php").Language())
}

func TestController(t *testing.T) {
	const ns = `App\Http\Controllers`
	syms := analyze(t, "/root/app/Http/Controllers/UserController.php", controllerSrc)

	tests := []struct {
		key            string
		owner          string
		exported       bool
		start, end     int
		doc, signature string
	}{
		{"constant " + ns + ".TOP", "", true, 14, 14, "", "TOP = 1"},
		{"class " + ns + ".UserController", "", true, 21, 53, "User controller.\nHandles users.",
			`final class UserController extends Controller implements Countable, \JsonSerializable`},
		{"constant " + ns + ".UserController.VERSION", "UserController", true, 26, 26, "", "VERSION = '1.0'"},
		{"variable " + ns + ".UserController.count", "UserController", false, 27, 27, "", "private static ?int $count = null"},
		{"variable " + ns + ".UserController.name", "UserController", false, 28, 28, "", "protected string $name = 'x'"},
		{"variable " + ns + ".UserController.id", "UserController", true, 29, 29, "", "public readonly int $id"},
		{"method " + ns + ".UserController.show", "UserController", true, 32, 48, "Show a user.",
			`public static function show(Req $request, int $id): \Illuminate\View\View`},
		{"method " + ns + ".UserController.helper", "UserController", false, 50, 50, "", "abstract protected function helper()"},
		{"method " + ns + ".UserController.__construct", "UserController", true, 52, 52, "", "public function __construct(private Mailer $mailer)"},
		{"variable " + ns + ".UserController.mailer", "UserController", false, 52, 52, "", "private Mailer $mailer"},
		{"interface " + ns + ".Countable", "", true, 55, 55, "", "interface Countable"},
		{"method " + ns + ".Countable.count", "Countable", true, 55, 55, "", "public function count(): int"},
		{"class " + ns + ".Loggable", "", true, 56, 56, "", "trait Loggable"},
		{"class " + ns + ".Status", "", true, 57, 57, "", "enum Status: string"},
		{"constant " + ns + ".Status.Active", "Status", true, 57, 57, "", "case Active = 'active'"},
		{"constant " + ns + ".Status.X", "Status", true, 57, 57, "", "X = 1"},
		{"method " + ns + ".Status.label", "Status", true, 57, 57, "", "public function label(): string"},
		{"function " + ns + ".free_fn", "", true, 59, 59, "", "function free_fn(array $a = [])"},
		{"hook " + ns + ".free_fn.init", "free_fn", true, 59, 59, "", "add_action('init', 'cb')"},
		{"route " + ns + "./users", "", true, 62, 62, "", "Route::post('/users', fn() => 1)"},
		{"route " + ns + ".admin/photos", "", true, 63, 63, "", "Route::resource('photos', PhotoController::class)"},
		{"hook " + ns + ".init", "", true, 64, 64, "", "add_action('init', 'my_init', 10, 2)"},
		{"hook " + ns + ".the_content", "", true, 65, 65, "", "add_filter('the_content', [$this, 'filter'])"},
	}
	for _, tt := range tests {
		t.Run(tt.key, func(t *testing.T) {
			s, ok := syms[tt.key]
			require.True(t, ok, "missing %s", tt.key)
			assert.Equal(t, ns, s.Module)
			assert.Equal(t, tt.owner, s.Owner)
			assert.Equal(t, tt.exported, s.Exported)
			assert.Equal(t, tt.start, s.StartLine)
			assert.Equal(t, tt.end, s.EndLine)
			assert.Equal(t, tt.doc, s.Doc)
			assert.Equal(t, tt.signature, s.Signature)
		})
	}

	other, ok := syms["constant OTHER"]
	require.True(t, ok, "define() creates a global constant")
	assert.Equal(t, "", other.Module)
	assert.Equal(t, 15, other.StartLine)
	assert.Equal(t, "define('OTHER', 2)", other.Signature)

	// Route "/users" is declared twice (get, post); the last one wins in the map
	// but both are produced.
	routes := 0
	for k := range syms {
		if k == "route "+ns+"./users" {
			routes++
		}
	}
	assert.Equal(t, 1, routes)
	assert.Equal(t, map[string]string{"framework": "laravel", "method": "post"}, syms["route "+ns+"./users"].Metadata)
	assert.Equal(t, map[string]string{"framework": "wordpress", "kind": "action"}, syms["hook "+ns+".init"].Metadata)
	assert.Equal(t, map[string]string{"framework": "wordpress", "kind": "filter"}, syms["hook "+ns+".the_content"].Metadata)
	assert.Contains(t, syms["class "+ns+".UserController"].Content, "#[Attr]\nfinal class")

	cls := syms["class "+ns+".UserController"]
	assert.Equal(t, map[string]string{
		"extends Controller":          ns + ".Controller",
		"implements Countable":        ns + ".Countable",
		"implements JsonSerializable": "JsonSerializable",
		"uses_type HasFactory":        ns + ".HasFactory",
		"uses_type Loggable":          ns + ".Loggable",
	}, targets(cls))
	for _, r := range cls.Relations {
		want := 22
		if r.Kind == parser.RelUsesType {
			want = 24
		}
		assert.Equal(t, want, r.Line, r.TargetName)
	}

	show := syms["method "+ns+".UserController.show"]
	assert.Equal(t, map[string]string{
		"imports users.show": "users.show",
		"call find":          `App\Models.User.find`,
		"uses_type Mailer":   `App\Services.Mailer`,
		"uses_type Req":      `Illuminate\Http.Request`,
		"uses_type View":     `Illuminate\View.View`,
		"uses_type Log":      `App\Services.Logger`,
		"call validate":      ns + ".UserController.validate",
		"call helper":        ns + ".UserController.helper",
		"call other":         ns + ".UserController.other",
		"call show":          "",
		"call input":         `Illuminate\Http.Request.input`, // $request declared Req
		"call format_name":   `App\Helpers.format_name`,
		"call strlen":        "", // global fallback: name lookup
		"call save":          "",
		"call helper2":       "",
		"call bar":           "",
		"call view":          "",
	}, targets(show), "innerCall from the anonymous class must not leak into show")
	for _, r := range show.Relations {
		if r.TargetName == "find" {
			assert.Equal(t, 34, r.Line)
		}
		if r.TargetName == "helper2" {
			assert.Equal(t, 44, r.Line)
		}
	}
	assert.Equal(t, map[string]string{"call info": `App\Services.Logger.info`}, targets(syms["method "+ns+".Loggable.log"]))
	assert.Equal(t, map[string]string{"call add_action": ""}, targets(syms["function "+ns+".free_fn"]))
}

const pluginSrc = `<?php
/**
 * Plugin Name: My Plugin
 */
namespace Vendor\Plugin {
    use \Foo\Bar;
    // line comment doc
    function init_plugin() {
        $x = new Bar;
        $y = new static;
        $z = $obj?->run();
        \Other\Ns::call();
        namespace\local_fn();
        Sub\Thing::make();
        Bar\Baz::make();
    }
    if (!function_exists('Vendor\Plugin\helper')) {
        function helper() {}
    }
    add_action('init', __NAMESPACE__ . '\init_plugin');
    add_action("wp_head", function () { Route::get('/x', 'y'); });
}
?>
<div><?php echo esc_html($x); ?></div>
<?php
class Broken extends \Base {
    public function ok() { return 1; }
    public function bad( {
        foo(;
    }
    public function after() { return 2; }
}
`

func TestPluginBracedNamespaceAndRecovery(t *testing.T) {
	const ns = `Vendor\Plugin`
	syms := analyze(t, "/root/wp-content/plugins/my/my.php", pluginSrc)

	fn, ok := syms["function "+ns+".init_plugin"]
	require.True(t, ok)
	assert.Equal(t, "line comment doc", fn.Doc)
	assert.Equal(t, 8, fn.StartLine)
	assert.Equal(t, 16, fn.EndLine)
	assert.Equal(t, map[string]string{
		"uses_type Bar":    "Foo.Bar",
		"uses_type static": "", // no enclosing class
		"call run":         "",
		"call call":        `Other.Ns.call`,
		"call local_fn":    ns + ".local_fn",
		"call make":        `Foo\Bar.Baz.make`, // Bar is imported, Sub is not: the last "make" wins
	}, targets(fn))
	var makes []string
	for _, r := range fn.Relations {
		if r.TargetName == "make" {
			makes = append(makes, r.TargetQualifiedName)
		}
	}
	assert.Equal(t, []string{ns + `\Sub.Thing.make`, `Foo\Bar.Baz.make`}, makes)

	_, ok = syms["function "+ns+".helper"]
	assert.True(t, ok, "declaration inside function_exists guard")

	assert.Equal(t, "", syms["hook "+ns+".init"].Owner)
	assert.Equal(t, 20, syms["hook "+ns+".init"].StartLine)
	assert.Equal(t, "wordpress", syms["hook "+ns+".wp_head"].Metadata["framework"])
	assert.Equal(t, "laravel", syms["route "+ns+"./x"].Metadata["framework"], "Route call inside a top-level closure")

	// Statements after the braced namespace are back in the global namespace.
	broken, ok := syms["class Broken"]
	require.True(t, ok)
	assert.Equal(t, "", broken.Module)
	assert.Equal(t, map[string]string{"extends Base": "Base"}, targets(broken))
	for _, m := range []string{"ok", "bad", "after"} {
		s, ok := syms["method Broken."+m]
		assert.True(t, ok, "method %s survives the syntax error", m)
		assert.Equal(t, "Broken", s.Owner)
	}
}

func TestMalformed(t *testing.T) {
	tests := []struct {
		name, src string
	}{
		{"empty", ""},
		{"no php tag", "just text"},
		{"unterminated class", "<?php\nclass {\n"},
		{"garbage", "<?php \x00\xff function ( {{{ ) ;;; class A extends"},
		{"name only", "<?php function (){}; new; Foo::; $x->();"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			syms, err := parser.Analyze(parser.File{Path: "/root/x.php", Source: []byte(tt.src)})
			assert.NoError(t, err)
			for _, s := range syms {
				assert.NotEmpty(t, s.Name)
			}
		})
	}
}

func TestGlobalNamespaceFunctionCalls(t *testing.T) {
	syms := analyze(t, "/root/f.php", "<?php\nuse function Lib\\helper;\nfunction a() { helper(); b(); \\c(); }\n")
	assert.Equal(t, map[string]string{"call helper": "Lib.helper", "call b": "b", "call c": "c"}, targets(syms["function a"]))
	assert.Equal(t, "a", syms["function a"].QualifiedName)
}

func TestConcurrentAnalyze(t *testing.T) {
	want, err := parser.Analyze(parser.File{Path: "/root/c.php", Source: []byte(controllerSrc)})
	require.NoError(t, err)
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 5; j++ {
				got, err := parser.Analyze(parser.File{Path: "/root/c.php", Source: []byte(controllerSrc)})
				assert.NoError(t, err)
				assert.Equal(t, want, got)
				_, err = parser.Analyze(parser.File{Path: "/root/p.php", Source: []byte(pluginSrc)})
				assert.NoError(t, err)
			}
		}()
	}
	wg.Wait()
}

// Parameter and return type declarations are type uses, resolved like
// extends; scalar types and self/static/parent are not.
func TestSignatureTypeUses(t *testing.T) {
	m := analyze(t, "/ws/app/Core/Cache.php", `<?php
namespace App\Core;
use App\Webpage;
class Cache {
    public function addModal(Webpage $modal, ?\App\User $u, int $i, A|B $x, self $s): Webpage { return $modal; }
}
function make(Webpage $w): static {}
`)
	got := func(s parser.Symbol) []string {
		var out []string
		for _, r := range s.Relations {
			if r.Kind == parser.RelUsesType {
				out = append(out, r.TargetQualifiedName)
			}
		}
		return out
	}
	assert.ElementsMatch(t, []string{"App.Webpage", "App.User", "App\\Core.A", "App\\Core.B", "App.Webpage"}, got(m["method App\\Core.Cache.addModal"]))
	assert.Equal(t, []string{"App.Webpage"}, got(m["function App\\Core.make"]))
}

// Hook and route names come only from a plain string literal in the name
// position; otherwise the callback string must not become the name.
func TestFrameworkNameIsFirstLiteral(t *testing.T) {
	syms := analyze(t, "/root/p.php", `<?php
add_action( $tag, 'zz_callback' );
add_filter( 'zz_prefix_' . $tag, 'zz_callback', 10 );
add_action( "x_{$tag}", 'zz_callback' );
add_action( "ok_hook", 'zz_callback' );
Route::get($uri, 'Ctrl@m');
Route::match(['get', 'post'], '/mixed', 'Ctrl@m');
`)
	var names []string
	for _, s := range syms {
		if s.Kind == parser.KindHook || s.Kind == parser.KindRoute {
			names = append(names, s.QualifiedName)
		}
	}
	assert.ElementsMatch(t, []string{"ok_hook", "/mixed"}, names)
}

// A class named with a word the grammar reserves (Object) parses as an error
// node followed by the base as "name": the class keeps its own name and the
// base stays its base.
func TestReservedClassName(t *testing.T) {
	syms := analyze(t, "app/Object.php", "<?php\nnamespace ZZ;\nclass Object extends Base {\n  public $table = 'x';\n}\nclass Object2 extends Base {}\n")
	o, ok := syms["class ZZ.Object"]
	require.True(t, ok, "%v", slices.Collect(maps.Keys(syms)))
	assert.Equal(t, "ZZ.Base", targets(o)["extends Base"])
	_, ok = syms["class ZZ.Base"]
	assert.False(t, ok)
	_, ok = syms["variable ZZ.Object.table"]
	assert.True(t, ok)
	_, ok = syms["class ZZ.Object2"]
	assert.True(t, ok)
}

// A call in a -> chain split over lines is on the line of its method name.
func TestChainedCallLine(t *testing.T) {
	syms := analyze(t, "src/Impl.php", "<?php\nnamespace A;\nclass Impl {\n  function run() {\n    $y = $this\n      ->builder()\n      ->where('a', 1);\n  }\n}\n")
	lines := map[string]int{}
	for _, r := range syms["method A.Impl.run"].Relations {
		lines[r.TargetName] = r.Line
	}
	assert.Equal(t, map[string]int{"builder": 6, "where": 7}, lines)
}

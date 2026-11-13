package index

import (
	"fmt"
	"strings"
	"testing"

	"github.com/doITmagic/rag-code-mcp/v2/parser"
	_ "github.com/doITmagic/rag-code-mcp/v2/parser/php"
	_ "github.com/doITmagic/rag-code-mcp/v2/parser/web"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A non-fresh run (resume, big pull) whose files call many symbols already
// in the store must resolve every one of them, not only the first 500.
func TestRunResolvesBeyondFiveHundredTargets(t *testing.T) {
	e := newEnv(t)
	var defs, calls []string
	for i := 0; i < 600; i++ {
		defs = append(defs, fmt.Sprintf("func T%d", i))
		calls = append(calls, fmt.Sprintf("func F%d -> b.T%d", i, i))
	}
	e.write(t, "b.go", strings.Join(defs, "\n"))
	_, err := e.run(t, nil)
	require.NoError(t, err)

	a := e.write(t, "a.go", strings.Join(calls, "\n"))
	_, err = e.run(t, nil)
	require.NoError(t, err)
	unresolved := 0
	for _, s := range e.symbols(t, a) {
		if s.Relations[0].TargetID == "" {
			unresolved++
		}
	}
	assert.Equal(t, 0, unresolved)

	// A later run without changes does not repair them.
	_, err = e.run(t, nil)
	require.NoError(t, err)
	unresolved = 0
	for _, s := range e.symbols(t, a) {
		if s.Relations[0].TargetID == "" {
			unresolved++
		}
	}
	assert.Equal(t, 0, unresolved, "after an unchanged rerun")
}

// A JS declaration both named and default-exported is qualified
// "<module>.default", while `import { sleep }` names "<module>.sleep": the
// top-level symbol of that module with that name is the target.
func TestResolveByModuleAndName(t *testing.T) {
	parser.Register(lineAnalyzer{"javascript", ".js"})
	e := newEnv(t)
	b := e.write(t, "b.js", "func sleep as default")
	a := e.write(t, "a.js", "func F -> b.sleep")
	e.write(t, "g.go", "func H -> b.sleep") // Go never takes this path
	_, err := e.run(t, nil)
	require.NoError(t, err)
	target := e.symbols(t, b)["sleep"].ID
	require.NotEmpty(t, target)
	assert.Equal(t, target, e.symbols(t, a)["F"].Relations[0].TargetID, "full run")

	c := e.write(t, "c.js", "func G -> b.sleep")
	_, err = e.run(t, nil)
	require.NoError(t, err)
	assert.Equal(t, target, e.symbols(t, c)["G"].Relations[0].TargetID, "from the store")
	assert.Empty(t, e.symbols(t, e.root+"/g.go")["H"].Relations[0].TargetID)
}

// onclick="toggle()" in a page names the browser global window.toggle: the
// JavaScript global function of that name, by name, from html only; a
// method or a module function of that name is out of its reach.
func TestResolveHTMLHandlerToJavaScriptGlobal(t *testing.T) {
	parser.Register(lineAnalyzer{"javascript", ".js"})
	parser.Register(lineAnalyzer{"html", ".fkh"})
	e := newEnv(t)
	js := e.write(t, "filter.js", "func toggle as global\nvar shown")
	e.write(t, "mod.js", "func toggle\nmethod Cart.toggle")
	e.write(t, "t.go", "func toggle")
	h := e.write(t, "page.fkh", "func page -> window.toggle\nfunc other -> window.shown")
	a := e.write(t, "a.js", "func F -> window.toggle")
	_, err := e.run(t, nil)
	require.NoError(t, err)
	target := e.symbols(t, js)["toggle"].ID
	require.NotEmpty(t, target)
	r := e.symbols(t, h)["page"].Relations[0]
	assert.Equal(t, target, r.TargetID, "full run")
	assert.Equal(t, parser.ResolutionUnique, r.Resolution)
	assert.Empty(t, e.symbols(t, h)["other"].Relations[0].TargetID, "a call by name never binds a variable")
	assert.Empty(t, e.symbols(t, a)["F"].Relations[0].TargetID, "only html handlers")

	h2 := e.write(t, "page2.fkh", "func page2 -> window.toggle")
	_, err = e.run(t, nil)
	require.NoError(t, err)
	assert.Equal(t, target, e.symbols(t, h2)["page2"].Relations[0].TargetID, "from the store")
}

// onclick="toggle()" names window.toggle, but a Go package or a JavaScript
// module called window holding a toggle is not the browser global.
func TestResolveHTMLHandlerIgnoresModuleNamedWindow(t *testing.T) {
	parser.Register(realGo)
	parser.Register(realJS)
	t.Cleanup(func() {
		parser.Register(lineAnalyzer{"go", ".go"})
		parser.Register(lineAnalyzer{"javascript", ".js"})
	})
	e := newEnv(t)
	e.write(t, "go.mod", "module acme.test/app\n\ngo 1.26\n")
	e.write(t, "window/win.go", "package window\n\nfunc toggle() {}\n")
	e.write(t, "ui/window.js", "export const toggle = false;\n")
	js := e.write(t, "web/app.js", "function toggle() {}\n")
	html := e.write(t, "web/page.html", "<button onclick=\"toggle()\">Toggle</button>\n")
	_, err := e.run(t, nil)
	require.NoError(t, err)
	rels := e.symbols(t, html)["page"].Relations
	require.Len(t, rels, 1)
	assert.Equal(t, e.symbols(t, js)["toggle"].ID, rels[0].TargetID)
	assert.Equal(t, parser.ResolutionUnique, rels[0].Resolution)
}

var realPHP = parser.ForLanguage("php")

// A PHP property and a method of one name are not twins: the call
// $cart->items() binds to the method, never to the wider property.
func TestResolvePHPCallPrefersMethodOverProperty(t *testing.T) {
	parser.Register(realPHP)
	t.Cleanup(func() { parser.Register(lineAnalyzer{lang: "php", ext: ".php"}) })

	e := newEnv(t)
	cart := e.write(t, "Cart.php", `<?php
namespace App;

class Cart {
    private array $items = [
        'sku_a',
        'sku_b',
        'sku_c',
    ];

    public function items(): array { return $this->items; }
}
`)
	caller := e.write(t, "Checkout.php", `<?php
namespace App;

class Checkout {
    public function run(Cart $cart): array {
        return $cart->items();
    }
}
`)

	_, err := e.run(t, nil)
	require.NoError(t, err)

	var methodID string
	for _, s := range e.all(t, cart) {
		if s.Name == "items" && s.Kind == parser.KindMethod {
			methodID = s.ID
		}
	}
	require.NotEmpty(t, methodID)

	var callTargetID string
	for _, r := range e.symbols(t, caller)["run"].Relations {
		if r.Kind == parser.RelCall && r.TargetName == "items" {
			callTargetID = r.TargetID
		}
	}
	assert.Equal(t, methodID, callTargetID)
}

// route('users.index') names the Laravel route whose metadata name is
// users.index, whatever its URI: exact when one route has that name,
// ambiguous when several, in a full run, from the store, and again when the
// routes file gives an existing route that name later.
func TestResolveRouteNameToRoute(t *testing.T) {
	parser.Register(realPHP)
	t.Cleanup(func() { parser.Register(lineAnalyzer{lang: "php", ext: ".php"}) })

	e := newEnv(t)
	routesSrc := `<?php
use Illuminate\Support\Facades\Route;
Route::get('/users', 'UserController@index')->name('users.index');
Route::get('/a', 'UserController@a')->name('dup');
Route::get('/b', 'UserController@b')->name('dup');
Route::get('/later', 'UserController@later');
`
	routes := e.write(t, "routes/web.php", routesSrc)
	ctrl := e.write(t, "app/C.php", `<?php
namespace App;
class C {
    public function go() { return route('users.index'); }
    public function dup() { return route('dup'); }
    public function later() { return route('later.page'); }
}
`)
	_, err := e.run(t, nil)
	require.NoError(t, err)
	ref := func(file, fn string) parser.Relation {
		for _, r := range e.symbols(t, file)[fn].Relations {
			if r.Kind == parser.RelImports {
				return r
			}
		}
		t.Fatalf("%s has no route reference", fn)
		return parser.Relation{}
	}
	users := e.symbols(t, routes)["/users"].ID
	require.NotEmpty(t, users)
	assert.Equal(t, users, ref(ctrl, "go").TargetID, "full run")
	assert.Equal(t, parser.ResolutionExact, ref(ctrl, "go").Resolution)
	assert.Equal(t, parser.ResolutionAmbiguous, ref(ctrl, "dup").Resolution)
	assert.Empty(t, ref(ctrl, "later").TargetID)

	other := e.write(t, "app/D.php", "<?php\nnamespace App;\nclass D { public function go() { return route('users.index'); } }\n")
	_, err = e.run(t, nil)
	require.NoError(t, err)
	assert.Equal(t, users, ref(other, "go").TargetID, "from the store")

	// The route keeps its line, so its id: only the name is new.
	e.write(t, "routes/web.php", strings.Replace(routesSrc, "'UserController@later');", "'UserController@later')->name('later.page');", 1))
	_, err = e.run(t, nil)
	require.NoError(t, err)
	assert.Equal(t, e.symbols(t, routes)["/later"].ID, ref(ctrl, "later").TargetID, "a caller of a name the run adds")
}

// A route handler 'Backend\StatisticsController@uploadImage' names that
// controller's method only: when it lacks one, the route never falls back by
// name to another controller's uploadImage; one its base declares is taken.
func TestResolveRouteHandlerNeverByName(t *testing.T) {
	parser.Register(realPHP)
	t.Cleanup(func() { parser.Register(lineAnalyzer{lang: "php", ext: ".php"}) })

	e := newEnv(t)
	e.write(t, "app/Http/Controllers/Backend/StatisticsController.php", "<?php\nnamespace App\\Http\\Controllers\\Backend;\nuse App\\Http\\Controllers\\Controller;\nclass StatisticsController extends Controller { public function index() {} }\n")
	other := e.write(t, "app/Http/Controllers/Backend/BackendController.php", "<?php\nnamespace App\\Http\\Controllers\\Backend;\nclass BackendController { public function uploadImage() {} }\n")
	base := e.write(t, "app/Http/Controllers/Controller.php", "<?php\nnamespace App\\Http\\Controllers;\nclass Controller { public function inherited() {} }\n")
	routes := e.write(t, "routes/web.php", `<?php
Route::post('/upload', ['uses' => 'Backend\StatisticsController@uploadImage']);
Route::get('/inherited', 'Backend\StatisticsController@inherited');
`)
	_, err := e.run(t, nil)
	require.NoError(t, err)
	require.NotEmpty(t, e.symbols(t, other)["uploadImage"].ID)
	assert.Empty(t, e.symbols(t, routes)["/upload"].Relations[0].TargetID, "BackendController::uploadImage is another controller's")
	assert.Equal(t, e.symbols(t, base)["inherited"].ID, e.symbols(t, routes)["/inherited"].Relations[0].TargetID, "the controller's base")
}

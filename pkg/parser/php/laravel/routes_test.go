package laravel

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestRouteAnalyzer_Analyze(t *testing.T) {
	code := `<?php
use Illuminate\Support\Facades\Route;
use App\Http\Controllers\UserController;
use App\Http\Controllers\PostController;

Route::get('/', function () {
    return view('welcome');
});

Route::get('/users', [UserController::class, 'index']);
Route::post('/users', [UserController::class, 'store']);
Route::put('/users/{id}', 'UserController@update');

Route::match(['get', 'post'], '/match', [PostController::class, 'handle']);

Route::resource('photos', PhotoController::class);
`
	// Create temp file
	tmpDir := t.TempDir()
	routeFile := filepath.Join(tmpDir, "web.php")
	err := os.WriteFile(routeFile, []byte(code), 0644)
	assert.NoError(t, err)

	// Analyze
	analyzer := NewRouteAnalyzer()
	routes, err := analyzer.Analyze([]string{routeFile})
	assert.NoError(t, err)
	assert.NotEmpty(t, routes)

	// Verify routes
	// 1. Closure route
	assert.Equal(t, "GET", routes[0].Method)
	assert.Equal(t, "/", routes[0].URI)
	assert.Equal(t, "Closure", routes[0].Controller)

	// 2. Array syntax route
	assert.Equal(t, "GET", routes[1].Method)
	assert.Equal(t, "/users", routes[1].URI)
	assert.Equal(t, "UserController", routes[1].Controller)
	assert.Equal(t, "index", routes[1].Action)

	// 3. String syntax route
	assert.Equal(t, "PUT", routes[3].Method)
	assert.Equal(t, "/users/{id}", routes[3].URI)
	assert.Equal(t, "UserController", routes[3].Controller)
	assert.Equal(t, "update", routes[3].Action)

	// 4. Match route (creates 2 routes)
	matchFound := 0
	for _, r := range routes {
		if r.URI == "/match" {
			matchFound++
			assert.Contains(t, []string{"GET", "POST"}, r.Method)
			assert.Equal(t, "PostController", r.Controller)
		}
	}
	assert.Equal(t, 2, matchFound)

	// 5. Resource route (creates 7 routes)
	resourceFound := 0
	for _, r := range routes {
		if r.Controller == "PhotoController" {
			resourceFound++
		}
	}
	assert.Equal(t, 7, resourceFound)
}

func TestRouteAnalyzer_ApiResourceAndFluentGroups(t *testing.T) {
	code := `<?php
use Illuminate\Support\Facades\Route;
use App\Http\Controllers\CommentController;
use App\Http\Controllers\Admin\DashboardController;

Route::apiResource('comments', CommentController::class);

Route::prefix('admin')->middleware(['auth', 'admin'])->name('admin.')->group(function () {
    Route::get('/dashboard', [DashboardController::class, 'index']);
});
`
	tmpDir := t.TempDir()
	routesDir := filepath.Join(tmpDir, "routes")
	assert.NoError(t, os.MkdirAll(routesDir, 0755))
	routeFile := filepath.Join(routesDir, "api.php")
	assert.NoError(t, os.WriteFile(routeFile, []byte(code), 0644))

	analyzer := NewRouteAnalyzer()
	routes, err := analyzer.Analyze([]string{routeFile})
	assert.NoError(t, err)

	// apiResource should generate 5 routes (index, store, show, update, destroy)
	apiResActions := make(map[string]string)
	var dashboardRoute *Route
	for i := range routes {
		r := &routes[i]
		if r.Controller == "CommentController" {
			apiResActions[r.Action] = r.Method
		}
		if r.Controller == "DashboardController" {
			dashboardRoute = r
		}
	}
	assert.Len(t, apiResActions, 5)
	assert.Equal(t, "GET", apiResActions["index"])
	assert.Equal(t, "POST", apiResActions["store"])
	assert.Equal(t, "GET", apiResActions["show"])
	assert.Equal(t, "PUT/PATCH", apiResActions["update"])
	assert.Equal(t, "DELETE", apiResActions["destroy"])

	if assert.NotNil(t, dashboardRoute, "expected fluent group route") {
		assert.Equal(t, "admin/dashboard", dashboardRoute.URI)
		assert.Equal(t, []string{"auth", "admin"}, dashboardRoute.Middleware)
		assert.Equal(t, "admin.", dashboardRoute.Name)
	}

	// Single file passed to findRouteFiles must not be returned twice
	adapter := NewAdapter()
	foundFiles := adapter.findRouteFiles([]string{routeFile})
	assert.Equal(t, []string{routeFile}, foundFiles)
}


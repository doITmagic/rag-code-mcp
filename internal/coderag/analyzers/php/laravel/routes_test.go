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

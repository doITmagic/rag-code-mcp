package php

import (
	"testing"

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
Route::post('/invoke', UserController::class);
Route::view('/about', 'pages.about');
`

func TestLaravelRouteGroupsAndHandlers(t *testing.T) {
	syms := analyze(t, "/root/routes/web.php", laravelRoutesSrc)
	want := map[string]map[string]string{
		"/admin/sesiune":  {"call getSession": `Backend.StatisticsController.getSession`},
		"/admin/users":    {"uses_type UsersController": `Backend.UsersController`},
		"admin/deep/x":    {"call store": `App\Other.XController.store`},
		"/admin":          {"call index": `Backend.StatisticsController.index`},
		"/api/me":         {"call me": `App\Http\Controllers.UserController.me`},
		"api/photos":      {"uses_type PhotoController": `PhotoController`},
		"/":               {"call index": `Main.MainController.index`},
		"/invoke":         {"uses_type UserController": `App\Http\Controllers.UserController`},
		"/about":          {"imports pages.about": `pages.about`},
	}
	for name, rels := range want {
		s, ok := syms["route "+name]
		require.True(t, ok, "route %s; have %v", name, keys(syms))
		assert.Equal(t, rels, targets(s), name)
	}
	assert.Equal(t, "apiResource", syms["route api/photos"].Metadata["method"])
}

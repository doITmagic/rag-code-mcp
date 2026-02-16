package php

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestPHPAnalyzer_LaravelModel(t *testing.T) {
	code := `<?php
namespace App\Models;

use Illuminate\Database\Eloquent\Model;

/**
 * User Model
 */
class User extends Model {
    protected $table = 'users';
    protected $fillable = ['name', 'email'];

    public function posts() {
        return $this->hasMany(Post::class);
    }

    public function profile() {
        return $this->hasOne('App\Models\Profile');
    }
}
`
	a := NewAnalyzer()
	symbols, err := a.Parse(context.Background(), "User.php", []byte(code))
	assert.NoError(t, err)

	// Should find: Class, method posts, method profile
	var foundModel bool
	var postRelation bool
	var profileRelation bool

	for _, sym := range symbols {
		if sym.Name == "User" {
			foundModel = true
			assert.Equal(t, "model", sym.Metadata["laravel_type"])
			assert.Equal(t, "users", sym.Metadata["table"])
			assert.Contains(t, sym.Metadata["fillable"], "name")
			
			relations, ok := sym.Metadata["relations"].([]any)
			if ok {
				for _, r := range relations {
					rel := r.(map[string]any)
					if rel["name"] == "posts" {
						postRelation = true
						assert.Equal(t, "hasMany", rel["type"])
						assert.Equal(t, "Post", rel["related"])
					}
					if rel["name"] == "profile" {
						profileRelation = true
						assert.Equal(t, "hasOne", rel["type"])
						assert.Equal(t, "App\\Models\\Profile", rel["related"])
					}
				}
			}
		}
	}

	assert.True(t, foundModel)
	assert.True(t, postRelation)
	assert.True(t, profileRelation)
}

func TestPHPAnalyzer_LaravelRoutes(t *testing.T) {
	code := `<?php
use Illuminate\Support\Facades\Route;

Route::get('/users', [UserController::class, 'index']);
Route::post('/api/save', 'LegacyController@store');
`
	a := NewAnalyzer()
	symbols, err := a.Parse(context.Background(), "web.php", []byte(code))
	assert.NoError(t, err)

	var foundUserRoute bool
	var foundApiRoute bool

	for _, sym := range symbols {
		if sym.Name == "/users" {
			foundUserRoute = true
			assert.Equal(t, "get", sym.Metadata["method"])
			assert.Equal(t, "UserController", sym.Metadata["controller"])
			assert.Equal(t, "index", sym.Metadata["action"])
		}
		if sym.Name == "/api/save" {
			foundApiRoute = true
			assert.Equal(t, "post", sym.Metadata["method"])
			assert.Equal(t, "LegacyController", sym.Metadata["controller"])
			assert.Equal(t, "LegacyController@store", sym.Metadata["action"])
		}
	}

	assert.True(t, foundUserRoute)
	assert.True(t, foundApiRoute)
}

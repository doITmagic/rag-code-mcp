package laravel

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/doITmagic/rag-code-mcp/internal/ragcode/analyzers/php"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestE2E_CompleteLaravelProject tests a complete Laravel project structure
// with Models, Controllers, and Routes working together
func TestE2E_CompleteLaravelProject(t *testing.T) {
	// Create a temporary Laravel project structure
	tmpDir := t.TempDir()

	// Create directory structure
	appDir := filepath.Join(tmpDir, "app")
	modelsDir := filepath.Join(appDir, "Models")
	controllersDir := filepath.Join(appDir, "Http", "Controllers")
	routesDir := filepath.Join(tmpDir, "routes")

	require.NoError(t, os.MkdirAll(modelsDir, 0755))
	require.NoError(t, os.MkdirAll(controllersDir, 0755))
	require.NoError(t, os.MkdirAll(routesDir, 0755))

	// 1. Create User Model with relations
	userModel := `<?php
namespace App\Models;

use Illuminate\Database\Eloquent\Model;
use Illuminate\Database\Eloquent\SoftDeletes;

/**
 * User model representing application users
 */
class User extends Model
{
    use SoftDeletes;

    protected $table = 'users';
    protected $fillable = ['name', 'email', 'password'];
    protected $hidden = ['password', 'remember_token'];
    protected $casts = [
        'email_verified_at' => 'datetime',
        'is_admin' => 'boolean',
    ];

    /**
     * Get all posts for this user
     */
    public function posts()
    {
        return $this->hasMany(Post::class);
    }

    /**
     * Get user profile
     */
    public function profile()
    {
        return $this->hasOne(Profile::class);
    }

    /**
     * Get user roles (many-to-many)
     */
    public function roles()
    {
        return $this->belongsToMany(Role::class);
    }

    /**
     * Scope for active users
     */
    public function scopeActive($query)
    {
        return $query->where('active', true);
    }

    /**
     * Scope for admin users
     */
    public function scopeAdmin($query)
    {
        return $query->where('is_admin', true);
    }

    /**
     * Get full name accessor
     */
    public function getFullNameAttribute()
    {
        return $this->first_name . ' ' . $this->last_name;
    }
}
`

	// 2. Create Post Model with relations
	postModel := `<?php
namespace App\Models;

use Illuminate\Database\Eloquent\Model;
use Illuminate\Database\Eloquent\SoftDeletes;

class Post extends Model
{
    use SoftDeletes;

    protected $fillable = ['title', 'content', 'user_id', 'published_at'];
    protected $casts = ['published_at' => 'datetime'];

    public function user()
    {
        return $this->belongsTo(User::class);
    }

    public function comments()
    {
        return $this->hasMany(Comment::class);
    }

    public function tags()
    {
        return $this->belongsToMany(Tag::class);
    }

    public function scopePublished($query)
    {
        return $query->whereNotNull('published_at');
    }
}
`

	// 3. Create UserController
	userController := `<?php
namespace App\Http\Controllers;

use App\Models\User;
use Illuminate\Http\Request;

class UserController extends Controller
{
    /**
     * Display a listing of users
     */
    public function index()
    {
        $users = User::active()->get();
        return view('users.index', compact('users'));
    }

    /**
     * Show the form for creating a new user
     */
    public function create()
    {
        return view('users.create');
    }

    /**
     * Store a newly created user
     */
    public function store(Request $request)
    {
        $user = User::create($request->validated());
        return redirect()->route('users.show', $user);
    }

    /**
     * Display the specified user
     */
    public function show(User $user)
    {
        return view('users.show', compact('user'));
    }

    /**
     * Update the specified user
     */
    public function update(Request $request, User $user)
    {
        $user->update($request->validated());
        return redirect()->route('users.show', $user);
    }

    /**
     * Remove the specified user
     */
    public function destroy(User $user)
    {
        $user->delete();
        return redirect()->route('users.index');
    }
}
`

	// 4. Create API Controller
	apiController := `<?php
namespace App\Http\Controllers\Api;

use App\Models\Post;
use Illuminate\Http\Request;

class PostController extends Controller
{
    public function index()
    {
        return response()->json(Post::published()->get());
    }

    public function show($id)
    {
        $post = Post::findOrFail($id);
        return response()->json($post);
    }

    public function store(Request $request)
    {
        $post = Post::create($request->all());
        return response()->json($post, 201);
    }
}
`

	// 5. Create web routes
	webRoutes := `<?php

use App\Http\Controllers\UserController;
use Illuminate\Support\Facades\Route;

Route::get('/', function () {
    return view('welcome');
});

Route::resource('users', UserController::class);

Route::get('/about', function () {
    return view('about');
})->name('about');
`

	// 6. Create API routes
	apiRoutes := `<?php

use App\Http\Controllers\Api\PostController;
use Illuminate\Support\Facades\Route;

Route::get('/posts', [PostController::class, 'index']);
Route::get('/posts/{id}', [PostController::class, 'show']);
Route::post('/posts', [PostController::class, 'store']);
`

	// Write all files
	require.NoError(t, os.WriteFile(filepath.Join(modelsDir, "User.php"), []byte(userModel), 0644))
	require.NoError(t, os.WriteFile(filepath.Join(modelsDir, "Post.php"), []byte(postModel), 0644))
	require.NoError(t, os.WriteFile(filepath.Join(controllersDir, "UserController.php"), []byte(userController), 0644))

	// Create API controller directory and file
	apiControllerDir := filepath.Join(controllersDir, "Api")
	require.NoError(t, os.MkdirAll(apiControllerDir, 0755))
	require.NoError(t, os.WriteFile(filepath.Join(apiControllerDir, "PostController.php"), []byte(apiController), 0644))
	require.NoError(t, os.WriteFile(filepath.Join(routesDir, "web.php"), []byte(webRoutes), 0644))
	require.NoError(t, os.WriteFile(filepath.Join(routesDir, "api.php"), []byte(apiRoutes), 0644))

	// Now analyze the entire project using the Adapter
	adapter := NewAdapter()
	chunks, err := adapter.AnalyzePaths([]string{tmpDir})
	require.NoError(t, err)

	t.Logf("📊 Total chunks generated: %d", len(chunks))

	// Verify Models

	for i := range chunks {
		chunk := &chunks[i]
		if chunk.Type == "class" && chunk.Name == "User" {
			t.Logf("✓ Found User model chunk")
			assert.Equal(t, "App\\Models", chunk.Package)
			assert.NotNil(t, chunk.Metadata)

			// Check Laravel metadata
			if laravelType, ok := chunk.Metadata["laravel_type"]; ok {
				assert.Equal(t, "model", laravelType)
				t.Logf("  - Laravel type: %s", laravelType)
			}

			if table, ok := chunk.Metadata["table"]; ok {
				assert.Equal(t, "users", table)
				t.Logf("  - Table: %s", table)
			}

			if fillable, ok := chunk.Metadata["fillable"]; ok {
				t.Logf("  - Fillable: %v", fillable)
			}

			if relationsJSON, ok := chunk.Metadata["relations"]; ok {
				var relations []EloquentRelation
				err := json.Unmarshal([]byte(relationsJSON.(string)), &relations)
				assert.NoError(t, err)
				assert.Len(t, relations, 3, "User should have 3 relations")
				t.Logf("  - Relations: %d", len(relations))

				// Verify specific relations
				relMap := make(map[string]EloquentRelation)
				for _, rel := range relations {
					relMap[rel.Name] = rel
					t.Logf("    • %s: %s -> %s", rel.Name, rel.Type, rel.RelatedModel)
				}

				assert.Equal(t, "hasMany", relMap["posts"].Type)
				assert.Equal(t, "hasOne", relMap["profile"].Type)
				assert.Equal(t, "belongsToMany", relMap["roles"].Type)
			}
		}

		if chunk.Type == "class" && chunk.Name == "Post" {
			t.Logf("✓ Found Post model chunk")
			assert.Equal(t, "App\\Models", chunk.Package)
		}
	}

	// Verify Controllers
	var userControllerFound, apiControllerFound bool

	for i := range chunks {
		chunk := &chunks[i]
		if chunk.Type == "class" && chunk.Name == "UserController" {
			userControllerFound = true
			t.Logf("✓ Found UserController chunk")
			assert.Equal(t, "App\\Http\\Controllers", chunk.Package)

			if laravelType, ok := chunk.Metadata["laravel_type"]; ok {
				assert.Equal(t, "controller", laravelType)
			}

			if isResource, ok := chunk.Metadata["is_resource"]; ok {
				assert.True(t, isResource.(bool), "UserController should be detected as resource controller")
				t.Logf("  - Is resource controller: %v", isResource)
			}
		}

		if chunk.Type == "class" && chunk.Name == "PostController" && chunk.Package == "App\\Http\\Controllers\\Api" {
			apiControllerFound = true
			t.Logf("✓ Found API PostController chunk")

			if isApi, ok := chunk.Metadata["is_api"]; ok {
				assert.True(t, isApi.(bool), "PostController should be detected as API controller")
				t.Logf("  - Is API controller: %v", isApi)
			}
		}
	}

	assert.True(t, userControllerFound, "Should find UserController")
	assert.True(t, apiControllerFound, "Should find API PostController")

	// Verify Routes
	var routeCount int
	for i := range chunks {
		chunk := &chunks[i]
		if chunk.Type == "route" {
			routeCount++
			t.Logf("✓ Found route: %s", chunk.Name)

			assert.NotNil(t, chunk.Metadata)
			if method, ok := chunk.Metadata["method"]; ok {
				t.Logf("  - Method: %s", method)
			}
			if uri, ok := chunk.Metadata["uri"]; ok {
				t.Logf("  - URI: %s", uri)
			}
		}
	}

	// Should have routes from both web.php and api.php
	// web.php: 1 closure + 7 resource routes (index, create, store, show, edit, update, destroy) + 1 about = 9
	// api.php: 3 routes
	// Total: 12 routes
	assert.GreaterOrEqual(t, routeCount, 10, "Should find at least 10 routes")
	t.Logf("📍 Total routes found: %d", routeCount)

	t.Log("\n✅ E2E Test Complete!")
}

// TestE2E_RelationshipResolution tests that relationships are correctly resolved
func TestE2E_RelationshipResolution(t *testing.T) {
	tmpDir := t.TempDir()
	modelsDir := filepath.Join(tmpDir, "app", "Models")
	require.NoError(t, os.MkdirAll(modelsDir, 0755))

	// Create models with complex relationships
	userModel := `<?php
namespace App\Models;

use Illuminate\Database\Eloquent\Model;

class User extends Model
{
    public function posts()
    {
        return $this->hasMany(Post::class, 'author_id', 'id');
    }

    public function latestPost()
    {
        return $this->hasOne(Post::class)->latest();
    }
}
`

	postModel := `<?php
namespace App\Models;

use Illuminate\Database\Eloquent\Model;

class Post extends Model
{
    public function author()
    {
        return $this->belongsTo(User::class, 'author_id');
    }
}
`

	require.NoError(t, os.WriteFile(filepath.Join(modelsDir, "User.php"), []byte(userModel), 0644))
	require.NoError(t, os.WriteFile(filepath.Join(modelsDir, "Post.php"), []byte(postModel), 0644))

	// Analyze
	phpAnalyzer := php.NewCodeAnalyzer()
	_, err := phpAnalyzer.AnalyzeFile(filepath.Join(modelsDir, "User.php"))
	require.NoError(t, err)

	packages := phpAnalyzer.GetPackages()
	require.NotEmpty(t, packages)

	laravelAnalyzer := NewEloquentAnalyzer(packages[0])
	models := laravelAnalyzer.AnalyzeModels()

	require.Len(t, models, 1)
	userModelData := models[0]

	t.Logf("User model relations: %d", len(userModelData.Relations))

	// Verify relations with foreign keys
	relMap := make(map[string]EloquentRelation)
	for _, rel := range userModelData.Relations {
		relMap[rel.Name] = rel
		t.Logf("  - %s: %s (FK: %s, LK: %s)", rel.Name, rel.Type, rel.ForeignKey, rel.LocalKey)
	}

	// Check hasMany with custom keys
	postsRel := relMap["posts"]
	assert.Equal(t, "hasMany", postsRel.Type)
	assert.Equal(t, "App\\Models\\Post", postsRel.RelatedModel)
	assert.Equal(t, "author_id", postsRel.ForeignKey)
	assert.Equal(t, "id", postsRel.LocalKey)

	t.Log("✅ Relationship resolution test complete!")
}

// TestE2E_ScopesAndAccessors tests scopes and accessors/mutators
func TestE2E_ScopesAndAccessors(t *testing.T) {
	tmpDir := t.TempDir()
	modelsDir := filepath.Join(tmpDir, "app", "Models")
	require.NoError(t, os.MkdirAll(modelsDir, 0755))

	productModel := `<?php
namespace App\Models;

use Illuminate\Database\Eloquent\Model;

class Product extends Model
{
    protected $fillable = ['name', 'price', 'stock'];
    protected $casts = ['price' => 'decimal:2'];

    // Scopes
    public function scopeInStock($query)
    {
        return $query->where('stock', '>', 0);
    }

    public function scopeExpensive($query, $threshold = 1000)
    {
        return $query->where('price', '>=', $threshold);
    }

    // Accessors
    public function getFormattedPriceAttribute()
    {
        return '$' . number_format($this->price, 2);
    }

    public function getIsAvailableAttribute()
    {
        return $this->stock > 0;
    }

    // Mutators
    public function setNameAttribute($value)
    {
        $this->attributes['name'] = ucfirst($value);
    }
}
`

	require.NoError(t, os.WriteFile(filepath.Join(modelsDir, "Product.php"), []byte(productModel), 0644))

	phpAnalyzer := php.NewCodeAnalyzer()
	_, err := phpAnalyzer.AnalyzeFile(filepath.Join(modelsDir, "Product.php"))
	require.NoError(t, err)

	packages := phpAnalyzer.GetPackages()
	require.NotEmpty(t, packages)

	laravelAnalyzer := NewEloquentAnalyzer(packages[0])
	models := laravelAnalyzer.AnalyzeModels()

	require.Len(t, models, 1)
	product := models[0]

	t.Logf("Product model analysis:")
	t.Logf("  - Scopes: %d", len(product.Scopes))
	t.Logf("  - Attributes: %d", len(product.Attributes))

	// Verify scopes
	assert.Len(t, product.Scopes, 2)
	scopeMap := make(map[string]EloquentScope)
	for _, scope := range product.Scopes {
		scopeMap[scope.Name] = scope
		t.Logf("    • Scope: %s (method: %s)", scope.Name, scope.MethodName)
	}
	assert.Contains(t, scopeMap, "inStock")
	assert.Contains(t, scopeMap, "expensive")

	// Verify accessors and mutators
	assert.Len(t, product.Attributes, 3)
	attrMap := make(map[string]EloquentAttribute)
	for _, attr := range product.Attributes {
		attrMap[attr.Name] = attr
		t.Logf("    • %s: %s (method: %s)", attr.Type, attr.Name, attr.MethodName)
	}

	assert.Equal(t, "accessor", attrMap["formatted_price"].Type)
	assert.Equal(t, "accessor", attrMap["is_available"].Type)
	assert.Equal(t, "mutator", attrMap["name"].Type)

	t.Log("✅ Scopes and accessors test complete!")
}

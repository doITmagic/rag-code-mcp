package laravel

import (
	"os"
	"testing"

	"github.com/doITmagic/coderag-mcp/internal/coderag/analyzers/php"
	"github.com/stretchr/testify/assert"
)

func TestControllerAnalyzer_ResourceController(t *testing.T) {
	phpCode := `<?php
namespace App\Http\Controllers;

use App\Models\Post;
use Illuminate\Http\Request;

/**
 * PostController handles blog post operations
 */
class PostController extends Controller
{
    /**
     * Display a listing of posts
     */
    public function index()
    {
        return view('posts.index', ['posts' => Post::all()]);
    }

    /**
     * Show the form for creating a new post
     */
    public function create()
    {
        return view('posts.create');
    }

    /**
     * Store a newly created post in storage
     */
    public function store(Request $request)
    {
        $post = Post::create($request->validated());
        return redirect()->route('posts.show', $post);
    }

    /**
     * Display the specified post
     */
    public function show(Post $post)
    {
        return view('posts.show', compact('post'));
    }

    /**
     * Show the form for editing the specified post
     */
    public function edit(Post $post)
    {
        return view('posts.edit', compact('post'));
    }

    /**
     * Update the specified post in storage
     */
    public function update(Request $request, Post $post)
    {
        $post->update($request->validated());
        return redirect()->route('posts.show', $post);
    }

    /**
     * Remove the specified post from storage
     */
    public function destroy(Post $post)
    {
        $post->delete();
        return redirect()->route('posts.index');
    }
}
`

	// Parse PHP code
	pkgInfo := parsePhpCode(t, phpCode, "test.php")

	// Analyze controllers
	analyzer := NewControllerAnalyzer(pkgInfo)
	controllers := analyzer.AnalyzeControllers()

	// Verify results
	assert.Len(t, controllers, 1, "Should detect 1 controller")

	controller := controllers[0]
	assert.Equal(t, "PostController", controller.ClassName)
	assert.Equal(t, "App\\Http\\Controllers", controller.Namespace)
	assert.Equal(t, "App\\Http\\Controllers\\PostController", controller.FullName)
	assert.Equal(t, "PostController handles blog post operations", controller.Description)
	assert.Equal(t, "Controller", controller.BaseController)
	assert.False(t, controller.IsApi, "Should not be API controller")
	assert.True(t, controller.IsResource, "Should be resource controller (7 CRUD methods)")

	// Check actions
	assert.Len(t, controller.Actions, 7, "Should have 7 resource actions")

	// Verify specific actions
	actionMap := make(map[string]ControllerAction)
	for _, action := range controller.Actions {
		actionMap[action.Name] = action
	}

	// Test index action
	indexAction, exists := actionMap["index"]
	assert.True(t, exists, "Should have index action")
	assert.Equal(t, "Display a listing of posts", indexAction.Description)
	assert.Equal(t, []string{"GET"}, indexAction.HttpMethods)
	assert.Empty(t, indexAction.Parameters)

	// Test store action
	storeAction, exists := actionMap["store"]
	assert.True(t, exists, "Should have store action")
	assert.Equal(t, "Store a newly created post in storage", storeAction.Description)
	assert.Equal(t, []string{"POST"}, storeAction.HttpMethods)
	assert.Equal(t, []string{"request"}, storeAction.Parameters)

	// Test update action
	updateAction, exists := actionMap["update"]
	assert.True(t, exists, "Should have update action")
	assert.Equal(t, []string{"PUT", "PATCH"}, updateAction.HttpMethods)
	assert.Equal(t, []string{"request", "post"}, updateAction.Parameters)

	// Test destroy action
	destroyAction, exists := actionMap["destroy"]
	assert.True(t, exists, "Should have destroy action")
	assert.Equal(t, []string{"DELETE"}, destroyAction.HttpMethods)
	assert.Equal(t, []string{"post"}, destroyAction.Parameters)
}

func TestControllerAnalyzer_ApiController(t *testing.T) {
	phpCode := `<?php
namespace App\Http\Controllers\Api;

use App\Models\User;
use Illuminate\Http\Request;

/**
 * API controller for user management
 */
class UserController extends Controller
{
    /**
     * Get all users
     */
    public function index()
    {
        return response()->json(User::all());
    }

    /**
     * Get a specific user
     */
    public function show($id)
    {
        return response()->json(User::findOrFail($id));
    }

    /**
     * Create a new user
     */
    public function store(Request $request)
    {
        $user = User::create($request->all());
        return response()->json($user, 201);
    }
}
`

	// Parse PHP code
	pkgInfo := parsePhpCode(t, phpCode, "test.php")

	// Analyze controllers
	analyzer := NewControllerAnalyzer(pkgInfo)
	controllers := analyzer.AnalyzeControllers()

	// Verify results
	assert.Len(t, controllers, 1, "Should detect 1 controller")

	controller := controllers[0]
	assert.Equal(t, "UserController", controller.ClassName)
	assert.Equal(t, "App\\Http\\Controllers\\Api", controller.Namespace)
	assert.True(t, controller.IsApi, "Should be API controller (namespace contains Api)")
	assert.False(t, controller.IsResource, "Should not be resource controller (only 3 methods)")

	// Check actions
	assert.Len(t, controller.Actions, 3)

	actionMap := make(map[string]ControllerAction)
	for _, action := range controller.Actions {
		actionMap[action.Name] = action
	}

	// Verify HTTP methods
	assert.Equal(t, []string{"GET"}, actionMap["index"].HttpMethods)
	assert.Equal(t, []string{"GET"}, actionMap["show"].HttpMethods)
	assert.Equal(t, []string{"POST"}, actionMap["store"].HttpMethods)
}

func TestControllerAnalyzer_CustomActions(t *testing.T) {
	phpCode := `<?php
namespace App\Http\Controllers;

class ProductController extends Controller
{
    public function getLatest()
    {
        return Product::latest()->get();
    }

    public function createBulk(Request $request)
    {
        return Product::insert($request->all());
    }

    public function updateStock(Product $product)
    {
        $product->increment('stock');
    }

    public function deleteExpired()
    {
        Product::where('expires_at', '<', now())->delete();
    }

    // This should be skipped (not public)
    protected function helper()
    {
        return true;
    }

    // This should be skipped (magic method)
    public function __construct()
    {
    }
}
`

	// Parse PHP code
	pkgInfo := parsePhpCode(t, phpCode, "test.php")

	// Analyze controllers
	analyzer := NewControllerAnalyzer(pkgInfo)
	controllers := analyzer.AnalyzeControllers()

	assert.Len(t, controllers, 1)

	controller := controllers[0]
	assert.Len(t, controller.Actions, 4, "Should have 4 public actions (skip protected and magic methods)")

	actionMap := make(map[string]ControllerAction)
	for _, action := range controller.Actions {
		actionMap[action.Name] = action
	}

	// Verify HTTP method inference from action names
	assert.Equal(t, []string{"GET"}, actionMap["getLatest"].HttpMethods, "getLatest should be GET")
	assert.Equal(t, []string{"POST"}, actionMap["createBulk"].HttpMethods, "createBulk should be POST")
	assert.Equal(t, []string{"PUT", "PATCH"}, actionMap["updateStock"].HttpMethods, "updateStock should be PUT/PATCH")
	assert.Equal(t, []string{"DELETE"}, actionMap["deleteExpired"].HttpMethods, "deleteExpired should be DELETE")

	// Verify helper and __construct are not in actions
	_, hasHelper := actionMap["helper"]
	_, hasConstruct := actionMap["__construct"]
	assert.False(t, hasHelper, "Should not include protected methods")
	assert.False(t, hasConstruct, "Should not include magic methods")
}

func TestControllerAnalyzer_NonController(t *testing.T) {
	phpCode := `<?php
namespace App\Services;

class UserService
{
    public function createUser($data)
    {
        return User::create($data);
    }
}
`

	// Parse PHP code
	pkgInfo := parsePhpCode(t, phpCode, "test.php")

	// Analyze controllers
	analyzer := NewControllerAnalyzer(pkgInfo)
	controllers := analyzer.AnalyzeControllers()

	assert.Empty(t, controllers, "Should not detect non-controller classes")
}

// Helper function to parse PHP code into PackageInfo
func parsePhpCode(t *testing.T, phpCode string, filename string) *php.PackageInfo {
	// Create temporary file for AnalyzeFile to work
	tmpFile := "/tmp/" + filename
	err := os.WriteFile(tmpFile, []byte(phpCode), 0644)
	assert.NoError(t, err)
	defer os.Remove(tmpFile)

	// Use PHP analyzer to extract package info
	analyzer := php.NewCodeAnalyzer()
	_, err = analyzer.AnalyzeFile(tmpFile)
	assert.NoError(t, err)

	packages := analyzer.GetPackages()
	assert.NotEmpty(t, packages, "Should have at least one package")

	return packages[0]
}

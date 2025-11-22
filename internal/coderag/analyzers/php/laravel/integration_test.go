package laravel

import (
	"os"
	"testing"

	"github.com/doITmagic/coderag-mcp/internal/coderag/analyzers/php"
	"github.com/stretchr/testify/assert"
)

func TestLaravelIntegration_FullProject(t *testing.T) {
	// Simulate a Laravel project with Model and Controller
	modelCode := `<?php
namespace App\Models;

use Illuminate\Database\Eloquent\Model;
use Illuminate\Database\Eloquent\SoftDeletes;

class Post extends Model
{
    use SoftDeletes;

    protected $table = 'posts';
    protected $fillable = ['title', 'content', 'user_id'];
    protected $casts = ['published_at' => 'datetime'];

    public function user()
    {
        return $this->belongsTo(User::class);
    }

    public function comments()
    {
        return $this->hasMany(Comment::class);
    }

    public function scopePublished($query)
    {
        return $query->whereNotNull('published_at');
    }
}
`

	controllerCode := `<?php
namespace App\Http\Controllers;

use App\Models\Post;
use Illuminate\Http\Request;

class PostController extends Controller
{
    public function index()
    {
        return view('posts.index', ['posts' => Post::published()->get()]);
    }

    public function show(Post $post)
    {
        return view('posts.show', compact('post'));
    }

    public function store(Request $request)
    {
        $post = Post::create($request->validated());
        return redirect()->route('posts.show', $post);
    }

    public function destroy(Post $post)
    {
        $post->delete();
        return redirect()->route('posts.index');
    }
}
`

	// Parse model
	modelPkgInfo := parsePhpCode(t, modelCode, "Post.php")

	// Parse controller
	controllerPkgInfo := parsePhpCode(t, controllerCode, "PostController.php")

	// Test Laravel detection on model
	modelAnalyzer := php.NewCodeAnalyzer()
	tmpModelFile := "/tmp/Post_integration_test.php"
	err := os.WriteFile(tmpModelFile, []byte(modelCode), 0644)
	assert.NoError(t, err)
	defer os.Remove(tmpModelFile)

	_, err = modelAnalyzer.AnalyzeFile(tmpModelFile)
	assert.NoError(t, err)
	assert.True(t, modelAnalyzer.IsLaravelProject(), "Should detect Laravel project from Model")

	// Analyze model with Laravel analyzer
	laravelModelAnalyzer := NewAnalyzer(modelPkgInfo)
	laravelInfo := laravelModelAnalyzer.Analyze()

	// Verify model analysis
	assert.Len(t, laravelInfo.Models, 1, "Should detect 1 Eloquent model")

	model := laravelInfo.Models[0]
	assert.Equal(t, "Post", model.ClassName)
	assert.Equal(t, "App\\Models", model.Namespace)
	assert.Equal(t, "posts", model.Table)
	assert.True(t, model.SoftDeletes)
	assert.Equal(t, []string{"title", "content", "user_id"}, model.Fillable)
	assert.Len(t, model.Relations, 2, "Should detect 2 relations")
	assert.Len(t, model.Scopes, 1, "Should detect 1 scope")

	// Verify controller analysis
	laravelControllerAnalyzer := NewAnalyzer(controllerPkgInfo)
	controllerInfo := laravelControllerAnalyzer.Analyze()

	assert.Len(t, controllerInfo.Controllers, 1, "Should detect 1 controller")

	controller := controllerInfo.Controllers[0]
	assert.Equal(t, "PostController", controller.ClassName)
	assert.Equal(t, "App\\Http\\Controllers", controller.Namespace)
	assert.False(t, controller.IsApi)
	assert.Len(t, controller.Actions, 4, "Should have 4 actions")

	// Verify action HTTP methods
	actionMap := make(map[string]ControllerAction)
	for _, action := range controller.Actions {
		actionMap[action.Name] = action
	}

	assert.Equal(t, []string{"GET"}, actionMap["index"].HttpMethods)
	assert.Equal(t, []string{"GET"}, actionMap["show"].HttpMethods)
	assert.Equal(t, []string{"POST"}, actionMap["store"].HttpMethods)
	assert.Equal(t, []string{"DELETE"}, actionMap["destroy"].HttpMethods)
}

func TestLaravelIntegration_DetectNonLaravel(t *testing.T) {
	// Plain PHP code without Laravel
	phpCode := `<?php
namespace App\Services;

class DataProcessor
{
    public function process($data)
    {
        return array_map('strtoupper', $data);
    }
}
`

	tmpFile := "/tmp/DataProcessor_test.php"
	err := os.WriteFile(tmpFile, []byte(phpCode), 0644)
	assert.NoError(t, err)
	defer os.Remove(tmpFile)

	analyzer := php.NewCodeAnalyzer()
	_, err = analyzer.AnalyzeFile(tmpFile)
	assert.NoError(t, err)

	assert.False(t, analyzer.IsLaravelProject(), "Should NOT detect Laravel project")
}

func TestLaravelIntegration_ApiController(t *testing.T) {
	apiControllerCode := `<?php
namespace App\Http\Controllers\Api;

use App\Models\User;
use Illuminate\Http\Request;

class UserController extends Controller
{
    public function index()
    {
        return response()->json(User::all());
    }

    public function show($id)
    {
        return response()->json(User::findOrFail($id));
    }

    public function store(Request $request)
    {
        $user = User::create($request->all());
        return response()->json($user, 201);
    }
}
`

	pkgInfo := parsePhpCode(t, apiControllerCode, "UserController.php")

	// Analyze with Laravel analyzer
	laravelAnalyzer := NewAnalyzer(pkgInfo)
	info := laravelAnalyzer.Analyze()

	assert.Len(t, info.Controllers, 1)

	controller := info.Controllers[0]
	assert.True(t, controller.IsApi, "Should detect API controller")
	assert.Equal(t, "App\\Http\\Controllers\\Api", controller.Namespace)
	assert.Len(t, controller.Actions, 3)
}

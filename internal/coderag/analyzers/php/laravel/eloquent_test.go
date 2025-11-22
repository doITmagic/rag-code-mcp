package laravel

import (
	"os"
	"testing"

	"github.com/doITmagic/coderag-mcp/internal/coderag/analyzers/php"
	"github.com/stretchr/testify/assert"
)

func TestEloquentAnalyzer_ExtractProperties(t *testing.T) {
	phpCode := `<?php
namespace App\Models;

use Illuminate\Database\Eloquent\Model;
use Illuminate\Database\Eloquent\SoftDeletes;

class User extends Model
{
    use SoftDeletes;

    protected $table = 'users';
    protected $primaryKey = 'id';
    
    protected $fillable = [
        'name',
        'email',
        'password',
    ];
    
    protected $guarded = ['admin'];
    
    protected $casts = [
        'is_admin' => 'boolean',
        'age' => 'integer',
        'metadata' => 'array',
    ];
    
    public function posts()
    {
        return $this->hasMany(Post::class, 'user_id');
    }
    
    public function profile()
    {
        return $this->hasOne(Profile::class);
    }
    
    public function roles()
    {
        return $this->belongsToMany(Role::class);
    }
    
    public function scopeActive($query)
    {
        return $query->where('active', true);
    }
    
    public function scopeAdmin($query)
    {
        return $query->where('is_admin', true);
    }
    
    public function getFullNameAttribute()
    {
        return $this->first_name . ' ' . $this->last_name;
    }
    
    public function setPasswordAttribute($value)
    {
        $this->attributes['password'] = bcrypt($value);
    }
}
`

	// Create temp file
	tmpFile := "/tmp/test_user_model.php"
	err := os.WriteFile(tmpFile, []byte(phpCode), 0644)
	assert.NoError(t, err)
	defer os.Remove(tmpFile)

	// Analyze PHP file
	analyzer := php.NewCodeAnalyzer()
	_, err = analyzer.AnalyzeFile(tmpFile)
	assert.NoError(t, err)

	// Get package info
	packages := analyzer.GetPackages()
	assert.Len(t, packages, 1)

	pkg := packages[0]
	assert.Equal(t, "App\\Models", pkg.Namespace)
	assert.Len(t, pkg.Classes, 1)

	// Now run Laravel analyzer
	laravelAnalyzer := NewEloquentAnalyzer(pkg)
	models := laravelAnalyzer.AnalyzeModels()

	assert.Len(t, models, 1, "Should detect one Eloquent model")

	model := models[0]

	// Test basic info
	assert.Equal(t, "User", model.ClassName)
	assert.Equal(t, "App\\Models\\User", model.FullName)
	assert.Equal(t, "users", model.Table)
	assert.Equal(t, "id", model.PrimaryKey)
	assert.True(t, model.SoftDeletes, "Should detect SoftDeletes trait")

	// Test $fillable
	assert.ElementsMatch(t, []string{"name", "email", "password"}, model.Fillable)

	// Test $guarded
	assert.ElementsMatch(t, []string{"admin"}, model.Guarded)

	// Test $casts
	assert.Equal(t, "boolean", model.Casts["is_admin"])
	assert.Equal(t, "integer", model.Casts["age"])
	assert.Equal(t, "array", model.Casts["metadata"])

	// Test relations
	assert.Len(t, model.Relations, 3, "Should detect 3 relations")

	// Check hasMany relation
	var postsRelation *EloquentRelation
	for i := range model.Relations {
		if model.Relations[i].Name == "posts" {
			postsRelation = &model.Relations[i]
			break
		}
	}
	assert.NotNil(t, postsRelation)
	assert.Equal(t, "hasMany", postsRelation.Type)
	assert.Equal(t, "App\\Models\\Post", postsRelation.RelatedModel)
	assert.Equal(t, "user_id", postsRelation.ForeignKey)

	// Check hasOne relation
	var profileRelation *EloquentRelation
	for i := range model.Relations {
		if model.Relations[i].Name == "profile" {
			profileRelation = &model.Relations[i]
			break
		}
	}
	assert.NotNil(t, profileRelation)
	assert.Equal(t, "hasOne", profileRelation.Type)
	assert.Equal(t, "App\\Models\\Profile", profileRelation.RelatedModel)

	// Check belongsToMany relation
	var rolesRelation *EloquentRelation
	for i := range model.Relations {
		if model.Relations[i].Name == "roles" {
			rolesRelation = &model.Relations[i]
			break
		}
	}
	assert.NotNil(t, rolesRelation)
	assert.Equal(t, "belongsToMany", rolesRelation.Type)
	assert.Equal(t, "App\\Models\\Role", rolesRelation.RelatedModel)

	// Test scopes
	assert.Len(t, model.Scopes, 2)

	var activeScope *EloquentScope
	for i := range model.Scopes {
		if model.Scopes[i].Name == "active" {
			activeScope = &model.Scopes[i]
			break
		}
	}
	assert.NotNil(t, activeScope)
	assert.Equal(t, "scopeActive", activeScope.MethodName)

	// Test attributes (accessors/mutators)
	assert.Len(t, model.Attributes, 2)

	var fullNameAttr *EloquentAttribute
	for i := range model.Attributes {
		if model.Attributes[i].Name == "full_name" {
			fullNameAttr = &model.Attributes[i]
			break
		}
	}
	assert.NotNil(t, fullNameAttr)
	assert.Equal(t, "accessor", fullNameAttr.Type)
	assert.Equal(t, "getFullNameAttribute", fullNameAttr.MethodName)

	var passwordAttr *EloquentAttribute
	for i := range model.Attributes {
		if model.Attributes[i].Name == "password" {
			passwordAttr = &model.Attributes[i]
			break
		}
	}
	assert.NotNil(t, passwordAttr)
	assert.Equal(t, "mutator", passwordAttr.Type)
	assert.Equal(t, "setPasswordAttribute", passwordAttr.MethodName)
}

func TestEloquentAnalyzer_NonEloquentClass(t *testing.T) {
	phpCode := `<?php
namespace App\Services;

class UserService
{
    public function createUser($data)
    {
        // Service logic
    }
}
`

	tmpFile := "/tmp/test_user_service.php"
	err := os.WriteFile(tmpFile, []byte(phpCode), 0644)
	assert.NoError(t, err)
	defer os.Remove(tmpFile)

	analyzer := php.NewCodeAnalyzer()
	_, err = analyzer.AnalyzeFile(tmpFile)
	assert.NoError(t, err)

	packages := analyzer.GetPackages()
	assert.Len(t, packages, 1)

	pkg := packages[0]
	laravelAnalyzer := NewEloquentAnalyzer(pkg)
	models := laravelAnalyzer.AnalyzeModels()

	assert.Len(t, models, 0, "Should not detect non-Eloquent classes as models")
}

func TestEloquentAnalyzer_MinimalModel(t *testing.T) {
	phpCode := `<?php
namespace App\Models;

use Illuminate\Database\Eloquent\Model;

class Post extends Model
{
    // Minimal model with no explicit properties
}
`

	tmpFile := "/tmp/test_post_model.php"
	err := os.WriteFile(tmpFile, []byte(phpCode), 0644)
	assert.NoError(t, err)
	defer os.Remove(tmpFile)

	analyzer := php.NewCodeAnalyzer()
	_, err = analyzer.AnalyzeFile(tmpFile)
	assert.NoError(t, err)

	packages := analyzer.GetPackages()
	assert.Len(t, packages, 1)

	pkg := packages[0]
	laravelAnalyzer := NewEloquentAnalyzer(pkg)
	models := laravelAnalyzer.AnalyzeModels()

	assert.Len(t, models, 1)
	model := models[0]

	assert.Equal(t, "Post", model.ClassName)
	assert.False(t, model.SoftDeletes)
	assert.Empty(t, model.Fillable)
	assert.Empty(t, model.Guarded)
	assert.Empty(t, model.Casts)
	assert.Empty(t, model.Relations)
	assert.Empty(t, model.Scopes)
}

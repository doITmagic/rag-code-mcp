package laravel

import (
	"testing"

	"github.com/VKCOM/php-parser/pkg/ast"
	"github.com/VKCOM/php-parser/pkg/conf"
	"github.com/VKCOM/php-parser/pkg/errors"
	"github.com/VKCOM/php-parser/pkg/parser"
	"github.com/VKCOM/php-parser/pkg/version"
	"github.com/stretchr/testify/assert"
)

func TestASTPropertyExtractor_ExtractMethodCalls(t *testing.T) {
	phpCode := `<?php
class User {
    public function posts() {
        return $this->hasMany(Post::class, 'user_id');
    }
    
    public function profile() {
        return $this->hasOne(Profile::class);
    }
    
    public function roles() {
        return $this->belongsToMany(Role::class);
    }
}
`

	// Parse PHP code
	var parserErrors []*errors.Error
	rootNode, err := parser.Parse([]byte(phpCode), conf.Config{
		Version: &version.Version{Major: 8, Minor: 0},
		ErrorHandlerFunc: func(e *errors.Error) {
			parserErrors = append(parserErrors, e)
		},
	})

	assert.NoError(t, err)
	assert.Empty(t, parserErrors)

	// Find the User class
	var classNode *ast.StmtClass
	for _, stmt := range rootNode.(*ast.Root).Stmts {
		if class, ok := stmt.(*ast.StmtClass); ok {
			if id, ok := class.Name.(*ast.Identifier); ok {
				if string(id.Value) == "User" {
					classNode = class
					break
				}
			}
		}
	}

	assert.NotNil(t, classNode, "User class not found")

	extractor := &ASTPropertyExtractor{}

	// Test posts() method
	var postsMethod *ast.StmtClassMethod
	for _, stmt := range classNode.Stmts {
		if method, ok := stmt.(*ast.StmtClassMethod); ok {
			if id, ok := method.Name.(*ast.Identifier); ok {
				if string(id.Value) == "posts" {
					postsMethod = method
					break
				}
			}
		}
	}

	assert.NotNil(t, postsMethod, "posts method not found")

	calls := extractor.ExtractMethodCalls(postsMethod)
	t.Logf("Method calls in posts(): %+v", calls)

	assert.Len(t, calls, 1, "Should find 1 method call")
	assert.Equal(t, "this", calls[0].Object)
	assert.Equal(t, "hasMany", calls[0].Method)
	assert.Len(t, calls[0].Args, 2, "Should have 2 arguments")
	assert.Equal(t, "Post", calls[0].Args[0], "First arg should be Post")
	assert.Equal(t, "user_id", calls[0].Args[1], "Second arg should be user_id")
}

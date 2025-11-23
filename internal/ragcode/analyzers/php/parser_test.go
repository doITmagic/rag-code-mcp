package php

import (
	"testing"

	"github.com/VKCOM/php-parser/pkg/ast"
	"github.com/VKCOM/php-parser/pkg/conf"
	"github.com/VKCOM/php-parser/pkg/errors"
	"github.com/VKCOM/php-parser/pkg/parser"
	"github.com/VKCOM/php-parser/pkg/version"
	"github.com/VKCOM/php-parser/pkg/visitor"
	"github.com/VKCOM/php-parser/pkg/visitor/traverser"
	"github.com/stretchr/testify/require"
)

// Test 1: Verify that the parser works correctly
func TestBasicParsing(t *testing.T) {
	src := []byte(`<?php
namespace App;

class MyClass {
    public function hello() {
        echo "test";
    }
}
`)

	var parserErrors []*errors.Error
	rootNode, err := parser.Parse(src, conf.Config{
		Version: &version.Version{Major: 8, Minor: 0},
		ErrorHandlerFunc: func(e *errors.Error) {
			parserErrors = append(parserErrors, e)
		},
	})

	require.NoError(t, err)
	require.NotNil(t, rootNode)
	require.Empty(t, parserErrors, "There should be no parsing errors")
}

// Test 2: Visitor pattern - extract class names
type ClassCollector struct {
	visitor.Null
	classes []string
}

func (v *ClassCollector) StmtClass(n *ast.StmtClass) {
	if n.Name != nil {
		if ident, ok := n.Name.(*ast.Identifier); ok {
			v.classes = append(v.classes, string(ident.Value))
		}
	}
}

func TestClassExtraction(t *testing.T) {
	src := []byte(`<?php
namespace App;

class FirstClass {
}

class SecondClass {
}
`)

	rootNode, err := parser.Parse(src, conf.Config{
		Version: &version.Version{Major: 8, Minor: 0},
	})
	require.NoError(t, err)

	// Collect classes
	collector := &ClassCollector{}
	traverser.NewTraverser(collector).Traverse(rootNode)

	require.Equal(t, []string{"FirstClass", "SecondClass"}, collector.classes)
}

// Test 3: Extract global functions
type FunctionCollector struct {
	visitor.Null
	functions []string
}

func (v *FunctionCollector) StmtFunction(n *ast.StmtFunction) {
	if n.Name != nil {
		if ident, ok := n.Name.(*ast.Identifier); ok {
			v.functions = append(v.functions, string(ident.Value))
		}
	}
}

func TestFunctionExtraction(t *testing.T) {
	src := []byte(`<?php
function globalFunc() {
    return true;
}

function anotherFunc($param) {
    return $param;
}
`)

	rootNode, err := parser.Parse(src, conf.Config{
		Version: &version.Version{Major: 8, Minor: 0},
	})
	require.NoError(t, err)

	collector := &FunctionCollector{}
	traverser.NewTraverser(collector).Traverse(rootNode)

	require.Equal(t, []string{"globalFunc", "anotherFunc"}, collector.functions)
}

// Test 4: Extract methods from classes
type MethodCollector struct {
	visitor.Null
	methods []string
}

func (v *MethodCollector) StmtClassMethod(n *ast.StmtClassMethod) {
	if n.Name != nil {
		if ident, ok := n.Name.(*ast.Identifier); ok {
			v.methods = append(v.methods, string(ident.Value))
		}
	}
}

func TestMethodExtraction(t *testing.T) {
	src := []byte(`<?php
class MyClass {
    public function methodOne() {
    }
    
    private function methodTwo() {
    }
}
`)

	rootNode, err := parser.Parse(src, conf.Config{
		Version: &version.Version{Major: 8, Minor: 0},
	})
	require.NoError(t, err)

	collector := &MethodCollector{}
	traverser.NewTraverser(collector).Traverse(rootNode)

	require.Equal(t, []string{"methodOne", "methodTwo"}, collector.methods)
}

// Test 5: Verify namespace extraction
type NamespaceCollector struct {
	visitor.Null
	namespaces []string
}

func (v *NamespaceCollector) StmtNamespace(n *ast.StmtNamespace) {
	if n.Name != nil {
		// Namespace can be Name, NameFullyQualified, etc.
		ns := extractNamespaceName(n.Name)
		if ns != "" {
			v.namespaces = append(v.namespaces, ns)
		}
	}
}

func extractNamespaceName(node ast.Vertex) string {
	switch n := node.(type) {
	case *ast.Name:
		// Simple name parts
		parts := make([]string, 0, len(n.Parts))
		for _, part := range n.Parts {
			if ident, ok := part.(*ast.NamePart); ok {
				parts = append(parts, string(ident.Value))
			}
		}
		result := ""
		for i, p := range parts {
			if i > 0 {
				result += "\\"
			}
			result += p
		}
		return result
	}
	return ""
}

func TestNamespaceExtraction(t *testing.T) {
	src := []byte(`<?php
namespace App\Controllers;

class HomeController {
}
`)

	rootNode, err := parser.Parse(src, conf.Config{
		Version: &version.Version{Major: 8, Minor: 0},
	})
	require.NoError(t, err)

	collector := &NamespaceCollector{}
	traverser.NewTraverser(collector).Traverse(rootNode)

	require.Equal(t, []string{"App\\Controllers"}, collector.namespaces)
}

package php

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestPHPAnalyzer(t *testing.T) {
	tmpDir := t.TempDir()
	
	code := `<?php
namespace App\Services;

interface Greeter {
    public function greet(string $name): string;
}

class MyGreeter implements Greeter {
    public function greet(string $name): string {
        return "Hello, " . $name;
    }
}

function global_greet() {
    return "Hi";
}
`
	filePath := filepath.Join(tmpDir, "test.php")
	if err := os.WriteFile(filePath, []byte(code), 0644); err != nil {
		t.Fatalf("failed to write test file: %v", err)
	}

	analyzer := NewAnalyzer()
	ctx := context.Background()

	res, err := analyzer.Analyze(ctx, filePath)
	if err != nil {
		t.Fatalf("Analyze failed: %v", err)
	}

	foundNamespace := false
	foundInterface := false
	foundClass := false
	foundMethod := false
	foundFunction := false

	for _, sym := range res.Symbols {
		if sym.Package == "App\\Services" {
			foundNamespace = true
		}
		switch sym.Name {
		case "Greeter":
			foundInterface = true
		case "MyGreeter":
			foundClass = true
		case "greet":
			foundMethod = true
		case "global_greet":
			foundFunction = true
		}
	}

	if !foundNamespace {
		t.Error("Namespace App\\Services not correctly extracted")
	}
	if !foundInterface {
		t.Error("Interface Greeter not found")
	}
	if !foundClass {
		t.Error("Class MyGreeter not found")
	}
	if !foundMethod {
		t.Error("Method greet not found")
	}
	if !foundFunction {
		t.Error("Function global_greet not found")
	}
}

func TestPHPAnalyzer_HighFidelity(t *testing.T) {
	tmpDir := t.TempDir()
	
	code := `<?php
/**
 * Class docstring
 */
class MyController extends BaseController {
    /**
     * Method docstring
     */
    public function index() {
        Route::get('/users', 'UsersController@index');
    }
}
`
	filePath := filepath.Join(tmpDir, "routes.php")
	if err := os.WriteFile(filePath, []byte(code), 0644); err != nil {
		t.Fatalf("failed to write test file: %v", err)
	}

	analyzer := NewAnalyzer()
	ctx := context.Background()

	res, err := analyzer.Analyze(ctx, filePath)
	if err != nil {
		t.Fatalf("Analyze failed: %v", err)
	}

	foundRoute := false
	foundClassDoc := false
	foundMethodDoc := false

	for _, sym := range res.Symbols {
		switch sym.Name {
		case "MyController":
			if sym.Docstring == "Class docstring" {
				foundClassDoc = true
			}
			if sym.Metadata["extends"] != "BaseController" {
				t.Errorf("Expected extends BaseController, got %v", sym.Metadata["extends"])
			}
		case "index":
			if sym.Docstring == "Method docstring" {
				foundMethodDoc = true
			}
		case "/users":
			foundRoute = true
			if sym.Metadata["php_kind"] != "route" {
				t.Errorf("Expected php_kind route, got %v", sym.Metadata["php_kind"])
			}
		}
	}

	if !foundRoute { t.Error("Laravel Route not found") }
	if !foundClassDoc { t.Error("Class PHPDoc not found") }
	if !foundMethodDoc { t.Error("Method PHPDoc not found") }
}

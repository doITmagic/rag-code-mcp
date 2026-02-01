package php

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/doITmagic/rag-code-mcp/internal/codetypes"
	"github.com/stretchr/testify/require"
)

func TestCodeAnalyzer_BasicClassExtraction(t *testing.T) {
	// Create temp PHP file
	tmpDir := t.TempDir()
	phpFile := filepath.Join(tmpDir, "test.php")

	phpCode := `<?php
namespace App\Models;

class User {
    public function getName() {
        return "test";
    }
    
    private function setName($name) {
        $this->name = $name;
    }
}
`

	err := os.WriteFile(phpFile, []byte(phpCode), 0644)
	require.NoError(t, err)

	// Analyze
	analyzer := NewCodeAnalyzer()
	chunks, err := analyzer.AnalyzeFile(phpFile)
	require.NoError(t, err)

	// Verify chunks
	require.NotEmpty(t, chunks, "Should extract chunks")

	// Should have: 1 class + 2 methods
	require.GreaterOrEqual(t, len(chunks), 3, "Should have at least 3 chunks (1 class + 2 methods)")

	// Find class chunk
	var classChunk *codetypes.CodeChunk
	for i := range chunks {
		if chunks[i].Type == "class" && chunks[i].Name == "User" {
			classChunk = &chunks[i]
			break
		}
	}
	require.NotNil(t, classChunk, "Should find User class")
	require.Equal(t, "php", classChunk.Language)
	require.Equal(t, "App\\Models", classChunk.Package)

	// Find method chunks
	methodCount := 0
	for _, chunk := range chunks {
		if chunk.Type == "method" {
			methodCount++
		}
	}
	require.Equal(t, 2, methodCount, "Should have 2 method chunks")
}

func TestCodeAnalyzer_GlobalFunction(t *testing.T) {
	tmpDir := t.TempDir()
	phpFile := filepath.Join(tmpDir, "functions.php")

	phpCode := `<?php
function hello($name) {
    return "Hello " . $name;
}

function world() {
    return "World";
}
`

	err := os.WriteFile(phpFile, []byte(phpCode), 0644)
	require.NoError(t, err)

	analyzer := NewCodeAnalyzer()
	chunks, err := analyzer.AnalyzeFile(phpFile)
	require.NoError(t, err)

	// Should have 2 functions
	require.Len(t, chunks, 2)

	for _, chunk := range chunks {
		require.Equal(t, "function", chunk.Type)
		require.Equal(t, "php", chunk.Language)
	}
}

func TestCodeAnalyzer_NamespacedFunction(t *testing.T) {
	tmpDir := t.TempDir()
	phpFile := filepath.Join(tmpDir, "helper.php")

	phpCode := `<?php
namespace App\Helpers;

function formatDate($date) {
    return date('Y-m-d', $date);
}
`

	err := os.WriteFile(phpFile, []byte(phpCode), 0644)
	require.NoError(t, err)

	analyzer := NewCodeAnalyzer()
	chunks, err := analyzer.AnalyzeFile(phpFile)
	require.NoError(t, err)

	require.Len(t, chunks, 1)
	require.Equal(t, "function", chunks[0].Type)
	require.Equal(t, "formatDate", chunks[0].Name)
	require.Equal(t, "App\\Helpers", chunks[0].Package)
}

func TestCodeAnalyzer_MultipleClasses(t *testing.T) {
	tmpDir := t.TempDir()
	phpFile := filepath.Join(tmpDir, "models.php")

	phpCode := `<?php
namespace App\Models;

class User {
    public function getId() {
        return $this->id;
    }
}

class Post {
    public function getTitle() {
        return $this->title;
    }
}
`

	err := os.WriteFile(phpFile, []byte(phpCode), 0644)
	require.NoError(t, err)

	analyzer := NewCodeAnalyzer()
	chunks, err := analyzer.AnalyzeFile(phpFile)
	require.NoError(t, err)

	// 2 classes + 2 methods = 4 chunks
	require.GreaterOrEqual(t, len(chunks), 4)

	classCount := 0
	for _, chunk := range chunks {
		if chunk.Type == "class" {
			classCount++
			require.Equal(t, "App\\Models", chunk.Package)
		}
	}
	require.Equal(t, 2, classCount, "Should have 2 classes")
}

func TestCodeAnalyzer_AnalyzePaths(t *testing.T) {
	tmpDir := t.TempDir()

	// Create multiple PHP files
	file1 := filepath.Join(tmpDir, "class1.php")
	file2 := filepath.Join(tmpDir, "class2.php")

	err := os.WriteFile(file1, []byte(`<?php
class ClassOne {
    public function method1() {}
}
`), 0644)
	require.NoError(t, err)

	err = os.WriteFile(file2, []byte(`<?php
class ClassTwo {
    public function method2() {}
}
`), 0644)
	require.NoError(t, err)

	analyzer := NewCodeAnalyzer()
	chunks, err := analyzer.AnalyzePaths([]string{file1, file2})
	require.NoError(t, err)

	// Should have chunks from both files
	require.GreaterOrEqual(t, len(chunks), 4, "Should have at least 4 chunks (2 classes + 2 methods)")

	// Verify both classes are present
	classNames := make(map[string]bool)
	for _, chunk := range chunks {
		if chunk.Type == "class" {
			classNames[chunk.Name] = true
		}
	}

	require.True(t, classNames["ClassOne"], "Should have ClassOne")
	require.True(t, classNames["ClassTwo"], "Should have ClassTwo")
}

func TestCodeAnalyzer_ClassWithProperties(t *testing.T) {
	tmpDir := t.TempDir()
	phpFile := filepath.Join(tmpDir, "user.php")

	phpCode := `<?php
namespace App;

class User {
    public string $name;
    private int $age;
    protected $email;
    public static $count;
    public readonly string $id;
}
`

	err := os.WriteFile(phpFile, []byte(phpCode), 0644)
	require.NoError(t, err)

	analyzer := NewCodeAnalyzer()
	chunks, err := analyzer.AnalyzeFile(phpFile)
	require.NoError(t, err)

	// 1 class + 5 properties = 6 chunks
	require.GreaterOrEqual(t, len(chunks), 6)

	// Count property chunks
	propCount := 0
	for _, chunk := range chunks {
		if chunk.Type == "property" {
			propCount++
			require.Equal(t, "App", chunk.Package)
		}
	}
	require.Equal(t, 5, propCount, "Should have 5 property chunks")
}

func TestCodeAnalyzer_ClassWithConstants(t *testing.T) {
	tmpDir := t.TempDir()
	phpFile := filepath.Join(tmpDir, "status.php")

	phpCode := `<?php
namespace App;

class Status {
    public const ACTIVE = 1;
    private const INACTIVE = 0;
    protected const PENDING = 2;
}
`

	err := os.WriteFile(phpFile, []byte(phpCode), 0644)
	require.NoError(t, err)

	analyzer := NewCodeAnalyzer()
	chunks, err := analyzer.AnalyzeFile(phpFile)
	require.NoError(t, err)

	// 1 class + 3 constants = 4 chunks
	require.GreaterOrEqual(t, len(chunks), 4)

	// Find constant chunks
	constCount := 0
	constNames := make(map[string]bool)
	for _, chunk := range chunks {
		if chunk.Type == "constant" {
			constCount++
			constNames[chunk.Name] = true
			require.Equal(t, "App", chunk.Package)
		}
	}
	require.Equal(t, 3, constCount, "Should have 3 constant chunks")
	require.True(t, constNames["ACTIVE"])
	require.True(t, constNames["INACTIVE"])
	require.True(t, constNames["PENDING"])
}

func TestCodeAnalyzer_Interface(t *testing.T) {
	tmpDir := t.TempDir()
	phpFile := filepath.Join(tmpDir, "repository.php")

	phpCode := `<?php
namespace App\Contracts;

interface RepositoryInterface {
    public function find($id);
    public function save($entity);
    public function delete($id);
}
`

	err := os.WriteFile(phpFile, []byte(phpCode), 0644)
	require.NoError(t, err)

	analyzer := NewCodeAnalyzer()
	chunks, err := analyzer.AnalyzeFile(phpFile)
	require.NoError(t, err)

	// 1 interface + 3 methods = 4 chunks
	require.GreaterOrEqual(t, len(chunks), 4)

	// Find interface chunk
	var ifaceChunk *codetypes.CodeChunk
	for i := range chunks {
		if chunks[i].Type == "interface" && chunks[i].Name == "RepositoryInterface" {
			ifaceChunk = &chunks[i]
			break
		}
	}
	require.NotNil(t, ifaceChunk)
	require.Equal(t, "App\\Contracts", ifaceChunk.Package)

	// Count methods
	methodCount := 0
	methodNames := make(map[string]bool)
	for _, chunk := range chunks {
		if chunk.Type == "method" {
			methodCount++
			methodNames[chunk.Name] = true
		}
	}
	require.Equal(t, 3, methodCount)
	require.True(t, methodNames["find"])
	require.True(t, methodNames["save"])
	require.True(t, methodNames["delete"])
}

func TestCodeAnalyzer_Trait(t *testing.T) {
	tmpDir := t.TempDir()
	phpFile := filepath.Join(tmpDir, "timestampable.php")

	phpCode := `<?php
namespace App\Traits;

trait Timestampable {
    protected $createdAt;
    protected $updatedAt;
    
    public function getCreatedAt() {
        return $this->createdAt;
    }
    
    public function touch() {
        $this->updatedAt = time();
    }
}
`

	err := os.WriteFile(phpFile, []byte(phpCode), 0644)
	require.NoError(t, err)

	analyzer := NewCodeAnalyzer()
	chunks, err := analyzer.AnalyzeFile(phpFile)
	require.NoError(t, err)

	// 1 trait + 2 methods + 2 properties = 5 chunks
	require.GreaterOrEqual(t, len(chunks), 5)

	// Find trait chunk
	var traitChunk *codetypes.CodeChunk
	for i := range chunks {
		if chunks[i].Type == "trait" && chunks[i].Name == "Timestampable" {
			traitChunk = &chunks[i]
			break
		}
	}
	require.NotNil(t, traitChunk)
	require.Equal(t, "App\\Traits", traitChunk.Package)

	// Count methods and properties
	methodCount := 0
	propCount := 0
	for _, chunk := range chunks {
		if chunk.Type == "method" {
			methodCount++
		}
		if chunk.Type == "property" {
			propCount++
		}
	}
	require.Equal(t, 2, methodCount, "Should have 2 methods")
	require.Equal(t, 2, propCount, "Should have 2 properties")
}

func TestCodeAnalyzer_CompleteClass(t *testing.T) {
	tmpDir := t.TempDir()
	phpFile := filepath.Join(tmpDir, "complete.php")

	phpCode := `<?php
namespace App\Models;

class Product {
    public const TYPE_PHYSICAL = 'physical';
    public const TYPE_DIGITAL = 'digital';
    
    private int $id;
    public string $name;
    protected float $price;
    
    public function __construct($name, $price) {
        $this->name = $name;
        $this->price = $price;
    }
    
    public function getId(): int {
        return $this->id;
    }
    
    public function getPrice(): float {
        return $this->price;
    }
}
`

	err := os.WriteFile(phpFile, []byte(phpCode), 0644)
	require.NoError(t, err)

	analyzer := NewCodeAnalyzer()
	chunks, err := analyzer.AnalyzeFile(phpFile)
	require.NoError(t, err)

	// 1 class + 2 constants + 3 properties + 3 methods = 9 chunks
	require.GreaterOrEqual(t, len(chunks), 9)

	// Verify chunk types
	typeCounts := make(map[string]int)
	for _, chunk := range chunks {
		typeCounts[chunk.Type]++
	}

	require.Equal(t, 1, typeCounts["class"], "Should have 1 class")
	require.Equal(t, 2, typeCounts["constant"], "Should have 2 constants")
	require.Equal(t, 3, typeCounts["property"], "Should have 3 properties")
	require.Equal(t, 3, typeCounts["method"], "Should have 3 methods")
}

func TestCodeAnalyzer_BarouUserClass(t *testing.T) {
	// This test inspects the real Laravel User model from the barou project, if present.
	// It is intended for manual inspection of the extracted CodeChunk, not for strict assertions.
	userPath := "/home/razvan/go/src/github.com/doITmagic/rag-code-mcp/barou/app/User.php"
	if _, err := os.Stat(userPath); os.IsNotExist(err) {
		t.Skipf("barou User.php not found at %s, skipping", userPath)
	}

	analyzer := NewCodeAnalyzer()
	chunks, err := analyzer.AnalyzeFile(userPath)
	require.NoError(t, err)
	require.NotEmpty(t, chunks, "expected at least one chunk from User.php")

	// Find the User class chunk
	var classChunk *codetypes.CodeChunk
	for i := range chunks {
		ch := &chunks[i]
		if ch.Type == "class" && ch.Name == "User" {
			classChunk = ch
			break
		}
	}
	require.NotNil(t, classChunk, "expected to find User class chunk")

	// Log high-level information similar to what we want for Go types
	t.Logf("User class: Name=%s, Package=%s, Signature=%s", classChunk.Name, classChunk.Package, classChunk.Signature)
	t.Logf("Location: %s:%d-%d", classChunk.FilePath, classChunk.StartLine, classChunk.EndLine)
	t.Logf("Docstring: %s", classChunk.Docstring)

	codePreview := classChunk.Code
	if len(codePreview) > 400 {
		codePreview = codePreview[:400]
	}
	t.Logf("Code preview (first 400 chars):\n%s", codePreview)

	// Also log methods defined in the same file/package to inspect relationships
	for _, ch := range chunks {
		if ch.Type == "method" && ch.FilePath == classChunk.FilePath && ch.Package == classChunk.Package {
			preview := ch.Code
			if len(preview) > 200 {
				preview = preview[:200]
			}
			t.Logf("Method: %s | Signature=%s | Lines=%d-%d\n%s", ch.Name, ch.Signature, ch.StartLine, ch.EndLine, preview)
		}
	}
}

//go:build ignore
// +build ignore

package golang

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/doITmagic/coderag-mcp/internal/codetypes"
)

func TestAPIAnalyzer_AnalyzeAPIPaths(t *testing.T) {
	tmpDir := t.TempDir()
	testFile := filepath.Join(tmpDir, "api.go")

	testCode := `package api

import "context"

// UserService provides user management functionality
type UserService struct {
	db Database
}

// Database represents a database connection
type Database interface {
	Query(ctx context.Context, query string) error
}

// CreateUser creates a new user in the system
// Returns the user ID and any error encountered
func (s *UserService) CreateUser(name string, email string) (int, error) {
	// Implementation here
	return 0, nil
}

// GetUser retrieves a user by ID
func (s *UserService) GetUser(id int) (*User, error) {
	return nil, nil
}

// User represents a user entity
type User struct {
	ID    int    
	Name  string 
	Email string 
}

const (
	// MaxRetries is the maximum number of retry attempts
	MaxRetries = 3
	// DefaultTimeout is the default timeout in seconds
	DefaultTimeout = 30
)

var (
	// ErrNotFound is returned when a resource is not found
	ErrNotFound = "not found"
)
`

	if err := os.WriteFile(testFile, []byte(testCode), 0644); err != nil {
		t.Fatalf("Failed to create test file: %v", err)
	}

	codeAnalyzer := NewCodeAnalyzer()
	apiAnalyzer := NewAPIAnalyzer(codeAnalyzer)

	chunks, err := apiAnalyzer.AnalyzeAPIPaths([]string{tmpDir})
	if err != nil {
		t.Fatalf("AnalyzeAPIPaths failed: %v", err)
	}

	if len(chunks) == 0 {
		t.Fatal("Expected at least some API chunks, got none")
	}

	// Verify that all chunks have Language set to "go"
	for _, chunk := range chunks {
		if chunk.Language != "go" {
			t.Errorf("Expected Language='go', got '%s' for chunk: %s", chunk.Language, chunk.Name)
		}
	}

	// Check for specific API symbols
	foundCreateUser := false
	foundGetUser := false
	foundUserService := false
	foundUser := false
	foundDatabase := false
	foundMaxRetries := false
	foundErrNotFound := false

	for _, chunk := range chunks {
		t.Logf("Found API chunk: Kind=%s, Name=%s, Package=%s", chunk.Kind, chunk.Name, chunk.Package)

		switch chunk.Name {
		case "CreateUser":
			foundCreateUser = true
			if chunk.Kind != "method" {
				t.Errorf("Expected CreateUser to be 'method', got '%s'", chunk.Kind)
			}
			if len(chunk.Parameters) == 0 {
				t.Error("Expected CreateUser to have parameters")
			}
		case "GetUser":
			foundGetUser = true
			if chunk.Kind != "method" {
				t.Errorf("Expected GetUser to be 'method', got '%s'", chunk.Kind)
			}
		case "UserService":
			foundUserService = true
			if chunk.Kind != "type" {
				t.Errorf("Expected UserService to be 'type', got '%s'", chunk.Kind)
			}
		case "User":
			foundUser = true
			if chunk.Kind != "type" {
				t.Errorf("Expected User to be 'type', got '%s'", chunk.Kind)
			}
			if len(chunk.Fields) == 0 {
				t.Error("Expected User type to have fields")
			}
		case "Database":
			foundDatabase = true
			if chunk.Kind != "type" && chunk.Kind != "interface" {
				t.Errorf("Expected Database to be 'type' or 'interface', got '%s'", chunk.Kind)
			}
		case "MaxRetries":
			foundMaxRetries = true
			if chunk.Kind != "const" {
				t.Errorf("Expected MaxRetries to be 'const', got '%s'", chunk.Kind)
			}
		case "ErrNotFound":
			foundErrNotFound = true
			if chunk.Kind != "var" {
				t.Errorf("Expected ErrNotFound to be 'var', got '%s'", chunk.Kind)
			}
		}
	}

	if !foundCreateUser {
		t.Error("Did not find 'CreateUser' method in API chunks")
	}
	if !foundGetUser {
		t.Error("Did not find 'GetUser' method in API chunks")
	}
	if !foundUserService {
		t.Error("Did not find 'UserService' type in API chunks")
	}
	if !foundUser {
		t.Error("Did not find 'User' type in API chunks")
	}
	if !foundDatabase {
		t.Error("Did not find 'Database' interface in API chunks")
	}
	if !foundMaxRetries {
		t.Error("Did not find 'MaxRetries' constant in API chunks")
	}
	if !foundErrNotFound {
		t.Error("Did not find 'ErrNotFound' variable in API chunks")
	}
}

func TestAPIAnalyzer_EmptyDirectory(t *testing.T) {
	tmpDir := t.TempDir()

	codeAnalyzer := NewCodeAnalyzer()
	apiAnalyzer := NewAPIAnalyzer(codeAnalyzer)

	chunks, err := apiAnalyzer.AnalyzeAPIPaths([]string{tmpDir})
	if err != nil {
		t.Fatalf("AnalyzeAPIPaths failed on empty directory: %v", err)
	}

	if len(chunks) != 0 {
		t.Errorf("Expected 0 API chunks for empty directory, got %d", len(chunks))
	}
}

func TestAPIAnalyzer_ChunkMetadata(t *testing.T) {
	tmpDir := t.TempDir()
	testFile := filepath.Join(tmpDir, "metadata.go")

	testCode := `package metadata

// Process processes data
func Process(input string) (string, error) {
	return input, nil
}
`

	if err := os.WriteFile(testFile, []byte(testCode), 0644); err != nil {
		t.Fatalf("Failed to create test file: %v", err)
	}

	codeAnalyzer := NewCodeAnalyzer()
	apiAnalyzer := NewAPIAnalyzer(codeAnalyzer)

	chunks, err := apiAnalyzer.AnalyzeAPIPaths([]string{tmpDir})
	if err != nil {
		t.Fatalf("AnalyzeAPIPaths failed: %v", err)
	}

	if len(chunks) == 0 {
		t.Fatal("Expected at least one API chunk")
	}

	for _, chunk := range chunks {
		// Verify required fields
		if chunk.FilePath == "" {
			t.Error("FilePath should not be empty")
		}
		if chunk.Package == "" {
			t.Error("Package should not be empty")
		}
		if chunk.Language != "go" {
			t.Errorf("Expected Language='go', got '%s'", chunk.Language)
		}

		// Check position metadata
		if chunk.StartLine <= 0 {
			t.Error("StartLine should be > 0")
		}
		if chunk.EndLine <= 0 {
			t.Error("EndLine should be > 0")
		}
	}
}

func TestAPIAnalyzer_ImplementsInterface(t *testing.T) {
	codeAnalyzer := NewCodeAnalyzer()
	apiAnalyzer := NewAPIAnalyzer(codeAnalyzer)

	// Verify that APIAnalyzerImpl implements codetypes.APIAnalyzer
	var _ codetypes.APIAnalyzer = apiAnalyzer
}

func TestAPIAnalyzer_FunctionParameters(t *testing.T) {
	tmpDir := t.TempDir()
	testFile := filepath.Join(tmpDir, "params.go")

	testCode := `package params

// Calculate performs a calculation
func Calculate(x int, y int, operation string) (result int, err error) {
	return 0, nil
}
`

	if err := os.WriteFile(testFile, []byte(testCode), 0644); err != nil {
		t.Fatalf("Failed to create test file: %v", err)
	}

	codeAnalyzer := NewCodeAnalyzer()
	apiAnalyzer := NewAPIAnalyzer(codeAnalyzer)

	chunks, err := apiAnalyzer.AnalyzeAPIPaths([]string{tmpDir})
	if err != nil {
		t.Fatalf("AnalyzeAPIPaths failed: %v", err)
	}

	var calculateChunk *codetypes.APIChunk
	for i := range chunks {
		if chunks[i].Name == "Calculate" {
			calculateChunk = &chunks[i]
			break
		}
	}

	if calculateChunk == nil {
		t.Fatal("Did not find 'Calculate' function in API chunks")
	}

	// Verify parameters
	if len(calculateChunk.Parameters) < 3 {
		t.Errorf("Expected at least 3 parameters, got %d", len(calculateChunk.Parameters))
	}

	// Verify returns
	if len(calculateChunk.Returns) < 2 {
		t.Errorf("Expected at least 2 return values, got %d", len(calculateChunk.Returns))
	}
}

func TestAPIAnalyzer_TypeFields(t *testing.T) {
	tmpDir := t.TempDir()
	testFile := filepath.Join(tmpDir, "fields.go")

	testCode := `package fields

// Config holds application configuration
type Config struct {
	Host     string
	Port     int
	Enabled  bool
	Tags     []string
}
`

	if err := os.WriteFile(testFile, []byte(testCode), 0644); err != nil {
		t.Fatalf("Failed to create test file: %v", err)
	}

	codeAnalyzer := NewCodeAnalyzer()
	apiAnalyzer := NewAPIAnalyzer(codeAnalyzer)

	chunks, err := apiAnalyzer.AnalyzeAPIPaths([]string{tmpDir})
	if err != nil {
		t.Fatalf("AnalyzeAPIPaths failed: %v", err)
	}

	var configChunk *codetypes.APIChunk
	for i := range chunks {
		if chunks[i].Name == "Config" {
			configChunk = &chunks[i]
			break
		}
	}

	if configChunk == nil {
		t.Fatal("Did not find 'Config' type in API chunks")
	}

	// Verify fields
	if len(configChunk.Fields) < 4 {
		t.Errorf("Expected at least 4 fields, got %d", len(configChunk.Fields))
	}

	// Check for specific fields
	fieldNames := make(map[string]bool)
	for _, field := range configChunk.Fields {
		fieldNames[field.Name] = true
	}

	expectedFields := []string{"Host", "Port", "Enabled", "Tags"}
	for _, expected := range expectedFields {
		if !fieldNames[expected] {
			t.Errorf("Expected field '%s' not found", expected)
		}
	}
}

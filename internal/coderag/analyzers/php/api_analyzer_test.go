//go:build ignore
// +build ignore

package php

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/doITmagic/coderag-mcp/internal/codetypes"
	"github.com/stretchr/testify/require"
)

func TestAPIAnalyzer_AnalyzeAPIPaths(t *testing.T) {
	tmpDir := t.TempDir()
	testFile := filepath.Join(tmpDir, "UserService.php")

	testCode := `<?php
namespace App\Services;

/**
 * UserService provides user management functionality.
 * 
 * This service handles all user-related operations including
 * creation, retrieval, and updates.
 * 
 * @package App\Services
 */
class UserService {
    /**
     * Database connection
     * 
     * @var Database
     */
    private Database $db;
    
    /**
     * Create a new user in the system.
     * 
     * @param string $name The user's full name
     * @param string $email The user's email address
     * @return int The created user ID
     * @throws \InvalidArgumentException If email is invalid
     */
    public function createUser(string $name, string $email): int {
        // Implementation here
        return 0;
    }
    
    /**
     * Retrieve a user by ID.
     * 
     * @param int $id The user ID
     * @return User|null The user entity or null if not found
     */
    public function getUser(int $id): ?User {
        return null;
    }
}

/**
 * User entity represents a system user.
 * 
 * @package App\Services
 */
class User {
    /** @var int User ID */
    public int $id;
    
    /** @var string User's name */
    public string $name;
    
    /** @var string User's email */
    public string $email;
}
`

	err := os.WriteFile(testFile, []byte(testCode), 0644)
	require.NoError(t, err)

	codeAnalyzer := NewCodeAnalyzer()
	apiAnalyzer := NewAPIAnalyzer(codeAnalyzer)

	chunks, err := apiAnalyzer.AnalyzeAPIPaths([]string{tmpDir})
	require.NoError(t, err)

	require.NotEmpty(t, chunks, "Expected at least some API chunks")

	// Verify that all chunks have Language set to "php"
	for _, chunk := range chunks {
		require.Equal(t, "php", chunk.Language, "Chunk %s should have Language='php'", chunk.Name)
	}

	// Find UserService class
	var userServiceChunk *codetypes.APIChunk
	for i := range chunks {
		if chunks[i].Kind == "class" && chunks[i].Name == "UserService" {
			userServiceChunk = &chunks[i]
			break
		}
	}

	require.NotNil(t, userServiceChunk, "Should find UserService class chunk")
	require.Equal(t, "App\\Services", userServiceChunk.Package)
	require.Contains(t, userServiceChunk.Description, "UserService provides user management functionality")
	require.NotEmpty(t, userServiceChunk.Methods, "UserService should have methods")

	// Verify methods have descriptions
	hasDescriptions := false
	for _, method := range userServiceChunk.Methods {
		if method.Description != "" {
			hasDescriptions = true
			t.Logf("Method %s has description: %s", method.Name, method.Description)
		}
	}
	require.True(t, hasDescriptions, "At least one method should have a description from PHPDoc")

	// Find User class
	var userChunk *codetypes.APIChunk
	for i := range chunks {
		if chunks[i].Kind == "class" && chunks[i].Name == "User" {
			userChunk = &chunks[i]
			break
		}
	}

	require.NotNil(t, userChunk, "Should find User class chunk")
	require.Contains(t, userChunk.Description, "User entity represents a system user")
	require.NotEmpty(t, userChunk.Fields, "User should have fields/properties")
}

func TestAPIAnalyzer_GlobalFunctions(t *testing.T) {
	tmpDir := t.TempDir()
	testFile := filepath.Join(tmpDir, "helpers.php")

	testCode := `<?php
namespace App\Helpers;

/**
 * Format a date to ISO 8601 format.
 * 
 * @param int $timestamp Unix timestamp
 * @return string Formatted date string
 */
function formatDate(int $timestamp): string {
    return date('c', $timestamp);
}

/**
 * Validate an email address.
 * 
 * @param string $email Email to validate
 * @return bool True if valid, false otherwise
 */
function validateEmail(string $email): bool {
    return filter_var($email, FILTER_VALIDATE_EMAIL) !== false;
}
`

	err := os.WriteFile(testFile, []byte(testCode), 0644)
	require.NoError(t, err)

	codeAnalyzer := NewCodeAnalyzer()
	apiAnalyzer := NewAPIAnalyzer(codeAnalyzer)

	chunks, err := apiAnalyzer.AnalyzeAPIPaths([]string{tmpDir})
	require.NoError(t, err)

	// Find function chunks
	functionChunks := []codetypes.APIChunk{}
	for _, chunk := range chunks {
		if chunk.Kind == "function" {
			functionChunks = append(functionChunks, chunk)
		}
	}

	require.Len(t, functionChunks, 2, "Should have 2 function chunks")

	// Verify functions have descriptions
	for _, fn := range functionChunks {
		require.NotEmpty(t, fn.Description, "Function %s should have description", fn.Name)
		require.Equal(t, "App\\Helpers", fn.Package)
		t.Logf("Function %s: %s", fn.Name, fn.Description)
	}
}

func TestAPIAnalyzer_Interface(t *testing.T) {
	tmpDir := t.TempDir()
	testFile := filepath.Join(tmpDir, "Repository.php")

	testCode := `<?php
namespace App\Contracts;

/**
 * Repository interface for data access.
 * 
 * @package App\Contracts
 */
interface RepositoryInterface {
    /**
     * Find entity by ID.
     * 
     * @param int $id Entity ID
     * @return mixed Entity or null
     */
    public function find(int $id);
    
    /**
     * Save an entity.
     * 
     * @param mixed $entity Entity to save
     * @return bool Success status
     */
    public function save($entity): bool;
}
`

	err := os.WriteFile(testFile, []byte(testCode), 0644)
	require.NoError(t, err)

	codeAnalyzer := NewCodeAnalyzer()
	apiAnalyzer := NewAPIAnalyzer(codeAnalyzer)

	chunks, err := apiAnalyzer.AnalyzeAPIPaths([]string{tmpDir})
	require.NoError(t, err)

	// Find interface chunk
	var ifaceChunk *codetypes.APIChunk
	for i := range chunks {
		if chunks[i].Kind == "interface" && chunks[i].Name == "RepositoryInterface" {
			ifaceChunk = &chunks[i]
			break
		}
	}

	require.NotNil(t, ifaceChunk, "Should find RepositoryInterface")
	require.Contains(t, ifaceChunk.Description, "Repository interface for data access")
	require.Len(t, ifaceChunk.Methods, 2, "Interface should have 2 methods")

	// Verify methods have descriptions
	for _, method := range ifaceChunk.Methods {
		require.NotEmpty(t, method.Description, "Method %s should have description", method.Name)
	}
}

func TestAPIAnalyzer_Trait(t *testing.T) {
	tmpDir := t.TempDir()
	testFile := filepath.Join(tmpDir, "Timestampable.php")

	testCode := `<?php
namespace App\Traits;

/**
 * Timestampable trait adds timestamp functionality.
 * 
 * @package App\Traits
 */
trait Timestampable {
    /** @var int Creation timestamp */
    protected int $createdAt;
    
    /** @var int Update timestamp */
    protected int $updatedAt;
    
    /**
     * Update the updated_at timestamp.
     * 
     * @return void
     */
    public function touch(): void {
        $this->updatedAt = time();
    }
}
`

	err := os.WriteFile(testFile, []byte(testCode), 0644)
	require.NoError(t, err)

	codeAnalyzer := NewCodeAnalyzer()
	apiAnalyzer := NewAPIAnalyzer(codeAnalyzer)

	chunks, err := apiAnalyzer.AnalyzeAPIPaths([]string{tmpDir})
	require.NoError(t, err)

	// Find trait chunk
	var traitChunk *codetypes.APIChunk
	for i := range chunks {
		if chunks[i].Kind == "trait" && chunks[i].Name == "Timestampable" {
			traitChunk = &chunks[i]
			break
		}
	}

	require.NotNil(t, traitChunk, "Should find Timestampable trait")
	require.Contains(t, traitChunk.Description, "Timestampable trait adds timestamp functionality")
	require.NotEmpty(t, traitChunk.Methods, "Trait should have methods")
	require.NotEmpty(t, traitChunk.Fields, "Trait should have fields/properties")
}

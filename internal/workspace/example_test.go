package workspace_test

import (
	"fmt"
	"log"
	"time"

	"github.com/doITmagic/rag-code-mcp/internal/workspace"
)

func ExampleDetector_DetectFromPath() {
	detector := workspace.NewDetector()

	// Detect workspace from a file path
	info, err := detector.DetectFromPath("/home/user/projects/my-app/src/main.go")
	if err != nil {
		log.Fatal(err)
	}

	fmt.Printf("Workspace root: %s\n", info.Root)
	fmt.Printf("Workspace ID: %s\n", info.ID)
	fmt.Printf("Project type: %s\n", info.ProjectType)
	fmt.Printf("Collection name: %s\n", info.CollectionName())
}

func ExampleDetector_DetectFromParams() {
	detector := workspace.NewDetector()

	// Simulate MCP tool parameters
	params := map[string]interface{}{
		"file_path": "/home/user/projects/my-app/internal/handlers/user.go",
		"query":     "user authentication",
	}

	info, err := detector.DetectFromParams(params)
	if err != nil {
		log.Fatal(err)
	}

	fmt.Printf("Detected workspace: %s\n", info.Root)
	fmt.Printf("Collection: %s\n", info.CollectionName())
}

func ExampleCache() {
	// Create cache with 5 minute TTL
	cache := workspace.NewCache(5 * time.Minute)

	detector := workspace.NewDetector()

	// Function to get or detect workspace
	getWorkspace := func(filePath string) (*workspace.Info, error) {
		// Try cache first
		if cached := cache.Get(filePath); cached != nil {
			return cached, nil
		}

		// Detect and cache
		info, err := detector.DetectFromPath(filePath)
		if err != nil {
			return nil, err
		}

		cache.Set(filePath, info)
		return info, nil
	}

	// First call - detects and caches
	info1, _ := getWorkspace("/home/user/project/main.go")
	fmt.Printf("First call: %s\n", info1.Root)

	// Second call - from cache (fast)
	info2, _ := getWorkspace("/home/user/project/main.go")
	fmt.Printf("Second call (cached): %s\n", info2.Root)

	// Periodic cleanup
	go func() {
		ticker := time.NewTicker(10 * time.Minute)
		defer ticker.Stop()

		for range ticker.C {
			removed := cache.CleanExpired()
			fmt.Printf("Cleaned %d expired entries\n", removed)
		}
	}()
}

func ExampleDetector_SetMarkers() {
	detector := workspace.NewDetector()

	// Customize workspace markers for specific use case
	detector.SetMarkers([]string{
		".git",
		"package.json",
		"deno.json", // Add custom marker
	})

	// Set exclusion patterns
	detector.SetExcludePatterns([]string{
		"/node_modules/",
		"/dist/",
		"/.next/",
	})

	info, _ := detector.DetectFromPath("/home/user/deno-project/src/main.ts")
	fmt.Printf("Custom detection: %s\n", info.Root)
}

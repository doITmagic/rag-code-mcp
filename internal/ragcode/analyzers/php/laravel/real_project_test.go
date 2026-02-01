package laravel

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/doITmagic/rag-code-mcp/internal/ragcode/analyzers/php"
	"github.com/stretchr/testify/assert"
)

// TestRealBarouProject_FullAnalysis tests the complete analysis of the Barou Laravel project
func TestRealBarouProject_FullAnalysis(t *testing.T) {
	barouPath := "/home/razvan/go/src/github.com/doITmagic/rag-code-mcp/barou"

	// Check if barou project exists
	if _, err := os.Stat(barouPath); os.IsNotExist(err) {
		t.Skip("Barou project not found, skipping real project test")
		return
	}

	fmt.Println("\n🚀 Starting Barou Laravel Project Analysis...")
	fmt.Println("=" + string(make([]byte, 60)) + "=")

	// Test 1: Analyze User.php model
	userFile := filepath.Join(barouPath, "app", "User.php")
	if _, err := os.Stat(userFile); err == nil {
		fmt.Println("\n📄 Analyzing User.php model...")

		phpAnalyzer := php.NewCodeAnalyzer()
		_, err := phpAnalyzer.AnalyzeFile(userFile)
		assert.NoError(t, err)

		assert.True(t, phpAnalyzer.IsLaravelProject(), "Should detect Laravel project")

		packages := phpAnalyzer.GetPackages()
		if len(packages) > 0 {
			laravelAnalyzer := NewEloquentAnalyzer(packages[0])
			models := laravelAnalyzer.AnalyzeModels()

			if len(models) > 0 {
				user := models[0]
				fmt.Printf("   ✓ Model: %s\n", user.FullName)
				fmt.Printf("   ✓ Table: %s\n", user.Table)
				fmt.Printf("   ✓ Fillable: %d properties\n", len(user.Fillable))
				fmt.Printf("   ✓ Relations: %d\n", len(user.Relations))
				fmt.Printf("   ✓ Scopes: %d\n", len(user.Scopes))

				for _, rel := range user.Relations {
					fmt.Printf("      • %s: %s -> %s\n", rel.Name, rel.Type, rel.RelatedModel)
				}
			}
		}
	}

	// Test 2: Analyze all models in app directory
	fmt.Println("\n📦 Analyzing all models in app directory...")

	appDir := filepath.Join(barouPath, "app")
	var modelFiles []string

	err := filepath.Walk(appDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if info.IsDir() {
			// Skip certain directories
			if info.Name() == "Http" || info.Name() == "Console" || info.Name() == "Events" ||
				info.Name() == "Listeners" || info.Name() == "Jobs" || info.Name() == "Mail" ||
				info.Name() == "Providers" || info.Name() == "Exceptions" {
				return filepath.SkipDir
			}
			return nil
		}
		if filepath.Ext(path) == ".php" && !contains(filepath.Base(path), "Helper") {
			modelFiles = append(modelFiles, path)
		}
		return nil
	})

	assert.NoError(t, err)
	fmt.Printf("   Found %d potential model files\n\n", len(modelFiles))

	totalModels := 0
	totalRelations := 0
	totalScopes := 0
	modelStats := make(map[string]struct {
		Relations int
		Scopes    int
		Fillable  int
	})

	for _, file := range modelFiles {
		phpAnalyzer := php.NewCodeAnalyzer()
		_, err := phpAnalyzer.AnalyzeFile(file)
		if err != nil {
			continue
		}

		if !phpAnalyzer.IsLaravelProject() {
			continue
		}

		packages := phpAnalyzer.GetPackages()
		if len(packages) == 0 {
			continue
		}

		laravelAnalyzer := NewEloquentAnalyzer(packages[0])
		models := laravelAnalyzer.AnalyzeModels()

		for _, model := range models {
			totalModels++
			totalRelations += len(model.Relations)
			totalScopes += len(model.Scopes)

			modelStats[model.ClassName] = struct {
				Relations int
				Scopes    int
				Fillable  int
			}{
				Relations: len(model.Relations),
				Scopes:    len(model.Scopes),
				Fillable:  len(model.Fillable),
			}

			if len(model.Relations) > 0 || len(model.Scopes) > 0 {
				fmt.Printf("   📄 %s\n", model.ClassName)
				if model.Table != "" {
					fmt.Printf("      Table: %s\n", model.Table)
				}
				if len(model.Fillable) > 0 {
					fmt.Printf("      Fillable: %d properties\n", len(model.Fillable))
				}
				if len(model.Relations) > 0 {
					fmt.Printf("      Relations: %d\n", len(model.Relations))
					for _, rel := range model.Relations {
						fmt.Printf("         • %s: %s -> %s\n", rel.Name, rel.Type, rel.RelatedModel)
					}
				}
				if len(model.Scopes) > 0 {
					fmt.Printf("      Scopes: %d\n", len(model.Scopes))
					for _, scope := range model.Scopes {
						fmt.Printf("         • %s\n", scope.Name)
					}
				}
				fmt.Println()
			}
		}
	}

	fmt.Println("\n📊 Analysis Summary:")
	fmt.Println("=" + string(make([]byte, 60)) + "=")
	fmt.Printf("   Total Models Analyzed: %d\n", totalModels)
	fmt.Printf("   Total Relations Found: %d\n", totalRelations)
	fmt.Printf("   Total Scopes Found: %d\n", totalScopes)

	if totalModels > 0 {
		fmt.Printf("   Average Relations per Model: %.1f\n", float64(totalRelations)/float64(totalModels))
	}

	// Test 3: Test with Adapter (full integration)
	fmt.Println("\n🔧 Testing with Laravel Adapter...")

	adapter := NewAdapter()
	chunks, err := adapter.AnalyzePaths([]string{appDir})
	assert.NoError(t, err)

	fmt.Printf("   Total chunks generated: %d\n", len(chunks))

	// Count different chunk types
	chunkTypes := make(map[string]int)
	laravelModels := 0
	laravelControllers := 0

	for _, chunk := range chunks {
		chunkTypes[chunk.Type]++

		if chunk.Metadata != nil {
			if laravelType, ok := chunk.Metadata["laravel_type"]; ok {
				if laravelType == "model" {
					laravelModels++
				} else if laravelType == "controller" {
					laravelControllers++
				}
			}
		}
	}

	fmt.Println("\n   Chunk Types:")
	for typ, count := range chunkTypes {
		fmt.Printf("      %s: %d\n", typ, count)
	}
	fmt.Printf("\n   Laravel-specific chunks:\n")
	fmt.Printf("      Models: %d\n", laravelModels)
	fmt.Printf("      Controllers: %d\n", laravelControllers)

	// Verify some chunks have Laravel metadata
	for i := range chunks {
		chunk := &chunks[i]
		if chunk.Type == "class" && chunk.Metadata != nil {
			if laravelType, ok := chunk.Metadata["laravel_type"]; ok && laravelType == "model" {
				if relationsJSON, ok := chunk.Metadata["relations"]; ok {
					var relations []EloquentRelation
					if err := json.Unmarshal([]byte(relationsJSON.(string)), &relations); err == nil && len(relations) > 0 {
						fmt.Printf("\n   ✓ Sample model with relations: %s\n", chunk.Name)
						fmt.Printf("      Package: %s\n", chunk.Package)
						fmt.Printf("      Relations: %d\n", len(relations))
						for _, rel := range relations[:min(3, len(relations))] {
							fmt.Printf("         • %s: %s -> %s\n", rel.Name, rel.Type, rel.RelatedModel)
						}
						if len(relations) > 3 {
							fmt.Printf("         ... and %d more\n", len(relations)-3)
						}
						break
					}
				}
			}
		}
	}

	fmt.Println("\n✅ Barou Project Analysis Complete!")
	fmt.Println("=" + string(make([]byte, 60)) + "=\n")

	// Assertions
	assert.Greater(t, totalModels, 0, "Should find at least one model")
	assert.Greater(t, len(chunks), 0, "Should generate chunks")
	assert.Greater(t, laravelModels, 0, "Should detect Laravel models")
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > len(substr) && (s[:len(substr)] == substr || s[len(s)-len(substr):] == substr || containsMiddle(s, substr)))
}

func containsMiddle(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

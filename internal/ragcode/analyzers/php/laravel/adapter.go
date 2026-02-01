package laravel

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"path/filepath"
	"strings"

	"github.com/doITmagic/rag-code-mcp/internal/codetypes"
	"github.com/doITmagic/rag-code-mcp/internal/ragcode/analyzers/php"
)

// Adapter implements codetypes.PathAnalyzer for Laravel projects
// It wraps the standard PHP analyzer and adds Laravel-specific analysis
type Adapter struct {
	phpAnalyzer *php.CodeAnalyzer
}

// NewAdapter creates a new Laravel analyzer adapter
func NewAdapter() *Adapter {
	return &Adapter{
		phpAnalyzer: php.NewCodeAnalyzer(),
	}
}

// AnalyzePaths implements the PathAnalyzer interface
func (a *Adapter) AnalyzePaths(paths []string) ([]codetypes.CodeChunk, error) {
	// 1. Run standard PHP analysis
	chunks, err := a.phpAnalyzer.AnalyzePaths(paths)
	if err != nil {
		return nil, err
	}

	// 2. Check if it's a Laravel project
	if !a.phpAnalyzer.IsLaravelProject() {
		return chunks, nil
	}

	// 3. Run Laravel-specific analysis for each package
	pkgs := a.phpAnalyzer.GetPackages()
	for _, pkg := range pkgs {
		analyzer := NewAnalyzer(pkg)
		info := analyzer.Analyze()

		// Enrich chunks with Laravel info
		a.enrichChunks(chunks, info)
	}

	// 4. Analyze Routes
	// We need to find route files within the provided paths
	routeFiles := a.findRouteFiles(paths)
	if len(routeFiles) > 0 {
		routeAnalyzer := NewRouteAnalyzer()
		routes, err := routeAnalyzer.Analyze(routeFiles)
		if err == nil {
			// Convert routes to chunks
			routeChunks := a.convertRoutesToChunks(routes)
			chunks = append(chunks, routeChunks...)
		}
	}

	return chunks, nil
}

func (a *Adapter) enrichChunks(chunks []codetypes.CodeChunk, info *LaravelInfo) {
	// Create lookup maps for faster access
	models := make(map[string]EloquentModel)
	for _, m := range info.Models {
		models[m.FullName] = m
	}

	controllers := make(map[string]Controller)
	for _, c := range info.Controllers {
		controllers[c.FullName] = c
	}

	// Iterate through chunks and add metadata
	for i := range chunks {
		chunk := &chunks[i]

		// We need to reconstruct the full name to match
		fullName := chunk.Name
		if chunk.Package != "" && chunk.Package != "global" {
			fullName = chunk.Package + "\\" + chunk.Name
		}

		if chunk.Type == "class" {
			if model, ok := models[fullName]; ok {
				if chunk.Metadata == nil {
					chunk.Metadata = make(map[string]any)
				}
				chunk.Metadata["laravel_type"] = "model"
				chunk.Metadata["table"] = model.Table
				chunk.Metadata["fillable"] = model.Fillable

				// Serialize relations to store in metadata
				if len(model.Relations) > 0 {
					rels, _ := json.Marshal(model.Relations)
					chunk.Metadata["relations"] = string(rels)
				}
			} else if ctrl, ok := controllers[fullName]; ok {
				if chunk.Metadata == nil {
					chunk.Metadata = make(map[string]any)
				}
				chunk.Metadata["laravel_type"] = "controller"
				chunk.Metadata["is_api"] = ctrl.IsApi
				chunk.Metadata["is_resource"] = ctrl.IsResource
			}
		}
	}
}

func (a *Adapter) findRouteFiles(paths []string) []string {
	var routeFiles []string

	// Common Laravel route files
	targets := []string{"web.php", "api.php", "console.php", "channels.php"}

	for _, root := range paths {
		// Check if root itself is a route file
		base := filepath.Base(root)
		for _, target := range targets {
			if base == target {
				routeFiles = append(routeFiles, root)
				break
			}
		}

		// If directory, look for routes folder
		// This is a heuristic. A better way would be to walk the directory structure
		// specifically looking for the 'routes' directory.
		// Check if routes dir exists
		// But 'root' might be deep inside.
		// Actually, AnalyzePaths usually receives the workspace root.

		// Let's walk the paths to find 'routes/*.php'
		_ = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return nil
			}
			if d.IsDir() {
				if d.Name() == "vendor" || d.Name() == ".git" || d.Name() == "node_modules" {
					return filepath.SkipDir
				}
				return nil
			}

			// Check if file is in a 'routes' directory
			dir := filepath.Dir(path)
			if filepath.Base(dir) == "routes" {
				name := d.Name()
				for _, target := range targets {
					if name == target {
						routeFiles = append(routeFiles, path)
						break
					}
				}
			}
			return nil
		})
	}

	return routeFiles
}

func (a *Adapter) convertRoutesToChunks(routes []Route) []codetypes.CodeChunk {
	var chunks []codetypes.CodeChunk

	for _, route := range routes {
		chunk := codetypes.CodeChunk{
			Name:      fmt.Sprintf("%s %s", route.Method, route.URI),
			Type:      "route",
			Language:  "php",
			FilePath:  route.FilePath,
			StartLine: route.Line,
			EndLine:   route.Line, // Routes are usually one line
			Signature: fmt.Sprintf("Route::%s('%s', ...)", strings.ToLower(route.Method), route.URI),
			Metadata: map[string]any{
				"method":     route.Method,
				"uri":        route.URI,
				"controller": route.Controller,
				"action":     route.Action,
				"framework":  "laravel",
			},
		}

		if route.Description != "" {
			chunk.Docstring = route.Description
		} else {
			chunk.Docstring = fmt.Sprintf("Route %s %s -> %s@%s", route.Method, route.URI, route.Controller, route.Action)
		}

		chunks = append(chunks, chunk)
	}

	return chunks
}

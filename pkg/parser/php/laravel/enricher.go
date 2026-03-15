package laravel

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/doITmagic/rag-code-mcp/internal/logger"
	"github.com/doITmagic/rag-code-mcp/pkg/parser/php"
)

// Enricher implements the php.FrameworkEnricher interface for Laravel analysis
type Enricher struct {
	adapter *Adapter
}

func init() {
	php.RegisterEnricher(&Enricher{
		adapter: NewAdapter(),
	})
}

// IsApplicable checks if the parsed paths correspond to a Laravel project.
func (e *Enricher) IsApplicable(ca *php.CodeAnalyzer, paths []string) bool {
	// 1. Quick: namespace/class-based detection from already-parsed packages
	byPackages := ca.IsLaravelProject()
	logger.Instance.Debug("[LARAVEL] IsApplicable: ca.IsLaravelProject()=%v for paths=%v", byPackages, paths)
	if byPackages {
		return true
	}

	// 2. Filesystem walk-up
	byFS := IsLaravelProjectByPaths(paths)
	logger.Instance.Debug("[LARAVEL] IsApplicable: IsLaravelProjectByPaths()=%v for paths=%v", byFS, paths)
	return byFS
}

// IsLaravelProjectByPaths walks UP parent directories from the given paths
// looking for Laravel root indicators (artisan file, composer.json with laravel/framework).
func IsLaravelProjectByPaths(paths []string) bool {
	for _, p := range paths {
		dir := p
		info, err := os.Stat(p)
		if err != nil {
			logger.Instance.Debug("[LARAVEL] IsLaravelProjectByPaths: stat error for %s: %v", p, err)
			continue
		}
		if !info.IsDir() {
			dir = filepath.Dir(p)
		}

		if isLaravelRoot(dir) {
			logger.Instance.Debug("[LARAVEL] IsLaravelProjectByPaths: FOUND Laravel root walking up from %s", dir)
			return true
		}
		logger.Instance.Debug("[LARAVEL] IsLaravelProjectByPaths: NO Laravel root found walking up from %s", dir)
	}
	return false
}

// isLaravelRoot walks UP from dir checking each parent for Laravel indicators.
func isLaravelRoot(dir string) bool {
	for {
		// Check for artisan (the strongest Laravel indicator)
		artisanPath := filepath.Join(dir, "artisan")
		if _, err := os.Stat(artisanPath); err == nil {
			logger.Instance.Debug("[LARAVEL] isLaravelRoot: FOUND artisan at %s", artisanPath)
			return true
		}

		// Check for composer.json with laravel/framework
		composerPath := filepath.Join(dir, "composer.json")
		if content, err := os.ReadFile(composerPath); err == nil {
			if strings.Contains(string(content), "laravel/framework") {
				logger.Instance.Debug("[LARAVEL] isLaravelRoot: FOUND laravel/framework in %s", composerPath)
				return true
			}
		}

		parent := filepath.Dir(dir)
		if parent == dir {
			break // reached filesystem root
		}
		dir = parent
	}
	return false
}

// Enrich receives the base PHP chunks and analyzed packages and returns chunks merged with Laravel specifics
func (e *Enricher) Enrich(ca *php.CodeAnalyzer, packages []*php.PackageInfo, paths []string, chunks []php.CodeChunk) []php.CodeChunk {
	logger.Instance.Info("[LARAVEL] Enrich: %d packages, %d paths, %d existing chunks", len(packages), len(paths), len(chunks))

	// Run Laravel-specific package analysis for Controllers and Eloquent Models
	for _, pkg := range packages {
		analyzer := NewAnalyzer(pkg)
		info := analyzer.Analyze()
		logger.Instance.Info("[LARAVEL] Enrich pkg=%s: models=%d controllers=%d", pkg.Namespace, len(info.Models), len(info.Controllers))

		// Enrich existing chunks with Laravel context (table, fillable, api routes)
		e.adapter.enrichChunks(chunks, info)
	}

	// Analyze Routes
	routeFiles := e.adapter.findRouteFiles(paths)
	logger.Instance.Info("[LARAVEL] Enrich: found %d route files from paths=%v", len(routeFiles), paths)
	if len(routeFiles) > 0 {
		routeAnalyzer := NewRouteAnalyzer()
		routes, err := routeAnalyzer.Analyze(routeFiles)
		if err == nil {
			routeChunks := e.adapter.convertRoutesToChunks(routes)
			logger.Instance.Info("[LARAVEL] Enrich: %d routes → %d chunks", len(routes), len(routeChunks))
			chunks = append(chunks, routeChunks...)
		} else {
			logger.Instance.Error("[LARAVEL] Enrich: route analysis error: %v", err)
		}
	}

	logger.Instance.Info("[LARAVEL] Enrich DONE: returning %d total chunks", len(chunks))
	return chunks
}

package laravel

import (
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

// IsApplicable checks if the parsed paths correspond to a Laravel project
func (e *Enricher) IsApplicable(ca *php.CodeAnalyzer, paths []string) bool {
	return ca.IsLaravelProject()
}

// Enrich receives the base PHP chunks and analyzed packages and returns chunks merged with Laravel specifics
func (e *Enricher) Enrich(ca *php.CodeAnalyzer, packages []*php.PackageInfo, paths []string, chunks []php.CodeChunk) []php.CodeChunk {
	// Run Laravel-specific package analysis for Controllers and Eloquent Models
	for _, pkg := range packages {
		analyzer := NewAnalyzer(pkg)
		info := analyzer.Analyze()

		// Enrich existing chunks with Laravel context (table, fillable, api routes)
		e.adapter.enrichChunks(chunks, info)
	}

	// Analyze Routes (these are handled separately since they are mostly top level closures inside routes/)
	routeFiles := e.adapter.findRouteFiles(paths)
	if len(routeFiles) > 0 {
		routeAnalyzer := NewRouteAnalyzer()
		routes, err := routeAnalyzer.Analyze(routeFiles)
		if err == nil {
			routeChunks := e.adapter.convertRoutesToChunks(routes)
			chunks = append(chunks, routeChunks...)
		}
	}

	return chunks
}

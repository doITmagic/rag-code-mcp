package wordpress

import "github.com/doITmagic/rag-code-mcp/pkg/parser/php"

// Enricher implements the php.FrameworkEnricher interface for WordPress analysis
type Enricher struct {
	analyzer *Analyzer
}

func init() {
	php.RegisterEnricher(&Enricher{
		analyzer: NewAnalyzer(),
	})
}

// IsApplicable checks if the parsed paths correspond to a WordPress project
func (e *Enricher) IsApplicable(ca *php.CodeAnalyzer, paths []string) bool {
	return IsWordPressProject(paths)
}

// Enrich receives the base PHP chunks and analyzed packages and returns chunks merged with WordPress specifics
func (e *Enricher) Enrich(ca *php.CodeAnalyzer, packages []*php.PackageInfo, paths []string, chunks []php.CodeChunk) []php.CodeChunk {
	// Reusing logic from wordpress.Analyzer
	wpInfo := e.analyzer.analyzeWordPress(packages, paths)

	// Enrich existing basic PHP chunks with WP info (e.g. marking Widgets)
	e.analyzer.enrichChunks(chunks, wpInfo)

	// Extract new specific chunks like Hooks, Blocks, Shortcodes, CPTs
	wpChunks := e.analyzer.convertToChunks(wpInfo)
	
	return append(chunks, wpChunks...)
}

package wordpress

import (
	"github.com/doITmagic/rag-code-mcp/internal/logger"
	"github.com/doITmagic/rag-code-mcp/pkg/parser/php"
)

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
	result := IsWordPressProject(paths)
	logger.Instance.Debug("[WORDPRESS] IsApplicable=%v for paths=%v", result, paths)
	return result
}

// Enrich receives the base PHP chunks and analyzed packages and returns chunks merged with WordPress specifics
func (e *Enricher) Enrich(ca *php.CodeAnalyzer, packages []*php.PackageInfo, paths []string, chunks []php.CodeChunk) []php.CodeChunk {
	logger.Instance.Debug("[WORDPRESS] Enrich: %d packages, %d paths, %d existing chunks", len(packages), len(paths), len(chunks))

	// Reusing logic from wordpress.Analyzer
	wpInfo := e.analyzer.analyzeWordPress(packages, paths)
	logger.Instance.Debug("[WORDPRESS] Enrich: hooks=%d, shortcodes=%d, widgets=%d, blocks=%d, postTypes=%d",
		len(wpInfo.Hooks), len(wpInfo.Shortcodes), len(wpInfo.Widgets), len(wpInfo.Blocks), len(wpInfo.PostTypes))

	// Enrich existing basic PHP chunks with WP info (e.g. marking Widgets)
	e.analyzer.enrichChunks(chunks, wpInfo)

	// Extract new specific chunks like Hooks, Blocks, Shortcodes, CPTs
	wpChunks := e.analyzer.convertToChunks(wpInfo)
	logger.Instance.Debug("[WORDPRESS] Enrich DONE: %d base + %d wp = %d total chunks", len(chunks), len(wpChunks), len(chunks)+len(wpChunks))

	return append(chunks, wpChunks...)
}

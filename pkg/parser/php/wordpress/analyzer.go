package wordpress

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/VKCOM/php-parser/pkg/ast"
	"github.com/VKCOM/php-parser/pkg/conf"
	"github.com/VKCOM/php-parser/pkg/errors"
	"github.com/VKCOM/php-parser/pkg/parser"
	"github.com/VKCOM/php-parser/pkg/version"

	pkgParser "github.com/doITmagic/rag-code-mcp/pkg/parser"
	"github.com/doITmagic/rag-code-mcp/pkg/parser/php"
)

// Analyzer is the main WordPress framework analyzer that coordinates all WordPress-specific analyzers
type Analyzer struct {
	hookAnalyzer         *HookAnalyzer
	postTypeAnalyzer     *PostTypeAnalyzer
	shortcodeAnalyzer    *ShortcodeAnalyzer
	blockAnalyzer        *BlockAnalyzer
	widgetAnalyzer       *WidgetAnalyzer
	adminAnalyzer        *AdminAnalyzer
	pluginHeaderAnalyzer *PluginHeaderAnalyzer
	phpAnalyzer          *php.CodeAnalyzer
}

// NewAnalyzer creates a new WordPress framework analyzer
func NewAnalyzer() *Analyzer {
	return &Analyzer{
		hookAnalyzer:         NewHookAnalyzer(),
		postTypeAnalyzer:     NewPostTypeAnalyzer(),
		shortcodeAnalyzer:    NewShortcodeAnalyzer(),
		blockAnalyzer:        NewBlockAnalyzer(),
		widgetAnalyzer:       NewWidgetAnalyzer(),
		adminAnalyzer:        NewAdminAnalyzer(),
		pluginHeaderAnalyzer: NewPluginHeaderAnalyzer(),
		phpAnalyzer:          php.NewCodeAnalyzer(),
	}
}

// AnalyzePaths performs WordPress-specific analysis on the given paths.
// It first runs the standard PHP analysis, then adds WordPress-specific analysis.
func (a *Analyzer) AnalyzePaths(paths []string) ([]php.CodeChunk, error) {
	// 1. Run standard PHP analysis
	chunks, err := a.phpAnalyzer.AnalyzePaths(paths)
	if err != nil {
		return nil, err
	}

	// 2. If not a WordPress project, return standard PHP chunks
	if !IsWordPressProject(paths) {
		return chunks, nil
	}

	// 3. Run WordPress-specific analysis
	packages := a.phpAnalyzer.GetPackages()
	wpInfo := a.analyzeWordPress(packages, paths)

	// 4. Enrich existing chunks with WordPress metadata
	a.enrichChunks(chunks, wpInfo)

	// 5. Add WordPress-specific chunks (hooks, post types, etc.)
	wpChunks := a.convertToChunks(wpInfo)
	chunks = append(chunks, wpChunks...)

	return chunks, nil
}

// analyzeWordPress performs complete WordPress analysis using both package info and AST
func (a *Analyzer) analyzeWordPress(packages []*php.PackageInfo, paths []string) *WordPressInfo {
	info := &WordPressInfo{}

	// Analyze from parsed package info (method calls)
	info.Hooks = a.hookAnalyzer.AnalyzeHooks(packages)
	info.PostTypes = a.postTypeAnalyzer.AnalyzePostTypesFromPackages(packages)
	info.Taxonomies = a.postTypeAnalyzer.AnalyzeTaxonomiesFromPackages(packages)
	info.Widgets = a.widgetAnalyzer.AnalyzeWidgets(packages)

	// Walk files for additional AST-based analysis (top-level calls, headers)
	for _, root := range paths {
		_ = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return nil
			}
			if d.IsDir() {
				base := filepath.Base(path)
				if base == ".git" || base == "vendor" || base == "node_modules" {
					return filepath.SkipDir
				}
				return nil
			}
			if !strings.HasSuffix(d.Name(), ".php") {
				return nil
			}

			content, err := os.ReadFile(path)
			if err != nil {
				return nil
			}

			// Parse for AST-based analysis
			rootNode, err := a.parsePHP(content)
			if err != nil {
				return nil
			}

			// Hooks from AST (catches top-level calls missed by package analysis)
			astHooks := a.hookAnalyzer.AnalyzeHooksFromAST(rootNode, path)
			info.Hooks = mergeHooks(info.Hooks, astHooks)

			// Post types and taxonomies from AST
			astPostTypes := a.postTypeAnalyzer.AnalyzePostTypes(rootNode, path)
			info.PostTypes = mergePostTypes(info.PostTypes, astPostTypes)
			astTaxonomies := a.postTypeAnalyzer.AnalyzeTaxonomies(rootNode, path)
			info.Taxonomies = mergeTaxonomies(info.Taxonomies, astTaxonomies)

			// Shortcodes
			shortcodes := a.shortcodeAnalyzer.AnalyzeShortcodes(rootNode, path)
			info.Shortcodes = append(info.Shortcodes, shortcodes...)

			// Blocks
			blocks := a.blockAnalyzer.AnalyzeBlocks(rootNode, path)
			info.Blocks = append(info.Blocks, blocks...)
			patterns := a.blockAnalyzer.AnalyzeBlockPatterns(rootNode, path)
			info.BlockPatterns = append(info.BlockPatterns, patterns...)

			// Admin pages and settings
			adminPages := a.adminAnalyzer.AnalyzeAdminPages(rootNode, path)
			info.AdminPages = append(info.AdminPages, adminPages...)
			settings := a.adminAnalyzer.AnalyzeSettings(rootNode, path)
			info.Settings = append(info.Settings, settings...)

			// Plugin header (only first match)
			if info.PluginHeader == nil {
				header := a.pluginHeaderAnalyzer.AnalyzeHeader(content, path)
				if header != nil {
					info.PluginHeader = header
				}
			}

			return nil
		})
	}

	return info
}

// enrichChunks adds WordPress metadata to existing PHP chunks
func (a *Analyzer) enrichChunks(chunks []php.CodeChunk, info *WordPressInfo) {
	// Build lookup map for widgets by full name
	widgetNames := make(map[string]bool)
	for _, w := range info.Widgets {
		widgetNames[w.FullName] = true
	}

	for i := range chunks {
		chunk := &chunks[i]
		if chunk.Type != "class" {
			continue
		}

		fullName := chunk.Name
		if chunk.Package != "" && chunk.Package != "global" {
			fullName = chunk.Package + "\\" + chunk.Name
		}

		if widgetNames[fullName] {
			if chunk.Metadata == nil {
				chunk.Metadata = make(map[string]any)
			}
			chunk.Metadata["framework"] = "wordpress"
			chunk.Metadata["wp_type"] = "widget"
		}
	}
}

// convertToChunks converts WordPress info to code chunks for indexing
func (a *Analyzer) convertToChunks(info *WordPressInfo) []php.CodeChunk {
	var chunks []php.CodeChunk

	// Convert hooks
	for _, hook := range info.Hooks {
		chunk := php.CodeChunk{
			Name:      fmt.Sprintf("%s(%s)", hook.Type, hook.Name),
			Type:      "wp_hook",
			Language:  "php",
			FilePath:  hook.FilePath,
			StartLine: hook.StartLine,
			EndLine:   hook.EndLine,
			Signature: buildHookSignature(hook),
			Docstring: fmt.Sprintf("WordPress %s hook: %s", hook.Type, hook.Name),
			Metadata: map[string]any{
				"framework": "wordpress",
				"hook_type": string(hook.Type),
				"hook_name": hook.Name,
				"callback":  hook.Callback,
				"priority":  hook.Priority,
			},
		}
		if hook.Callback != "" {
			chunk.Relations = append(chunk.Relations, pkgParser.Relation{
				TargetName: hook.Callback,
				Type:       pkgParser.RelCalls,
			})
		}
		chunks = append(chunks, chunk)
	}

	// Convert post types
	for _, pt := range info.PostTypes {
		chunks = append(chunks, php.CodeChunk{
			Name:      pt.Name,
			Type:      "wp_post_type",
			Language:  "php",
			FilePath:  pt.FilePath,
			StartLine: pt.StartLine,
			EndLine:   pt.EndLine,
			Signature: fmt.Sprintf("register_post_type('%s', ...)", pt.Name),
			Docstring: fmt.Sprintf("WordPress Custom Post Type: %s", pt.Name),
			Metadata: map[string]any{
				"framework": "wordpress",
				"wp_type":   "post_type",
			},
		})
	}

	// Convert taxonomies
	for _, tax := range info.Taxonomies {
		chunk := php.CodeChunk{
			Name:      tax.Name,
			Type:      "wp_taxonomy",
			Language:  "php",
			FilePath:  tax.FilePath,
			StartLine: tax.StartLine,
			EndLine:   tax.EndLine,
			Signature: fmt.Sprintf("register_taxonomy('%s', ...)", tax.Name),
			Docstring: fmt.Sprintf("WordPress Custom Taxonomy: %s", tax.Name),
			Metadata: map[string]any{
				"framework":  "wordpress",
				"wp_type":    "taxonomy",
				"post_types": tax.PostTypes,
			},
		}
		// Relate taxonomy to its post types
		for _, ptName := range tax.PostTypes {
			chunk.Relations = append(chunk.Relations, pkgParser.Relation{
				TargetName: ptName,
				Type:       pkgParser.RelCalls,
			})
		}
		chunks = append(chunks, chunk)
	}

	// Convert shortcodes
	for _, sc := range info.Shortcodes {
		chunks = append(chunks, php.CodeChunk{
			Name:      sc.Tag,
			Type:      "wp_shortcode",
			Language:  "php",
			FilePath:  sc.FilePath,
			StartLine: sc.StartLine,
			EndLine:   sc.EndLine,
			Signature: fmt.Sprintf("add_shortcode('%s', '%s')", sc.Tag, sc.Callback),
			Docstring: fmt.Sprintf("WordPress Shortcode: [%s]", sc.Tag),
			Metadata: map[string]any{
				"framework": "wordpress",
				"wp_type":   "shortcode",
				"callback":  sc.Callback,
			},
		})
	}

	// Convert blocks
	for _, block := range info.Blocks {
		chunks = append(chunks, php.CodeChunk{
			Name:      block.Name,
			Type:      "wp_block",
			Language:  "php",
			FilePath:  block.FilePath,
			StartLine: block.StartLine,
			EndLine:   block.EndLine,
			Signature: fmt.Sprintf("register_block_type('%s', ...)", block.Name),
			Docstring: fmt.Sprintf("Gutenberg Block: %s", block.Name),
			Metadata: map[string]any{
				"framework": "wordpress",
				"wp_type":   "block",
			},
		})
	}

	// Convert admin pages
	for _, page := range info.AdminPages {
		pageType := "menu_page"
		if page.IsSubmenu {
			pageType = "submenu_page"
		}
		chunks = append(chunks, php.CodeChunk{
			Name:      page.MenuSlug,
			Type:      "wp_admin_page",
			Language:  "php",
			FilePath:  page.FilePath,
			StartLine: page.StartLine,
			EndLine:   page.EndLine,
			Signature: fmt.Sprintf("add_%s('%s')", pageType, page.MenuSlug),
			Docstring: fmt.Sprintf("WordPress Admin Page: %s (%s)", page.PageTitle, pageType),
			Metadata: map[string]any{
				"framework":  "wordpress",
				"wp_type":    "admin_page",
				"page_title": page.PageTitle,
				"menu_title": page.MenuTitle,
				"capability": page.Capability,
				"is_submenu": page.IsSubmenu,
				"parent":     page.Parent,
			},
		})
	}

	// Convert plugin header
	if info.PluginHeader != nil {
		h := info.PluginHeader
		headerType := "plugin"
		if h.IsTheme {
			headerType = "theme"
		}
		chunks = append(chunks, php.CodeChunk{
			Name:      h.Name,
			Type:      "wp_" + headerType,
			Language:  "php",
			FilePath:  h.FilePath,
			Docstring: fmt.Sprintf("WordPress %s: %s v%s by %s", headerType, h.Name, h.Version, h.Author),
			Metadata: map[string]any{
				"framework":   "wordpress",
				"wp_type":     headerType + "_header",
				"version":     h.Version,
				"author":      h.Author,
				"text_domain": h.TextDomain,
				"description": h.Description,
			},
		})
	}

	return chunks
}

// parsePHP parses PHP source code and returns the AST root
func (a *Analyzer) parsePHP(content []byte) (ast.Vertex, error) {
	rootNode, err := parser.Parse(content, conf.Config{
		Version: &version.Version{Major: 8, Minor: 0},
		ErrorHandlerFunc: func(e *errors.Error) {
			// Ignore parser errors for analysis
		},
	})
	if err != nil {
		return nil, err
	}
	return rootNode, nil
}

// buildHookSignature creates a readable signature for a hook
func buildHookSignature(hook WPHook) string {
	switch hook.Type {
	case HookAction:
		if hook.Priority > 0 {
			return fmt.Sprintf("add_action('%s', '%s', %d, %d)", hook.Name, hook.Callback, hook.Priority, hook.AcceptedArgs)
		}
		return fmt.Sprintf("add_action('%s', '%s')", hook.Name, hook.Callback)
	case HookFilter:
		if hook.Priority > 0 {
			return fmt.Sprintf("add_filter('%s', '%s', %d, %d)", hook.Name, hook.Callback, hook.Priority, hook.AcceptedArgs)
		}
		return fmt.Sprintf("add_filter('%s', '%s')", hook.Name, hook.Callback)
	case HookActionTrigger:
		return fmt.Sprintf("do_action('%s')", hook.Name)
	case HookFilterTrigger:
		return fmt.Sprintf("apply_filters('%s')", hook.Name)
	case HookActionRemoval:
		return fmt.Sprintf("remove_action('%s', '%s')", hook.Name, hook.Callback)
	case HookFilterRemoval:
		return fmt.Sprintf("remove_filter('%s', '%s')", hook.Name, hook.Callback)
	default:
		return fmt.Sprintf("%s('%s')", hook.Type, hook.Name)
	}
}

// IsWordPressProject detects if the given paths contain a WordPress project
func IsWordPressProject(paths []string) bool {
	for _, root := range paths {
		info, err := os.Stat(root)
		if err != nil {
			continue
		}

		if !info.IsDir() {
			// Check single file for plugin/theme header
			content, err := os.ReadFile(root)
			if err == nil {
				analyzer := NewPluginHeaderAnalyzer()
				if header := analyzer.AnalyzeHeader(content, root); header != nil {
					return true
				}
			}
			continue
		}

		// Check for WordPress indicator files
		wpIndicators := []string{
			"wp-config.php",
			"wp-content",
			"wp-includes",
			"wp-admin",
		}
		for _, indicator := range wpIndicators {
			if _, err := os.Stat(filepath.Join(root, indicator)); err == nil {
				return true
			}
		}

		// Walk for plugin/theme headers
		found := false
		_ = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
			if err != nil || found {
				return filepath.SkipAll
			}
			if d.IsDir() {
				base := filepath.Base(path)
				if base == "vendor" || base == "node_modules" || base == ".git" {
					return filepath.SkipDir
				}
				return nil
			}
			if !strings.HasSuffix(d.Name(), ".php") {
				return nil
			}
			content, err := os.ReadFile(path)
			if err != nil {
				return nil
			}
			analyzer := NewPluginHeaderAnalyzer()
			if header := analyzer.AnalyzeHeader(content, path); header != nil {
				found = true
				return filepath.SkipAll
			}
			return nil
		})
		if found {
			return true
		}
	}

	return false
}

// merge helpers to deduplicate when both package-level and AST-level analysis find the same items

func mergeHooks(existing, additional []WPHook) []WPHook {
	seen := make(map[string]bool)
	for _, h := range existing {
		key := fmt.Sprintf("%s:%s:%s:%d", h.FilePath, h.Type, h.Name, h.StartLine)
		seen[key] = true
	}
	for _, h := range additional {
		key := fmt.Sprintf("%s:%s:%s:%d", h.FilePath, h.Type, h.Name, h.StartLine)
		if !seen[key] {
			existing = append(existing, h)
			seen[key] = true
		}
	}
	return existing
}

func mergePostTypes(existing, additional []PostType) []PostType {
	seen := make(map[string]bool)
	for _, pt := range existing {
		key := fmt.Sprintf("%s:%s:%d", pt.FilePath, pt.Name, pt.StartLine)
		seen[key] = true
	}
	for _, pt := range additional {
		key := fmt.Sprintf("%s:%s:%d", pt.FilePath, pt.Name, pt.StartLine)
		if !seen[key] {
			existing = append(existing, pt)
			seen[key] = true
		}
	}
	return existing
}

func mergeTaxonomies(existing, additional []Taxonomy) []Taxonomy {
	seen := make(map[string]bool)
	for _, t := range existing {
		key := fmt.Sprintf("%s:%s:%d", t.FilePath, t.Name, t.StartLine)
		seen[key] = true
	}
	for _, t := range additional {
		key := fmt.Sprintf("%s:%s:%d", t.FilePath, t.Name, t.StartLine)
		if !seen[key] {
			existing = append(existing, t)
			seen[key] = true
		}
	}
	return existing
}

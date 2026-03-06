package wordpress

import (
	"regexp"
	"strings"
)

// pluginHeaderRegex matches WordPress plugin/theme header fields
var pluginHeaderRegex = regexp.MustCompile(`(?i)^\s*\*?\s*(Plugin Name|Theme Name|Plugin URI|Description|Version|Author|Author URI|Text Domain|Domain Path|License|License URI|Requires at least|Tested up to|Requires PHP):\s*(.+)$`)

// PluginHeaderAnalyzer extracts WordPress plugin or theme metadata from file header comment
type PluginHeaderAnalyzer struct{}

// NewPluginHeaderAnalyzer creates a new plugin header analyzer
func NewPluginHeaderAnalyzer() *PluginHeaderAnalyzer {
	return &PluginHeaderAnalyzer{}
}

// AnalyzeHeader extracts plugin/theme metadata from the first PHP comment block in source code.
// WordPress plugin headers are DocBlock-style comments at the very top of the main plugin file.
func (a *PluginHeaderAnalyzer) AnalyzeHeader(source []byte, filePath string) *PluginHeader {
	content := string(source)

	// Find the first PHP comment block (/* ... */ or /** ... */)
	startIdx := strings.Index(content, "/*")
	if startIdx == -1 {
		return nil
	}
	endIdx := strings.Index(content[startIdx:], "*/")
	if endIdx == -1 {
		return nil
	}

	comment := content[startIdx : startIdx+endIdx+2]

	// Extract fields from comment
	header := &PluginHeader{
		FilePath: filePath,
	}

	lines := strings.Split(comment, "\n")
	for _, line := range lines {
		matches := pluginHeaderRegex.FindStringSubmatch(line)
		if len(matches) < 3 {
			continue
		}

		key := strings.ToLower(strings.TrimSpace(matches[1]))
		value := strings.TrimSpace(matches[2])

		switch key {
		case "plugin name":
			header.Name = value
		case "theme name":
			header.Name = value
			header.IsTheme = true
		case "plugin uri":
			header.PluginURI = value
		case "description":
			header.Description = value
		case "version":
			header.Version = value
		case "author":
			header.Author = value
		case "author uri":
			header.AuthorURI = value
		case "text domain":
			header.TextDomain = value
		case "domain path":
			header.DomainPath = value
		case "license":
			header.License = value
		}
	}

	// Only return if we found a name (Plugin Name or Theme Name is required)
	if header.Name == "" {
		return nil
	}

	return header
}

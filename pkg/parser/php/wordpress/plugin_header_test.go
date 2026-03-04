package wordpress

import (
	"testing"
)

func TestPluginHeaderAnalyzer_PluginHeader(t *testing.T) {
	source := `<?php
/**
 * Plugin Name: My Awesome Plugin
 * Plugin URI: https://example.com/plugin
 * Description: A great WordPress plugin
 * Version: 1.2.3
 * Author: Razvan
 * Author URI: https://example.com
 * Text Domain: my-awesome-plugin
 * Domain Path: /languages
 * License: GPL-2.0+
 */

add_action('init', 'my_init');
`
	analyzer := NewPluginHeaderAnalyzer()
	header := analyzer.AnalyzeHeader([]byte(source), "my-plugin.php")

	if header == nil {
		t.Fatal("expected plugin header, got nil")
	}
	if header.Name != "My Awesome Plugin" {
		t.Errorf("expected name 'My Awesome Plugin', got '%s'", header.Name)
	}
	if header.PluginURI != "https://example.com/plugin" {
		t.Errorf("expected plugin URI 'https://example.com/plugin', got '%s'", header.PluginURI)
	}
	if header.Description != "A great WordPress plugin" {
		t.Errorf("expected description, got '%s'", header.Description)
	}
	if header.Version != "1.2.3" {
		t.Errorf("expected version '1.2.3', got '%s'", header.Version)
	}
	if header.Author != "Razvan" {
		t.Errorf("expected author 'Razvan', got '%s'", header.Author)
	}
	if header.AuthorURI != "https://example.com" {
		t.Errorf("expected author URI 'https://example.com', got '%s'", header.AuthorURI)
	}
	if header.TextDomain != "my-awesome-plugin" {
		t.Errorf("expected text domain 'my-awesome-plugin', got '%s'", header.TextDomain)
	}
	if header.DomainPath != "/languages" {
		t.Errorf("expected domain path '/languages', got '%s'", header.DomainPath)
	}
	if header.License != "GPL-2.0+" {
		t.Errorf("expected license 'GPL-2.0+', got '%s'", header.License)
	}
	if header.IsTheme {
		t.Error("expected IsTheme=false for plugin")
	}
	if header.FilePath != "my-plugin.php" {
		t.Errorf("expected file path 'my-plugin.php', got '%s'", header.FilePath)
	}
}

func TestPluginHeaderAnalyzer_ThemeHeader(t *testing.T) {
	source := `<?php
/**
 * Theme Name: My Theme
 * Version: 2.0.0
 * Author: Designer
 * Text Domain: my-theme
 */
`
	analyzer := NewPluginHeaderAnalyzer()
	header := analyzer.AnalyzeHeader([]byte(source), "style.css")

	if header == nil {
		t.Fatal("expected theme header, got nil")
	}
	if header.Name != "My Theme" {
		t.Errorf("expected name 'My Theme', got '%s'", header.Name)
	}
	if !header.IsTheme {
		t.Error("expected IsTheme=true for theme")
	}
	if header.Version != "2.0.0" {
		t.Errorf("expected version '2.0.0', got '%s'", header.Version)
	}
}

func TestPluginHeaderAnalyzer_NoHeader(t *testing.T) {
	source := `<?php
// Just some PHP code
add_action('init', 'setup');
`
	analyzer := NewPluginHeaderAnalyzer()
	header := analyzer.AnalyzeHeader([]byte(source), "test.php")

	if header != nil {
		t.Errorf("expected nil header, got %+v", header)
	}
}

func TestPluginHeaderAnalyzer_CommentWithoutPluginName(t *testing.T) {
	source := `<?php
/**
 * This is just a regular PHP doc comment
 * It doesn't have Plugin Name or Theme Name
 */
function my_function() {}
`
	analyzer := NewPluginHeaderAnalyzer()
	header := analyzer.AnalyzeHeader([]byte(source), "test.php")

	if header != nil {
		t.Errorf("expected nil for non-plugin comment, got %+v", header)
	}
}

func TestPluginHeaderAnalyzer_MinimalHeader(t *testing.T) {
	source := `<?php
/**
 * Plugin Name: Minimal Plugin
 */
`
	analyzer := NewPluginHeaderAnalyzer()
	header := analyzer.AnalyzeHeader([]byte(source), "minimal.php")

	if header == nil {
		t.Fatal("expected header, got nil")
	}
	if header.Name != "Minimal Plugin" {
		t.Errorf("expected 'Minimal Plugin', got '%s'", header.Name)
	}
	// All other fields should be empty
	if header.Version != "" {
		t.Errorf("expected empty version, got '%s'", header.Version)
	}
	if header.Author != "" {
		t.Errorf("expected empty author, got '%s'", header.Author)
	}
}

func TestPluginHeaderAnalyzer_NoComment(t *testing.T) {
	source := `<?php
echo "hello world";
`
	analyzer := NewPluginHeaderAnalyzer()
	header := analyzer.AnalyzeHeader([]byte(source), "test.php")

	if header != nil {
		t.Errorf("expected nil, got %+v", header)
	}
}

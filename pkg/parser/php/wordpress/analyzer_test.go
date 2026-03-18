package wordpress

import (
	"os"
	"path/filepath"
	"testing"
)

func TestAnalyzer_FullPluginAnalysis(t *testing.T) {
	// Create a temp directory with a sample WordPress plugin
	tmpDir := t.TempDir()

	// Main plugin file with header
	pluginFile := filepath.Join(tmpDir, "my-plugin.php")
	err := os.WriteFile(pluginFile, []byte(`<?php
/**
 * Plugin Name: Test Plugin
 * Version: 1.0.0
 * Author: TestAuthor
 * Text Domain: test-plugin
 */

add_action('init', 'my_init_function');
add_action('wp_enqueue_scripts', 'enqueue_my_assets');
add_filter('the_content', 'modify_content');

function my_init_function() {
    register_post_type('event', array('public' => true));
    register_taxonomy('event_type', 'event', array('hierarchical' => true));
    add_shortcode('event-list', 'render_event_list');
}
`), 0644)
	if err != nil {
		t.Fatal(err)
	}

	// Admin file
	adminFile := filepath.Join(tmpDir, "admin.php")
	err = os.WriteFile(adminFile, []byte(`<?php
function setup_admin_menu() {
    add_menu_page('Events', 'Events', 'manage_options', 'event-manager', 'render_events_page');
    add_submenu_page('event-manager', 'Settings', 'Settings', 'manage_options', 'event-settings', 'render_event_settings');
    register_setting('event_options', 'events_per_page');
}
`), 0644)
	if err != nil {
		t.Fatal(err)
	}

	// Blocks file
	blocksFile := filepath.Join(tmpDir, "blocks.php")
	err = os.WriteFile(blocksFile, []byte(`<?php
function register_event_blocks() {
    register_block_type('test-plugin/event-card', array(
        'render_callback' => 'render_event_card',
    ));
    register_block_pattern('test-plugin/event-grid', array(
        'title' => 'Event Grid',
    ));
}
`), 0644)
	if err != nil {
		t.Fatal(err)
	}

	// Widget file
	widgetFile := filepath.Join(tmpDir, "widget.php")
	err = os.WriteFile(widgetFile, []byte(`<?php
class EventWidget extends WP_Widget {
    public function __construct() {
        parent::__construct('event_widget', 'Event Widget');
    }

    public function widget($args, $instance) {
        echo "Events";
    }
}
`), 0644)
	if err != nil {
		t.Fatal(err)
	}

	// Run full analysis
	analyzer := NewAnalyzer()
	chunks, err := analyzer.AnalyzePaths([]string{tmpDir})
	if err != nil {
		t.Fatalf("AnalyzePaths failed: %v", err)
	}

	if len(chunks) == 0 {
		t.Fatal("expected chunks, got 0")
	}

	// Count by type
	typeCounts := make(map[string]int)
	for _, chunk := range chunks {
		typeCounts[chunk.Type]++
	}

	// Verify we found WordPress-specific chunks
	if typeCounts["wp_hook"] == 0 {
		t.Error("expected wp_hook chunks")
	}
	if typeCounts["wp_post_type"] == 0 {
		t.Error("expected wp_post_type chunks")
	}
	if typeCounts["wp_taxonomy"] == 0 {
		t.Error("expected wp_taxonomy chunks")
	}
	if typeCounts["wp_shortcode"] == 0 {
		t.Error("expected wp_shortcode chunks")
	}
	if typeCounts["wp_block"] == 0 {
		t.Error("expected wp_block chunks")
	}
	if typeCounts["wp_admin_page"] == 0 {
		t.Error("expected wp_admin_page chunks")
	}
	if typeCounts["wp_plugin"] == 0 {
		t.Error("expected wp_plugin chunks")
	}

	// Verify WordPress metadata on enriched chunks
	for _, chunk := range chunks {
		if chunk.Type == "wp_hook" || chunk.Type == "wp_post_type" ||
			chunk.Type == "wp_taxonomy" || chunk.Type == "wp_shortcode" ||
			chunk.Type == "wp_block" || chunk.Type == "wp_admin_page" ||
			chunk.Type == "wp_plugin" {
			if chunk.Metadata == nil {
				t.Errorf("chunk %s has nil metadata", chunk.Name)
				continue
			}
			if fw, ok := chunk.Metadata["framework"].(string); !ok || fw != "wordpress" {
				t.Errorf("chunk %s: expected framework=wordpress, got %v", chunk.Name, chunk.Metadata["framework"])
			}
		}
	}

	t.Logf("Total chunks: %d, type breakdown: %v", len(chunks), typeCounts)
}

func TestAnalyzer_NotWordPressProject(t *testing.T) {
	// Create a temp directory with a plain PHP file (no WP indicators)
	tmpDir := t.TempDir()

	phpFile := filepath.Join(tmpDir, "index.php")
	err := os.WriteFile(phpFile, []byte(`<?php
function hello() {
    echo "Hello World";
}
`), 0644)
	if err != nil {
		t.Fatal(err)
	}

	analyzer := NewAnalyzer()
	chunks, err := analyzer.AnalyzePaths([]string{tmpDir})
	if err != nil {
		t.Fatalf("AnalyzePaths failed: %v", err)
	}

	// Should still return standard PHP chunks
	if len(chunks) == 0 {
		t.Error("expected at least PHP chunks")
	}

	// Should NOT have WordPress-specific chunks
	for _, chunk := range chunks {
		if chunk.Type == "wp_hook" || chunk.Type == "wp_post_type" {
			t.Errorf("unexpected WordPress chunk type: %s", chunk.Type)
		}
	}
}

func TestIsWordPressProject_WithWpConfig(t *testing.T) {
	tmpDir := t.TempDir()

	// Create wp-config.php indicator
	err := os.WriteFile(filepath.Join(tmpDir, "wp-config.php"), []byte(`<?php
define('DB_NAME', 'wordpress');
`), 0644)
	if err != nil {
		t.Fatal(err)
	}

	if !IsWordPressProject([]string{tmpDir}) {
		t.Error("expected true for project with wp-config.php")
	}
}

func TestIsWordPressProject_WithPluginHeader(t *testing.T) {
	tmpDir := t.TempDir()

	err := os.WriteFile(filepath.Join(tmpDir, "plugin.php"), []byte(`<?php
/**
 * Plugin Name: My Plugin
 */
`), 0644)
	if err != nil {
		t.Fatal(err)
	}

	if !IsWordPressProject([]string{tmpDir}) {
		t.Error("expected true for project with plugin header")
	}
}

func TestIsWordPressProject_NotWordPress(t *testing.T) {
	tmpDir := t.TempDir()

	err := os.WriteFile(filepath.Join(tmpDir, "index.php"), []byte(`<?php
echo "hello";
`), 0644)
	if err != nil {
		t.Fatal(err)
	}

	if IsWordPressProject([]string{tmpDir}) {
		t.Error("expected false for non-WordPress project")
	}
}

func TestIsWordPressProject_SinglePluginFile(t *testing.T) {
	tmpDir := t.TempDir()

	pluginFile := filepath.Join(tmpDir, "my-plugin.php")
	err := os.WriteFile(pluginFile, []byte(`<?php
/**
 * Plugin Name: Direct Plugin
 * Version: 1.0
 */
`), 0644)
	if err != nil {
		t.Fatal(err)
	}

	if !IsWordPressProject([]string{pluginFile}) {
		t.Error("expected true for single plugin file")
	}
}

func TestBuildHookSignature(t *testing.T) {
	tests := []struct {
		hook     WPHook
		expected string
	}{
		{
			hook:     WPHook{Type: HookAction, Name: "init", Callback: "my_init"},
			expected: "add_action('init', 'my_init')",
		},
		{
			hook:     WPHook{Type: HookAction, Name: "init", Callback: "my_init", Priority: 20, AcceptedArgs: 2},
			expected: "add_action('init', 'my_init', 20, 2)",
		},
		{
			hook:     WPHook{Type: HookFilter, Name: "the_content", Callback: "filter"},
			expected: "add_filter('the_content', 'filter')",
		},
		{
			hook:     WPHook{Type: HookActionTrigger, Name: "my_hook"},
			expected: "do_action('my_hook')",
		},
		{
			hook:     WPHook{Type: HookFilterTrigger, Name: "my_filter"},
			expected: "apply_filters('my_filter')",
		},
		{
			hook:     WPHook{Type: HookActionRemoval, Name: "wp_head", Callback: "wp_generator"},
			expected: "remove_action('wp_head', 'wp_generator')",
		},
		{
			hook:     WPHook{Type: HookFilterRemoval, Name: "the_content", Callback: "wpautop"},
			expected: "remove_filter('the_content', 'wpautop')",
		},
	}

	for _, tt := range tests {
		result := buildHookSignature(tt.hook)
		if result != tt.expected {
			t.Errorf("buildHookSignature(%+v) = %q, want %q", tt.hook, result, tt.expected)
		}
	}
}

func TestConvertToChunks_Hooks(t *testing.T) {
	analyzer := NewAnalyzer()

	info := &WordPressInfo{
		Hooks: []WPHook{
			{Type: HookAction, Name: "init", Callback: "my_init", FilePath: "test.php", StartLine: 1, EndLine: 1},
			{Type: HookFilter, Name: "the_content", Callback: "filter_fn", FilePath: "test.php", StartLine: 2, EndLine: 2},
		},
	}

	chunks := analyzer.convertToChunks(info)
	if len(chunks) != 2 {
		t.Fatalf("expected 2 chunks, got %d", len(chunks))
	}

	// Verify hook chunk metadata
	for _, chunk := range chunks {
		if chunk.Type != "wp_hook" {
			t.Errorf("expected type wp_hook, got %s", chunk.Type)
		}
		if chunk.Language != "php" {
			t.Errorf("expected language php, got %s", chunk.Language)
		}
		if chunk.Metadata["framework"] != "wordpress" {
			t.Errorf("expected framework=wordpress")
		}
	}
}

func TestConvertToChunks_PostTypes(t *testing.T) {
	analyzer := NewAnalyzer()

	info := &WordPressInfo{
		PostTypes: []PostType{
			{Name: "book", FilePath: "test.php", StartLine: 1},
		},
	}

	chunks := analyzer.convertToChunks(info)
	if len(chunks) != 1 {
		t.Fatalf("expected 1 chunk, got %d", len(chunks))
	}
	if chunks[0].Type != "wp_post_type" {
		t.Errorf("expected type wp_post_type, got %s", chunks[0].Type)
	}
	if chunks[0].Name != "book" {
		t.Errorf("expected name 'book', got '%s'", chunks[0].Name)
	}
}

func TestConvertToChunks_PluginHeader(t *testing.T) {
	analyzer := NewAnalyzer()

	info := &WordPressInfo{
		PluginHeader: &PluginHeader{
			Name:     "My Plugin",
			Version:  "1.0.0",
			Author:   "Razvan",
			FilePath: "my-plugin.php",
		},
	}

	chunks := analyzer.convertToChunks(info)
	if len(chunks) != 1 {
		t.Fatalf("expected 1 chunk, got %d", len(chunks))
	}
	if chunks[0].Type != "wp_plugin" {
		t.Errorf("expected type wp_plugin, got %s", chunks[0].Type)
	}
	if chunks[0].Name != "My Plugin" {
		t.Errorf("expected name 'My Plugin', got '%s'", chunks[0].Name)
	}
}

func TestMergeHooks_Deduplication(t *testing.T) {
	existing := []WPHook{
		{Type: HookAction, Name: "init", FilePath: "test.php", StartLine: 1},
	}
	additional := []WPHook{
		{Type: HookAction, Name: "init", FilePath: "test.php", StartLine: 1},  // duplicate
		{Type: HookFilter, Name: "title", FilePath: "test.php", StartLine: 5}, // new
	}

	result := mergeHooks(existing, additional)
	if len(result) != 2 {
		t.Errorf("expected 2 hooks after merge, got %d", len(result))
	}
}

func TestMergePostTypes_Deduplication(t *testing.T) {
	existing := []PostType{
		{Name: "book", FilePath: "test.php", StartLine: 1},
	}
	additional := []PostType{
		{Name: "book", FilePath: "test.php", StartLine: 1},   // duplicate
		{Name: "movie", FilePath: "test.php", StartLine: 10}, // new
	}

	result := mergePostTypes(existing, additional)
	if len(result) != 2 {
		t.Errorf("expected 2 post types after merge, got %d", len(result))
	}
}

func TestAnalyzer_OxygenElementDetection(t *testing.T) {
	tmpDir := t.TempDir()

	// Plugin header to detect as WordPress
	err := os.WriteFile(filepath.Join(tmpDir, "plugin.php"), []byte(`<?php
/**
 * Plugin Name: Oxygen Custom Elements
 */
`), 0644)
	if err != nil {
		t.Fatal(err)
	}

	// Oxygen element extending OxyEl
	oxyFile := filepath.Join(tmpDir, "elements.php")
	err = os.WriteFile(oxyFile, []byte(`<?php
class MyCustomHeader extends OxyEl {
    public function slug() {
        return 'my-custom-header';
    }
    public function render($options, $defaults, $content) {
        echo '<h1>Custom</h1>';
    }
}
`), 0644)
	if err != nil {
		t.Fatal(err)
	}

	analyzer := NewAnalyzer()
	chunks, err := analyzer.AnalyzePaths([]string{tmpDir})
	if err != nil {
		t.Fatalf("AnalyzePaths failed: %v", err)
	}

	// Find oxy_element chunks
	var oxyChunks []string
	for _, c := range chunks {
		if c.Type == "oxy_element" {
			oxyChunks = append(oxyChunks, c.Name)
			if c.Metadata["framework"] != "wordpress" {
				t.Errorf("Oxygen chunk %s: expected framework=wordpress", c.Name)
			}
			if c.Metadata["wp_type"] != "oxygen_element" {
				t.Errorf("Oxygen chunk %s: expected wp_type=oxygen_element, got %v", c.Name, c.Metadata["wp_type"])
			}
		}
	}

	if len(oxyChunks) == 0 {
		t.Error("expected at least 1 oxy_element chunk, got 0")
	} else {
		t.Logf("Found Oxygen elements: %v", oxyChunks)
	}
}

func TestAnalyzer_WooCommerceHookClassification(t *testing.T) {
	tmpDir := t.TempDir()

	// Plugin header
	err := os.WriteFile(filepath.Join(tmpDir, "plugin.php"), []byte(`<?php
/**
 * Plugin Name: WooCommerce Extension
 */
`), 0644)
	if err != nil {
		t.Fatal(err)
	}

	// WC hooks
	wcFile := filepath.Join(tmpDir, "wc-hooks.php")
	err = os.WriteFile(wcFile, []byte(`<?php
add_action('woocommerce_before_cart', 'custom_cart_notice');
add_filter('woocommerce_product_get_price', 'custom_price', 10, 2);
add_action('woocommerce_checkout_process', 'validate_checkout');
`), 0644)
	if err != nil {
		t.Fatal(err)
	}

	analyzer := NewAnalyzer()
	chunks, err := analyzer.AnalyzePaths([]string{tmpDir})
	if err != nil {
		t.Fatalf("AnalyzePaths failed: %v", err)
	}

	// Find wc_hook chunks
	wcAreas := make(map[string]bool)
	for _, c := range chunks {
		if c.Type == "wc_hook" {
			area, _ := c.Metadata["wc_area"].(string)
			wcAreas[area] = true
			if c.Metadata["framework"] != "wordpress" {
				t.Errorf("WC chunk %s: expected framework=wordpress", c.Name)
			}
		}
	}

	if len(wcAreas) == 0 {
		t.Error("expected WC hook chunks, got 0")
	}
	if !wcAreas["cart"] {
		t.Error("expected wc_area=cart for woocommerce_before_cart")
	}
	if !wcAreas["product"] {
		t.Error("expected wc_area=product for woocommerce_product_get_price")
	}
	if !wcAreas["checkout"] {
		t.Error("expected wc_area=checkout for woocommerce_checkout_process")
	}

	t.Logf("Found WC areas: %v", wcAreas)
}

func TestConvertToChunks_OxygenAndWooCommerce(t *testing.T) {
	analyzer := NewAnalyzer()

	// Use concrete types for OxygenInfo and WooCommerceInfo
	info := &WordPressInfo{
		Hooks: []WPHook{
			{Type: HookAction, Name: "init", Callback: "my_init", FilePath: "test.php", StartLine: 1, EndLine: 1},
		},
	}

	// Test that convertToChunks handles nil OxygenInfo/WooCommerceInfo gracefully
	chunks := analyzer.convertToChunks(info)
	if len(chunks) != 1 {
		t.Fatalf("expected 1 chunk (hook), got %d", len(chunks))
	}
	if chunks[0].Type != "wp_hook" {
		t.Errorf("expected wp_hook, got %s", chunks[0].Type)
	}
}


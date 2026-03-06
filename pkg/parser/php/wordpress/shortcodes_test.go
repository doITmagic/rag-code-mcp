package wordpress

import (
	"testing"
)

func TestShortcodeAnalyzer_AddShortcode(t *testing.T) {
	code := `<?php
add_shortcode('gallery', 'render_gallery');
add_shortcode('contact-form', 'render_contact_form');
`
	root := parsePHP(t, code)
	analyzer := NewShortcodeAnalyzer()
	shortcodes := analyzer.AnalyzeShortcodes(root, "test.php")

	if len(shortcodes) != 2 {
		t.Fatalf("expected 2 shortcodes, got %d", len(shortcodes))
	}
	if shortcodes[0].Tag != "gallery" {
		t.Errorf("expected tag 'gallery', got '%s'", shortcodes[0].Tag)
	}
	if shortcodes[0].Callback != "render_gallery" {
		t.Errorf("expected callback 'render_gallery', got '%s'", shortcodes[0].Callback)
	}
	if shortcodes[1].Tag != "contact-form" {
		t.Errorf("expected tag 'contact-form', got '%s'", shortcodes[1].Tag)
	}
}

func TestShortcodeAnalyzer_InsideFunction(t *testing.T) {
	code := `<?php
function register_shortcodes() {
    add_shortcode('slider', 'render_slider');
}
`
	root := parsePHP(t, code)
	analyzer := NewShortcodeAnalyzer()
	shortcodes := analyzer.AnalyzeShortcodes(root, "test.php")

	if len(shortcodes) != 1 {
		t.Fatalf("expected 1 shortcode, got %d", len(shortcodes))
	}
	if shortcodes[0].Tag != "slider" {
		t.Errorf("expected 'slider', got '%s'", shortcodes[0].Tag)
	}
}

func TestShortcodeAnalyzer_InsideClassMethod(t *testing.T) {
	code := `<?php
class MyPlugin {
    public function init() {
        add_shortcode('pricing', 'render_pricing');
    }
}
`
	root := parsePHP(t, code)
	analyzer := NewShortcodeAnalyzer()
	shortcodes := analyzer.AnalyzeShortcodes(root, "test.php")

	if len(shortcodes) != 1 {
		t.Fatalf("expected 1 shortcode, got %d", len(shortcodes))
	}
	if shortcodes[0].Tag != "pricing" {
		t.Errorf("expected 'pricing', got '%s'", shortcodes[0].Tag)
	}
}

func TestShortcodeAnalyzer_NoShortcodes(t *testing.T) {
	code := `<?php
add_action('init', 'setup');
`
	root := parsePHP(t, code)
	analyzer := NewShortcodeAnalyzer()
	shortcodes := analyzer.AnalyzeShortcodes(root, "test.php")

	if len(shortcodes) != 0 {
		t.Errorf("expected 0 shortcodes, got %d", len(shortcodes))
	}
}

package wordpress

import (
	"testing"
)

func TestBlockAnalyzer_RegisterBlockType(t *testing.T) {
	code := `<?php
register_block_type('my-plugin/hero', array(
    'render_callback' => 'render_hero_block',
));

register_block_type('my-plugin/cta', array(
    'editor_script' => 'cta-block-editor',
));
`
	root := parsePHP(t, code)
	analyzer := NewBlockAnalyzer()
	blocks := analyzer.AnalyzeBlocks(root, "test.php")

	if len(blocks) != 2 {
		t.Fatalf("expected 2 blocks, got %d", len(blocks))
	}
	if blocks[0].Name != "my-plugin/hero" {
		t.Errorf("expected 'my-plugin/hero', got '%s'", blocks[0].Name)
	}
	if blocks[1].Name != "my-plugin/cta" {
		t.Errorf("expected 'my-plugin/cta', got '%s'", blocks[1].Name)
	}
}

func TestBlockAnalyzer_RegisterBlockPattern(t *testing.T) {
	code := `<?php
register_block_pattern('my-plugin/two-columns', array(
    'title' => 'Two Columns',
    'content' => '<div>...</div>',
));
`
	root := parsePHP(t, code)
	analyzer := NewBlockAnalyzer()
	patterns := analyzer.AnalyzeBlockPatterns(root, "test.php")

	if len(patterns) != 1 {
		t.Fatalf("expected 1 pattern, got %d", len(patterns))
	}
	if patterns[0].Name != "my-plugin/two-columns" {
		t.Errorf("expected 'my-plugin/two-columns', got '%s'", patterns[0].Name)
	}
}

func TestBlockAnalyzer_InsideFunction(t *testing.T) {
	code := `<?php
function register_blocks() {
    register_block_type('my-plugin/card', array());
    register_block_pattern('my-plugin/grid', array());
}
`
	root := parsePHP(t, code)
	analyzer := NewBlockAnalyzer()

	blocks := analyzer.AnalyzeBlocks(root, "test.php")
	if len(blocks) != 1 {
		t.Fatalf("expected 1 block, got %d", len(blocks))
	}
	if blocks[0].Name != "my-plugin/card" {
		t.Errorf("expected 'my-plugin/card', got '%s'", blocks[0].Name)
	}

	patterns := analyzer.AnalyzeBlockPatterns(root, "test.php")
	if len(patterns) != 1 {
		t.Fatalf("expected 1 pattern, got %d", len(patterns))
	}
}

func TestBlockAnalyzer_NoBlocks(t *testing.T) {
	code := `<?php
add_action('init', 'setup');
`
	root := parsePHP(t, code)
	analyzer := NewBlockAnalyzer()
	blocks := analyzer.AnalyzeBlocks(root, "test.php")
	patterns := analyzer.AnalyzeBlockPatterns(root, "test.php")

	if len(blocks) != 0 {
		t.Errorf("expected 0 blocks, got %d", len(blocks))
	}
	if len(patterns) != 0 {
		t.Errorf("expected 0 patterns, got %d", len(patterns))
	}
}

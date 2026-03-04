package wordpress

import (
	"testing"
)

func TestPostTypeAnalyzer_RegisterPostType(t *testing.T) {
	code := `<?php
register_post_type('book', array(
    'labels' => array('name' => 'Books'),
    'public' => true,
));

register_post_type('portfolio', array(
    'labels' => array('name' => 'Portfolio'),
    'public' => true,
    'has_archive' => true,
));
`
	root := parsePHP(t, code)
	analyzer := NewPostTypeAnalyzer()
	postTypes := analyzer.AnalyzePostTypes(root, "test.php")

	if len(postTypes) != 2 {
		t.Fatalf("expected 2 post types, got %d", len(postTypes))
	}
	if postTypes[0].Name != "book" {
		t.Errorf("expected 'book', got '%s'", postTypes[0].Name)
	}
	if postTypes[1].Name != "portfolio" {
		t.Errorf("expected 'portfolio', got '%s'", postTypes[1].Name)
	}
	if postTypes[0].FilePath != "test.php" {
		t.Errorf("expected file path 'test.php', got '%s'", postTypes[0].FilePath)
	}
}

func TestPostTypeAnalyzer_InsideFunction(t *testing.T) {
	code := `<?php
function register_custom_post_types() {
    register_post_type('event', array('public' => true));
    register_post_type('testimonial', array('public' => true));
}
`
	root := parsePHP(t, code)
	analyzer := NewPostTypeAnalyzer()
	postTypes := analyzer.AnalyzePostTypes(root, "test.php")

	if len(postTypes) != 2 {
		t.Fatalf("expected 2 post types, got %d", len(postTypes))
	}
	if postTypes[0].Name != "event" {
		t.Errorf("expected 'event', got '%s'", postTypes[0].Name)
	}
}

func TestPostTypeAnalyzer_InsideClass(t *testing.T) {
	code := `<?php
class CustomPostTypes {
    public function register() {
        register_post_type('product', array('public' => true));
    }
}
`
	root := parsePHP(t, code)
	analyzer := NewPostTypeAnalyzer()
	postTypes := analyzer.AnalyzePostTypes(root, "test.php")

	if len(postTypes) != 1 {
		t.Fatalf("expected 1 post type, got %d", len(postTypes))
	}
	if postTypes[0].Name != "product" {
		t.Errorf("expected 'product', got '%s'", postTypes[0].Name)
	}
}

func TestTaxonomyAnalyzer_RegisterTaxonomy(t *testing.T) {
	code := `<?php
register_taxonomy('genre', 'book', array(
    'labels' => array('name' => 'Genres'),
    'hierarchical' => true,
));

register_taxonomy('color', 'product', array('hierarchical' => false));
`
	root := parsePHP(t, code)
	analyzer := NewPostTypeAnalyzer()
	taxonomies := analyzer.AnalyzeTaxonomies(root, "test.php")

	if len(taxonomies) != 2 {
		t.Fatalf("expected 2 taxonomies, got %d", len(taxonomies))
	}
	if taxonomies[0].Name != "genre" {
		t.Errorf("expected 'genre', got '%s'", taxonomies[0].Name)
	}
	if len(taxonomies[0].PostTypes) != 1 || taxonomies[0].PostTypes[0] != "book" {
		t.Errorf("expected post type 'book', got %v", taxonomies[0].PostTypes)
	}
	if taxonomies[1].Name != "color" {
		t.Errorf("expected 'color', got '%s'", taxonomies[1].Name)
	}
}

func TestTaxonomyAnalyzer_InsideFunction(t *testing.T) {
	code := `<?php
function setup_taxonomies() {
    register_taxonomy('tag', 'post', array('hierarchical' => false));
}
`
	root := parsePHP(t, code)
	analyzer := NewPostTypeAnalyzer()
	taxonomies := analyzer.AnalyzeTaxonomies(root, "test.php")

	if len(taxonomies) != 1 {
		t.Fatalf("expected 1 taxonomy, got %d", len(taxonomies))
	}
	if taxonomies[0].Name != "tag" {
		t.Errorf("expected 'tag', got '%s'", taxonomies[0].Name)
	}
}

func TestPostTypeAnalyzer_NoPostTypes(t *testing.T) {
	code := `<?php
echo "no post types here";
add_action('init', 'my_init');
`
	root := parsePHP(t, code)
	analyzer := NewPostTypeAnalyzer()
	postTypes := analyzer.AnalyzePostTypes(root, "test.php")

	if len(postTypes) != 0 {
		t.Errorf("expected 0 post types, got %d", len(postTypes))
	}
}

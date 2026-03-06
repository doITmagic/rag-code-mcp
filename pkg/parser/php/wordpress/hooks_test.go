package wordpress

import (
	"testing"

	"github.com/VKCOM/php-parser/pkg/ast"
	"github.com/VKCOM/php-parser/pkg/conf"
	"github.com/VKCOM/php-parser/pkg/errors"
	"github.com/VKCOM/php-parser/pkg/parser"
	"github.com/VKCOM/php-parser/pkg/version"
)

func parsePHP(t *testing.T, code string) ast.Vertex {
	t.Helper()
	root, err := parser.Parse([]byte(code), conf.Config{
		Version: &version.Version{Major: 8, Minor: 0},
		ErrorHandlerFunc: func(e *errors.Error) {
			// ignore
		},
	})
	if err != nil {
		t.Fatalf("failed to parse PHP: %v", err)
	}
	return root
}

func TestHookAnalyzer_AddAction(t *testing.T) {
	code := `<?php
add_action('init', 'my_init_callback');
add_action('wp_enqueue_scripts', 'enqueue_assets', 20);
add_action('save_post', 'handle_save', 10, 2);
`
	root := parsePHP(t, code)
	analyzer := NewHookAnalyzer()
	hooks := analyzer.AnalyzeHooksFromAST(root, "test.php")

	if len(hooks) != 3 {
		t.Fatalf("expected 3 hooks, got %d", len(hooks))
	}

	// First hook
	h := hooks[0]
	if h.Type != HookAction {
		t.Errorf("expected action, got %s", h.Type)
	}
	if h.Name != "init" {
		t.Errorf("expected hook name 'init', got '%s'", h.Name)
	}
	if h.Callback != "my_init_callback" {
		t.Errorf("expected callback 'my_init_callback', got '%s'", h.Callback)
	}

	// Second hook — with priority
	h = hooks[1]
	if h.Name != "wp_enqueue_scripts" {
		t.Errorf("expected 'wp_enqueue_scripts', got '%s'", h.Name)
	}
	if h.Priority != 20 {
		t.Errorf("expected priority 20, got %d", h.Priority)
	}

	// Third hook — with priority and accepted_args
	h = hooks[2]
	if h.Name != "save_post" {
		t.Errorf("expected 'save_post', got '%s'", h.Name)
	}
	if h.Priority != 10 {
		t.Errorf("expected priority 10, got %d", h.Priority)
	}
	if h.AcceptedArgs != 2 {
		t.Errorf("expected accepted_args 2, got %d", h.AcceptedArgs)
	}
}

func TestHookAnalyzer_AddFilter(t *testing.T) {
	code := `<?php
add_filter('the_content', 'my_content_filter');
add_filter('the_title', 'modify_title', 15);
`
	root := parsePHP(t, code)
	analyzer := NewHookAnalyzer()
	hooks := analyzer.AnalyzeHooksFromAST(root, "test.php")

	if len(hooks) != 2 {
		t.Fatalf("expected 2 hooks, got %d", len(hooks))
	}

	if hooks[0].Type != HookFilter {
		t.Errorf("expected filter, got %s", hooks[0].Type)
	}
	if hooks[0].Name != "the_content" {
		t.Errorf("expected 'the_content', got '%s'", hooks[0].Name)
	}
	if hooks[1].Priority != 15 {
		t.Errorf("expected priority 15, got %d", hooks[1].Priority)
	}
}

func TestHookAnalyzer_DoAction(t *testing.T) {
	code := `<?php
do_action('my_custom_hook', $arg1, $arg2);
`
	root := parsePHP(t, code)
	analyzer := NewHookAnalyzer()
	hooks := analyzer.AnalyzeHooksFromAST(root, "test.php")

	if len(hooks) != 1 {
		t.Fatalf("expected 1 hook, got %d", len(hooks))
	}
	if hooks[0].Type != HookActionTrigger {
		t.Errorf("expected action_trigger, got %s", hooks[0].Type)
	}
	if hooks[0].Name != "my_custom_hook" {
		t.Errorf("expected 'my_custom_hook', got '%s'", hooks[0].Name)
	}
}

func TestHookAnalyzer_ApplyFilters(t *testing.T) {
	code := `<?php
$value = apply_filters('my_filter', $original_value);
`
	root := parsePHP(t, code)
	analyzer := NewHookAnalyzer()
	hooks := analyzer.AnalyzeHooksFromAST(root, "test.php")

	if len(hooks) != 1 {
		t.Fatalf("expected 1 hook, got %d", len(hooks))
	}
	if hooks[0].Type != HookFilterTrigger {
		t.Errorf("expected filter_trigger, got %s", hooks[0].Type)
	}
	if hooks[0].Name != "my_filter" {
		t.Errorf("expected 'my_filter', got '%s'", hooks[0].Name)
	}
}

func TestHookAnalyzer_RemoveAction(t *testing.T) {
	code := `<?php
remove_action('wp_head', 'wp_generator');
remove_filter('the_content', 'wpautop');
`
	root := parsePHP(t, code)
	analyzer := NewHookAnalyzer()
	hooks := analyzer.AnalyzeHooksFromAST(root, "test.php")

	if len(hooks) != 2 {
		t.Fatalf("expected 2 hooks, got %d", len(hooks))
	}
	if hooks[0].Type != HookActionRemoval {
		t.Errorf("expected action_removal, got %s", hooks[0].Type)
	}
	if hooks[0].Name != "wp_head" {
		t.Errorf("expected 'wp_head', got '%s'", hooks[0].Name)
	}
	if hooks[1].Type != HookFilterRemoval {
		t.Errorf("expected filter_removal, got %s", hooks[1].Type)
	}
}

func TestHookAnalyzer_HasFilter(t *testing.T) {
	code := `<?php
has_filter('the_content');
has_action('init');
`
	root := parsePHP(t, code)
	analyzer := NewHookAnalyzer()
	hooks := analyzer.AnalyzeHooksFromAST(root, "test.php")

	if len(hooks) != 2 {
		t.Fatalf("expected 2 hooks, got %d", len(hooks))
	}
	if hooks[0].Type != HookFilterCheck {
		t.Errorf("expected filter_check, got %s", hooks[0].Type)
	}
	if hooks[1].Type != HookActionCheck {
		t.Errorf("expected action_check, got %s", hooks[1].Type)
	}
}

func TestHookAnalyzer_InsideFunction(t *testing.T) {
	code := `<?php
function setup_hooks() {
    add_action('init', 'my_init');
    add_filter('the_content', 'filter_content');
}
`
	root := parsePHP(t, code)
	analyzer := NewHookAnalyzer()
	hooks := analyzer.AnalyzeHooksFromAST(root, "test.php")

	if len(hooks) != 2 {
		t.Fatalf("expected 2 hooks, got %d", len(hooks))
	}
	if hooks[0].Name != "init" {
		t.Errorf("expected 'init', got '%s'", hooks[0].Name)
	}
	if hooks[1].Name != "the_content" {
		t.Errorf("expected 'the_content', got '%s'", hooks[1].Name)
	}
}

func TestHookAnalyzer_InsideClassMethod(t *testing.T) {
	code := `<?php
class MyPlugin {
    public function __construct() {
        add_action('init', [$this, 'init_plugin']);
    }

    public function init_plugin() {
        add_action('wp_loaded', [$this, 'on_loaded']);
    }
}
`
	root := parsePHP(t, code)
	analyzer := NewHookAnalyzer()
	hooks := analyzer.AnalyzeHooksFromAST(root, "test.php")

	if len(hooks) != 2 {
		t.Fatalf("expected 2 hooks, got %d", len(hooks))
	}
	if hooks[0].Name != "init" {
		t.Errorf("expected 'init', got '%s'", hooks[0].Name)
	}
	// Array callback [$this, 'init_plugin'] → "$this::init_plugin"
	if hooks[0].Callback != "$this::init_plugin" {
		t.Errorf("expected callback '$this::init_plugin', got '%s'", hooks[0].Callback)
	}
	if hooks[1].Name != "wp_loaded" {
		t.Errorf("expected 'wp_loaded', got '%s'", hooks[1].Name)
	}
}

func TestHookAnalyzer_InsideIfBlock(t *testing.T) {
	code := `<?php
if (is_admin()) {
    add_action('admin_init', 'setup_admin');
}
`
	root := parsePHP(t, code)
	analyzer := NewHookAnalyzer()
	hooks := analyzer.AnalyzeHooksFromAST(root, "test.php")

	if len(hooks) != 1 {
		t.Fatalf("expected 1 hook, got %d", len(hooks))
	}
	if hooks[0].Name != "admin_init" {
		t.Errorf("expected 'admin_init', got '%s'", hooks[0].Name)
	}
}

func TestHookAnalyzer_InsideNamespace(t *testing.T) {
	code := `<?php
namespace MyPlugin\Hooks;

add_action('init', 'MyPlugin\Hooks\setup');
`
	root := parsePHP(t, code)
	analyzer := NewHookAnalyzer()
	hooks := analyzer.AnalyzeHooksFromAST(root, "test.php")

	if len(hooks) != 1 {
		t.Fatalf("expected 1 hook, got %d", len(hooks))
	}
}

func TestHookAnalyzer_MixedHookTypes(t *testing.T) {
	code := `<?php
add_action('init', 'my_init');
add_filter('the_content', 'my_filter');
do_action('my_custom_action');
apply_filters('my_custom_filter', $value);
remove_action('wp_head', 'wp_generator');
has_filter('the_title');
`
	root := parsePHP(t, code)
	analyzer := NewHookAnalyzer()
	hooks := analyzer.AnalyzeHooksFromAST(root, "test.php")

	if len(hooks) != 6 {
		t.Fatalf("expected 6 hooks, got %d", len(hooks))
	}

	expectedTypes := []HookType{
		HookAction,
		HookFilter,
		HookActionTrigger,
		HookFilterTrigger,
		HookActionRemoval,
		HookFilterCheck,
	}

	for i, expected := range expectedTypes {
		if hooks[i].Type != expected {
			t.Errorf("hook %d: expected type %s, got %s", i, expected, hooks[i].Type)
		}
	}
}

func TestHookAnalyzer_NonHookFunctionIgnored(t *testing.T) {
	code := `<?php
my_custom_function('arg1', 'arg2');
echo "hello";
$x = array_map('strtoupper', $items);
`
	root := parsePHP(t, code)
	analyzer := NewHookAnalyzer()
	hooks := analyzer.AnalyzeHooksFromAST(root, "test.php")

	if len(hooks) != 0 {
		t.Errorf("expected 0 hooks, got %d", len(hooks))
	}
}

func TestHookAnalyzer_WooCommerceHooks(t *testing.T) {
	code := `<?php
add_action('woocommerce_before_cart', 'my_cart_notice');
add_filter('woocommerce_product_get_price', 'custom_price', 10, 2);
do_action('woocommerce_checkout_process');
`
	root := parsePHP(t, code)
	analyzer := NewHookAnalyzer()
	hooks := analyzer.AnalyzeHooksFromAST(root, "test.php")

	if len(hooks) != 3 {
		t.Fatalf("expected 3 hooks, got %d", len(hooks))
	}
	if hooks[0].Name != "woocommerce_before_cart" {
		t.Errorf("expected 'woocommerce_before_cart', got '%s'", hooks[0].Name)
	}
	if hooks[1].Priority != 10 {
		t.Errorf("expected priority 10, got %d", hooks[1].Priority)
	}
	if hooks[1].AcceptedArgs != 2 {
		t.Errorf("expected accepted_args 2, got %d", hooks[1].AcceptedArgs)
	}
}

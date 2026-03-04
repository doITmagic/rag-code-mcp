package woocommerce

import (
	"testing"

	"github.com/VKCOM/php-parser/pkg/ast"
	"github.com/VKCOM/php-parser/pkg/conf"
	"github.com/VKCOM/php-parser/pkg/errors"
	"github.com/VKCOM/php-parser/pkg/parser"
	"github.com/VKCOM/php-parser/pkg/version"

	"github.com/doITmagic/rag-code-mcp/pkg/parser/php/wordpress"
)

func parsePHP(t *testing.T, code string) ast.Vertex {
	t.Helper()
	root, err := parser.Parse([]byte(code), conf.Config{
		Version:          &version.Version{Major: 8, Minor: 0},
		ErrorHandlerFunc: func(e *errors.Error) {},
	})
	if err != nil {
		t.Fatalf("failed to parse PHP: %v", err)
	}
	return root
}

func TestAnalyzer_WooCommerceHooks(t *testing.T) {
	code := `<?php
add_action('woocommerce_before_cart', 'my_cart_notice');
add_filter('woocommerce_product_get_price', 'custom_price', 10, 2);
do_action('woocommerce_checkout_process');
add_action('woocommerce_order_status_completed', 'handle_order');
add_filter('woocommerce_shipping_methods', 'add_shipping');
`
	root := parsePHP(t, code)
	analyzer := NewAnalyzer()
	info := analyzer.Analyze(root, "test.php")

	if len(info.Hooks) != 5 {
		t.Fatalf("expected 5 WC hooks, got %d", len(info.Hooks))
	}

	// Check area classification
	expected := map[string]WCHookArea{
		"woocommerce_before_cart":            WCAreaCart,
		"woocommerce_product_get_price":      WCAreaProduct,
		"woocommerce_checkout_process":       WCAreaCheckout,
		"woocommerce_order_status_completed": WCAreaOrder,
		"woocommerce_shipping_methods":       WCAreaShipping,
	}

	for _, hook := range info.Hooks {
		expectedArea, ok := expected[hook.HookName]
		if !ok {
			t.Errorf("unexpected hook: %s", hook.HookName)
			continue
		}
		if hook.Area != expectedArea {
			t.Errorf("hook %s: expected area %s, got %s", hook.HookName, expectedArea, hook.Area)
		}
	}
}

func TestAnalyzer_WCAPICalls(t *testing.T) {
	code := `<?php
$product = wc_get_product($id);
$order = wc_get_order(123);
$price = wc_price(29.99);
wc_add_notice('Item added', 'success');
wc_get_template('cart/cart.php');
`
	root := parsePHP(t, code)
	analyzer := NewAnalyzer()
	info := analyzer.Analyze(root, "test.php")

	if len(info.APICalls) != 5 {
		t.Fatalf("expected 5 API calls, got %d", len(info.APICalls))
	}

	expectedCats := map[string]string{
		"wc_get_product":  "product",
		"wc_get_order":    "order",
		"wc_price":        "formatting",
		"wc_add_notice":   "notice",
		"wc_get_template": "template",
	}

	for _, call := range info.APICalls {
		expectedCat, ok := expectedCats[call.Function]
		if !ok {
			t.Errorf("unexpected API call: %s", call.Function)
			continue
		}
		if call.Category != expectedCat {
			t.Errorf("call %s: expected category %s, got %s", call.Function, expectedCat, call.Category)
		}
	}
}

func TestAnalyzer_IgnoresNonWCHooks(t *testing.T) {
	code := `<?php
add_action('init', 'my_init');
add_filter('the_content', 'my_filter');
do_action('my_custom_hook');
`
	root := parsePHP(t, code)
	analyzer := NewAnalyzer()
	info := analyzer.Analyze(root, "test.php")

	if len(info.Hooks) != 0 {
		t.Errorf("expected 0 WC hooks for non-WC hooks, got %d", len(info.Hooks))
	}
}

func TestAnalyzer_InsideFunction(t *testing.T) {
	code := `<?php
function setup_wc() {
    add_action('woocommerce_before_checkout_form', 'my_checkout_banner');
    $product = wc_get_product(42);
}
`
	root := parsePHP(t, code)
	analyzer := NewAnalyzer()
	info := analyzer.Analyze(root, "test.php")

	if len(info.Hooks) != 1 {
		t.Fatalf("expected 1 WC hook, got %d", len(info.Hooks))
	}
	if info.Hooks[0].Area != WCAreaCheckout {
		t.Errorf("expected checkout area, got %s", info.Hooks[0].Area)
	}

	if len(info.APICalls) != 1 {
		t.Fatalf("expected 1 API call, got %d", len(info.APICalls))
	}
	if info.APICalls[0].Function != "wc_get_product" {
		t.Errorf("expected wc_get_product, got %s", info.APICalls[0].Function)
	}
}

func TestAnalyzer_InsideClassMethod(t *testing.T) {
	code := `<?php
class MyWCPlugin {
    public function init() {
        add_action('woocommerce_email_order_details', 'custom_email');
    }

    public function get_order_data($id) {
        return wc_get_order($id);
    }
}
`
	root := parsePHP(t, code)
	analyzer := NewAnalyzer()
	info := analyzer.Analyze(root, "test.php")

	if len(info.Hooks) != 1 {
		t.Fatalf("expected 1 WC hook, got %d", len(info.Hooks))
	}
	if info.Hooks[0].Area != WCAreaEmail {
		t.Errorf("expected email area, got %s", info.Hooks[0].Area)
	}

	if len(info.APICalls) != 1 {
		t.Fatalf("expected 1 API call, got %d", len(info.APICalls))
	}
}

func TestAnalyzeHooksFromWP(t *testing.T) {
	wpHooks := []wordpress.WPHook{
		{Type: wordpress.HookAction, Name: "init", Callback: "my_init", FilePath: "test.php", StartLine: 1},
		{Type: wordpress.HookAction, Name: "woocommerce_before_cart", Callback: "cart_fn", FilePath: "test.php", StartLine: 2, Priority: 20},
		{Type: wordpress.HookFilter, Name: "woocommerce_product_get_price", Callback: "price_fn", FilePath: "test.php", StartLine: 3},
		{Type: wordpress.HookAction, Name: "wp_enqueue_scripts", Callback: "enqueue", FilePath: "test.php", StartLine: 4},
		{Type: wordpress.HookActionTrigger, Name: "woocommerce_checkout_process", FilePath: "test.php", StartLine: 5},
	}

	analyzer := NewAnalyzer()
	wcHooks := analyzer.AnalyzeHooksFromWP(wpHooks)

	// Should only pick up the 3 woocommerce_ hooks
	if len(wcHooks) != 3 {
		t.Fatalf("expected 3 WC hooks, got %d", len(wcHooks))
	}

	if wcHooks[0].HookName != "woocommerce_before_cart" {
		t.Errorf("expected woocommerce_before_cart, got %s", wcHooks[0].HookName)
	}
	if wcHooks[0].Area != WCAreaCart {
		t.Errorf("expected cart area, got %s", wcHooks[0].Area)
	}
	if wcHooks[0].Priority != 20 {
		t.Errorf("expected priority 20, got %d", wcHooks[0].Priority)
	}

	if wcHooks[1].Area != WCAreaProduct {
		t.Errorf("expected product area, got %s", wcHooks[1].Area)
	}
	if wcHooks[2].Area != WCAreaCheckout {
		t.Errorf("expected checkout area, got %s", wcHooks[2].Area)
	}
}

func TestClassifyHookArea(t *testing.T) {
	tests := []struct {
		hookName string
		expected WCHookArea
	}{
		{"woocommerce_before_cart", WCAreaCart},
		{"woocommerce_add_to_cart", WCAreaCart},
		{"woocommerce_checkout_process", WCAreaCheckout},
		{"woocommerce_before_checkout_form", WCAreaCheckout},
		{"woocommerce_product_get_price", WCAreaProduct},
		{"woocommerce_single_product_summary", WCAreaProduct},
		{"woocommerce_order_status_changed", WCAreaOrder},
		{"woocommerce_new_order", WCAreaOrder},
		{"woocommerce_account_dashboard", WCAreaAccount},
		{"woocommerce_my_account_my_orders", WCAreaAccount},
		{"woocommerce_admin_order_data", WCAreaAdmin},
		{"woocommerce_shipping_init", WCAreaShipping},
		{"woocommerce_payment_complete", WCAreaPayment},
		{"woocommerce_email_header", WCAreaEmail},
		{"woocommerce_loaded", WCAreaGeneral},
		{"woocommerce_init", WCAreaGeneral},
	}

	for _, tt := range tests {
		result := classifyHookArea(tt.hookName)
		if result != tt.expected {
			t.Errorf("classifyHookArea(%s) = %s, want %s", tt.hookName, result, tt.expected)
		}
	}
}

func TestAnalyzer_MixedWCAndNonWC(t *testing.T) {
	code := `<?php
add_action('init', 'my_init');
add_action('woocommerce_before_cart', 'cart_notice');
$product = wc_get_product(1);
echo "hello";
some_random_function();
`
	root := parsePHP(t, code)
	analyzer := NewAnalyzer()
	info := analyzer.Analyze(root, "test.php")

	if len(info.Hooks) != 1 {
		t.Errorf("expected 1 WC hook, got %d", len(info.Hooks))
	}
	if len(info.APICalls) != 1 {
		t.Errorf("expected 1 API call, got %d", len(info.APICalls))
	}
}

func TestAnalyzer_EmptyFile(t *testing.T) {
	code := `<?php
// empty file
`
	root := parsePHP(t, code)
	analyzer := NewAnalyzer()
	info := analyzer.Analyze(root, "test.php")

	if len(info.Hooks) != 0 || len(info.APICalls) != 0 {
		t.Error("expected empty results for empty file")
	}
}

func TestAnalyzer_AccountAreaHooks(t *testing.T) {
	code := `<?php
add_action('woocommerce_account_navigation', 'custom_nav');
add_filter('woocommerce_my_account_my_orders_query', 'custom_orders_query');
`
	root := parsePHP(t, code)
	analyzer := NewAnalyzer()
	info := analyzer.Analyze(root, "test.php")

	if len(info.Hooks) != 2 {
		t.Fatalf("expected 2 hooks, got %d", len(info.Hooks))
	}
	for _, hook := range info.Hooks {
		if hook.Area != WCAreaAccount {
			t.Errorf("hook %s: expected account area, got %s", hook.HookName, hook.Area)
		}
	}
}

func TestAnalyzer_InsideIfElse(t *testing.T) {
	code := `<?php
if (is_admin()) {
    add_action('woocommerce_admin_init', 'admin_setup');
} elseif (is_checkout()) {
    add_action('woocommerce_checkout_init', 'checkout_setup');
} else {
    $product = wc_get_product(1);
}
`
	root := parsePHP(t, code)
	analyzer := NewAnalyzer()
	info := analyzer.Analyze(root, "test.php")

	if len(info.Hooks) != 2 {
		t.Fatalf("expected 2 WC hooks, got %d", len(info.Hooks))
	}
	if len(info.APICalls) != 1 {
		t.Fatalf("expected 1 API call, got %d", len(info.APICalls))
	}
}

func TestAnalyzer_InsideNamespace(t *testing.T) {
	code := `<?php
namespace MyPlugin\WC;

add_action('woocommerce_payment_complete', 'handle_payment');
wc_get_order(123);
`
	root := parsePHP(t, code)
	analyzer := NewAnalyzer()
	info := analyzer.Analyze(root, "test.php")

	if len(info.Hooks) != 1 {
		t.Fatalf("expected 1 hook, got %d", len(info.Hooks))
	}
	if info.Hooks[0].Area != WCAreaPayment {
		t.Errorf("expected payment area, got %s", info.Hooks[0].Area)
	}
	if len(info.APICalls) != 1 {
		t.Fatalf("expected 1 API call, got %d", len(info.APICalls))
	}
}

func TestAnalyzer_WCStaticCall(t *testing.T) {
	code := `<?php
WC::instance();
`
	root := parsePHP(t, code)
	analyzer := NewAnalyzer()
	info := analyzer.Analyze(root, "test.php")

	if len(info.APICalls) != 1 {
		t.Fatalf("expected 1 API call for WC::instance(), got %d", len(info.APICalls))
	}
	if info.APICalls[0].Function != "WC::instance" {
		t.Errorf("expected 'WC::instance', got '%s'", info.APICalls[0].Function)
	}
	if info.APICalls[0].Category != "core" {
		t.Errorf("expected category 'core', got '%s'", info.APICalls[0].Category)
	}
}

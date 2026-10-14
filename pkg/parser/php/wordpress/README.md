# WordPress, WooCommerce & Oxygen Builder Analyzer (`pkg/parser/php/wordpress`)

Framework-aware analyzer and `php.FrameworkEnricher` for **WordPress** core/plugins/themes, **WooCommerce**, and **Oxygen Builder**.

## 🏗️ Architecture

```
wordpress/
├── adapter.go           - Registers WordPress as a php.FrameworkEnricher
├── analyzer.go          - Main WordPress analyzer coordinator & project detector
├── types.go             - WordPress symbol & metadata structures
├── ast_helper.go        - AST value extraction (strings, arrays, dynamic concat/encapsed strings)
├── hooks.go             - Actions & Filters analyzer
├── post_types.go        - Custom Post Types (CPT) & Custom Taxonomies analyzer
├── shortcodes.go        - Shortcode registration analyzer
├── blocks.go            - Gutenberg block registration analyzer
├── widgets.go           - WP_Widget subclass analyzer
├── admin.go             - Admin menu pages, settings, AJAX & WP REST API route analyzer
├── plugin_header.go     - WordPress plugin & theme header metadata parser
├── oxygen/              - Oxygen Builder custom elements & shortcodes analyzer
└── woocommerce/         - WooCommerce hooks, REST API, product types & template overrides
```

## ✨ Implemented Features

1. **WordPress Hooks (`hooks.go`)**
   - Extracts `add_action`, `add_filter`, `do_action`, `apply_filters`, `remove_action`, `remove_filter`, `has_action`, `has_filter`.
   - Captures hook name (including dynamic interpolated hooks like `"save_post_{$post_type}"`), callback function/method (`[Class::class, 'method']`, closures, string callbacks), priority, and accepted args.

2. **Custom Post Types & Taxonomies (`post_types.go`)**
   - Extracts `register_post_type()` and `register_taxonomy()` calls along with labels, capabilities, REST support (`show_in_rest`), and associated post types.

3. **Shortcodes, Gutenberg Blocks & Widgets (`shortcodes.go`, `blocks.go`, `widgets.go`)**
   - Extracts `add_shortcode()` tags and callbacks.
   - Extracts `register_block_type()` server-rendered Gutenberg blocks.
   - Detects classes extending `WP_Widget` (`widget`, `form`, `update`).

4. **Admin Pages, AJAX & REST API (`admin.go`)**
   - Extracts `add_menu_page`, `add_submenu_page`, `register_setting`, `wp_ajax_*` / `wp_ajax_nopriv_*` handlers, and `register_rest_route` endpoints.

5. **Oxygen Builder (`oxygen/`)**
   - Detects `OxyEl` subclasses (`class MyElement extends OxyEl`) and extracts element name, tag, slug, controls (`addOptionControl`, `addStyleControl`), and `render()` callbacks.

6. **WooCommerce (`woocommerce/`)**
   - Extracts WooCommerce-specific action/filter hooks (`woocommerce_*`), custom product types (`WC_Product_*`), payment gateways (`WC_Payment_Gateway`), REST controllers, and theme template overrides.

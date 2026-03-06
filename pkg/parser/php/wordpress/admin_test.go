package wordpress

import (
	"testing"
)

func TestAdminAnalyzer_AddMenuPage(t *testing.T) {
	code := `<?php
add_menu_page('My Plugin', 'My Plugin', 'manage_options', 'my-plugin-settings', 'render_settings_page');
`
	root := parsePHP(t, code)
	analyzer := NewAdminAnalyzer()
	pages := analyzer.AnalyzeAdminPages(root, "test.php")

	if len(pages) != 1 {
		t.Fatalf("expected 1 admin page, got %d", len(pages))
	}
	page := pages[0]
	if page.PageTitle != "My Plugin" {
		t.Errorf("expected page title 'My Plugin', got '%s'", page.PageTitle)
	}
	if page.MenuTitle != "My Plugin" {
		t.Errorf("expected menu title 'My Plugin', got '%s'", page.MenuTitle)
	}
	if page.Capability != "manage_options" {
		t.Errorf("expected capability 'manage_options', got '%s'", page.Capability)
	}
	if page.MenuSlug != "my-plugin-settings" {
		t.Errorf("expected menu slug 'my-plugin-settings', got '%s'", page.MenuSlug)
	}
	if page.Callback != "render_settings_page" {
		t.Errorf("expected callback 'render_settings_page', got '%s'", page.Callback)
	}
	if page.IsSubmenu {
		t.Error("expected IsSubmenu=false")
	}
}

func TestAdminAnalyzer_AddSubmenuPage(t *testing.T) {
	code := `<?php
add_submenu_page('options-general.php', 'Sub Settings', 'Sub Menu', 'manage_options', 'my-sub-settings', 'render_sub_page');
`
	root := parsePHP(t, code)
	analyzer := NewAdminAnalyzer()
	pages := analyzer.AnalyzeAdminPages(root, "test.php")

	if len(pages) != 1 {
		t.Fatalf("expected 1 admin page, got %d", len(pages))
	}
	page := pages[0]
	if !page.IsSubmenu {
		t.Error("expected IsSubmenu=true")
	}
	if page.Parent != "options-general.php" {
		t.Errorf("expected parent 'options-general.php', got '%s'", page.Parent)
	}
	if page.PageTitle != "Sub Settings" {
		t.Errorf("expected page title 'Sub Settings', got '%s'", page.PageTitle)
	}
	if page.MenuSlug != "my-sub-settings" {
		t.Errorf("expected menu slug 'my-sub-settings', got '%s'", page.MenuSlug)
	}
	if page.Callback != "render_sub_page" {
		t.Errorf("expected callback 'render_sub_page', got '%s'", page.Callback)
	}
}

func TestAdminAnalyzer_RegisterSetting(t *testing.T) {
	code := `<?php
register_setting('my_options_group', 'my_option_name');
register_setting('general', 'another_option');
`
	root := parsePHP(t, code)
	analyzer := NewAdminAnalyzer()
	settings := analyzer.AnalyzeSettings(root, "test.php")

	if len(settings) != 2 {
		t.Fatalf("expected 2 settings, got %d", len(settings))
	}
	if settings[0].Group != "my_options_group" {
		t.Errorf("expected group 'my_options_group', got '%s'", settings[0].Group)
	}
	if settings[0].Option != "my_option_name" {
		t.Errorf("expected option 'my_option_name', got '%s'", settings[0].Option)
	}
	if settings[1].Group != "general" {
		t.Errorf("expected group 'general', got '%s'", settings[1].Group)
	}
}

func TestAdminAnalyzer_InsideFunction(t *testing.T) {
	code := `<?php
function setup_admin() {
    add_menu_page('Dashboard', 'Dash', 'read', 'my-dashboard', 'render_dash');
    register_setting('dash_group', 'dash_option');
}
`
	root := parsePHP(t, code)
	analyzer := NewAdminAnalyzer()

	pages := analyzer.AnalyzeAdminPages(root, "test.php")
	if len(pages) != 1 {
		t.Fatalf("expected 1 admin page, got %d", len(pages))
	}
	if pages[0].MenuSlug != "my-dashboard" {
		t.Errorf("expected 'my-dashboard', got '%s'", pages[0].MenuSlug)
	}

	settings := analyzer.AnalyzeSettings(root, "test.php")
	if len(settings) != 1 {
		t.Fatalf("expected 1 setting, got %d", len(settings))
	}
}

func TestAdminAnalyzer_NoAdminPages(t *testing.T) {
	code := `<?php
add_action('init', 'setup');
`
	root := parsePHP(t, code)
	analyzer := NewAdminAnalyzer()
	pages := analyzer.AnalyzeAdminPages(root, "test.php")
	settings := analyzer.AnalyzeSettings(root, "test.php")

	if len(pages) != 0 {
		t.Errorf("expected 0 pages, got %d", len(pages))
	}
	if len(settings) != 0 {
		t.Errorf("expected 0 settings, got %d", len(settings))
	}
}

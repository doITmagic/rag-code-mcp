package wordpress

// WordPressInfo contains WordPress-specific framework information extracted from a project
type WordPressInfo struct {
	Hooks           []WPHook       `json:"hooks,omitempty"`
	PostTypes       []PostType     `json:"post_types,omitempty"`
	Taxonomies      []Taxonomy     `json:"taxonomies,omitempty"`
	Shortcodes      []Shortcode    `json:"shortcodes,omitempty"`
	Blocks          []Block        `json:"blocks,omitempty"`
	BlockPatterns   []BlockPattern `json:"block_patterns,omitempty"`
	Widgets         []Widget       `json:"widgets,omitempty"`
	AdminPages      []AdminPage    `json:"admin_pages,omitempty"`
	Settings        []Setting      `json:"settings,omitempty"`
	PluginHeader    *PluginHeader  `json:"plugin_header,omitempty"`
	OxygenInfo      any            `json:"oxygen,omitempty"`      // *oxygen.OxygenInfo (avoid import cycle)
	WooCommerceInfo any            `json:"woocommerce,omitempty"` // *woocommerce.WooCommerceInfo (avoid import cycle)
}

// HookType represents the type of a WordPress hook
type HookType string

const (
	HookAction        HookType = "action"
	HookFilter        HookType = "filter"
	HookActionTrigger HookType = "action_trigger"
	HookFilterTrigger HookType = "filter_trigger"
	HookActionRemoval HookType = "action_removal"
	HookFilterRemoval HookType = "filter_removal"
	HookFilterCheck   HookType = "filter_check"
	HookActionCheck   HookType = "action_check"
)

// WPHook represents a WordPress action or filter hook
type WPHook struct {
	Type         HookType `json:"type"`
	Name         string   `json:"name"`                    // Hook name (e.g., "init", "the_content")
	Callback     string   `json:"callback,omitempty"`      // Callback function/method name
	Priority     int      `json:"priority,omitempty"`      // Hook priority (default 10)
	AcceptedArgs int      `json:"accepted_args,omitempty"` // Number of accepted arguments
	FilePath     string   `json:"file_path"`
	StartLine    int      `json:"start_line"`
	EndLine      int      `json:"end_line"`
}

// PostType represents a custom post type registration
type PostType struct {
	Name      string `json:"name"` // Post type slug (e.g., "book")
	Label     string `json:"label,omitempty"`
	FilePath  string `json:"file_path"`
	StartLine int    `json:"start_line"`
	EndLine   int    `json:"end_line"`
}

// Taxonomy represents a custom taxonomy registration
type Taxonomy struct {
	Name      string   `json:"name"`                 // Taxonomy slug (e.g., "genre")
	PostTypes []string `json:"post_types,omitempty"` // Associated post types
	Label     string   `json:"label,omitempty"`
	FilePath  string   `json:"file_path"`
	StartLine int      `json:"start_line"`
	EndLine   int      `json:"end_line"`
}

// Shortcode represents a WordPress shortcode registration
type Shortcode struct {
	Tag       string `json:"tag"`                // Shortcode tag (e.g., "gallery")
	Callback  string `json:"callback,omitempty"` // Callback function name
	FilePath  string `json:"file_path"`
	StartLine int    `json:"start_line"`
	EndLine   int    `json:"end_line"`
}

// Block represents a Gutenberg block registration
type Block struct {
	Name      string `json:"name"` // Block name (e.g., "my-plugin/block")
	FilePath  string `json:"file_path"`
	StartLine int    `json:"start_line"`
	EndLine   int    `json:"end_line"`
}

// BlockPattern represents a Gutenberg block pattern registration
type BlockPattern struct {
	Name      string `json:"name"` // Pattern name
	FilePath  string `json:"file_path"`
	StartLine int    `json:"start_line"`
	EndLine   int    `json:"end_line"`
}

// Widget represents a WordPress widget class (extends WP_Widget)
type Widget struct {
	ClassName string `json:"class_name"`
	Namespace string `json:"namespace,omitempty"`
	FullName  string `json:"full_name"`
	FilePath  string `json:"file_path"`
	StartLine int    `json:"start_line"`
	EndLine   int    `json:"end_line"`
}

// AdminPage represents a WordPress admin menu page
type AdminPage struct {
	PageTitle  string `json:"page_title"`
	MenuTitle  string `json:"menu_title,omitempty"`
	Capability string `json:"capability,omitempty"`
	MenuSlug   string `json:"menu_slug"`
	Callback   string `json:"callback,omitempty"`
	IsSubmenu  bool   `json:"is_submenu,omitempty"`
	Parent     string `json:"parent,omitempty"` // Parent slug for submenus
	FilePath   string `json:"file_path"`
	StartLine  int    `json:"start_line"`
	EndLine    int    `json:"end_line"`
}

// Setting represents a WordPress settings registration
type Setting struct {
	Group    string `json:"group"`
	Option   string `json:"option"`
	FilePath string `json:"file_path,omitempty"`
	Line     int    `json:"line,omitempty"`
}

// PluginHeader represents WordPress plugin/theme metadata from the file header comment
type PluginHeader struct {
	Name        string `json:"name"`
	PluginURI   string `json:"plugin_uri,omitempty"`
	Description string `json:"description,omitempty"`
	Version     string `json:"version,omitempty"`
	Author      string `json:"author,omitempty"`
	AuthorURI   string `json:"author_uri,omitempty"`
	TextDomain  string `json:"text_domain,omitempty"`
	DomainPath  string `json:"domain_path,omitempty"`
	License     string `json:"license,omitempty"`
	IsTheme     bool   `json:"is_theme,omitempty"` // true if Theme Name header found
	FilePath    string `json:"file_path"`
}

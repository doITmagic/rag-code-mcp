package oxygen

// OxygenInfo contains Oxygen Builder-specific analysis results
type OxygenInfo struct {
	Elements   []OxygenElement  `json:"elements,omitempty"`
	Templates  []OxygenTemplate `json:"templates,omitempty"`
	CodeBlocks []CodeBlock      `json:"code_blocks,omitempty"`
}

// OxygenElement represents a custom Oxygen element (class extends OxyEl)
type OxygenElement struct {
	ClassName  string   `json:"class_name"`
	Namespace  string   `json:"namespace,omitempty"`
	FullName   string   `json:"full_name"`
	BaseClass  string   `json:"base_class,omitempty"` // OxyEl, OxyElShadow, OxygenElement, etc.
	SlugMethod bool     `json:"has_slug"`             // Has slug() method
	Methods    []string `json:"methods,omitempty"`    // Detected methods (init, name, slug, icon, controls, render)
	FilePath   string   `json:"file_path"`
	StartLine  int      `json:"start_line"`
	EndLine    int      `json:"end_line"`
}

// OxygenTemplate represents an Oxygen template registration
// Oxygen uses ct_template custom post type for templates
type OxygenTemplate struct {
	PostType string `json:"post_type"` // ct_template or oxy_user_library
	Name     string `json:"name,omitempty"`
	FilePath string `json:"file_path"`
	Line     int    `json:"line,omitempty"`
}

// CodeBlock represents an Oxygen Code Block (PHP inline in builder)
// These are elements with type "code-block" in ct_builder_json
type CodeBlock struct {
	FilePath  string `json:"file_path"`
	StartLine int    `json:"start_line"`
	EndLine   int    `json:"end_line"`
}

// OxyElRequiredMethods lists the methods that an OxyEl element should implement
var OxyElRequiredMethods = []string{"init", "name", "slug", "icon", "controls", "render"}

package codetypes

// CodeChunk is the canonical v2 format for indexing/search. It represents a
// semantically meaningful piece of code (usually a function, method, type or
// interface declaration) that is stored in vector search.
type CodeChunk struct {
	// Symbol metadata
	Type     string // function | method | type | interface | file
	Name     string // Symbol name (or file base name for Type=file)
	Package  string // Package/module name
	Language string // go | php | python | typescript etc

	// Source location
	FilePath  string // Relative path from repository root
	URI       string // Full document URI (optional)
	StartLine int    // 1-based
	EndLine   int    // 1-based

	// Selection range (for precise navigation to symbol name)
	SelectionStartLine int // 1-based line where symbol name starts
	SelectionEndLine   int // 1-based line where symbol name ends

	// Content
	Signature string // Function/method signature or type definition header
	Docstring string // Associated doc comment (trimmed)
	Code      string // Pretty-printed code for this chunk

	// Extra metadata
	Metadata map[string]any
}

// LEGACY: APIChunk/APIAnalyzer are part of an older indexing path for
// "API docs". New code should rely on CodeChunk + Descriptor schema instead.
// These types are kept temporarily for backward-compatibility and tests and
// will be removed once all callers are migrated.

// APIChunk represents API-level documentation extracted from code symbols.
type APIChunk struct {
	// Identification
	Kind     string `json:"kind"` // function | method | type | const | var | class | interface
	Name     string `json:"name"`
	Language string `json:"language"` // go | php | python | typescript etc

	// Location
	PackagePath string `json:"package_path,omitempty"`
	Package     string `json:"package"`
	FilePath    string `json:"file_path,omitempty"`
	URI         string `json:"uri,omitempty"` // Full document URI
	StartLine   int    `json:"start_line,omitempty"`
	EndLine     int    `json:"end_line,omitempty"`

	// Selection range (for precise navigation to symbol name)
	SelectionStartLine int `json:"selection_start_line,omitempty"`
	SelectionEndLine   int `json:"selection_end_line,omitempty"`

	// Content & Documentation
	Signature   string `json:"signature,omitempty"`
	Description string `json:"description,omitempty"`
	Detail      string `json:"detail,omitempty"` // Short additional info
	Code        string `json:"code,omitempty"`

	// Function/Method specific
	Parameters []ParamInfo  `json:"parameters,omitempty"`
	Returns    []ReturnInfo `json:"returns,omitempty"`
	Receiver   string       `json:"receiver,omitempty"`
	Examples   []string     `json:"examples,omitempty"`

	// Type specific
	Fields  []FieldInfo  `json:"fields,omitempty"`
	Methods []MethodInfo `json:"methods,omitempty"`

	// Const/Var specific
	DataType string `json:"data_type,omitempty"`
	Value    string `json:"value,omitempty"`

	// Metadata & Attributes
	IsExported     bool     `json:"is_exported"`
	AccessModifier string   `json:"access_modifier,omitempty"` // public | private | protected | internal
	Tags           []string `json:"tags,omitempty"`            // deprecated | experimental | internal
	Deprecated     bool     `json:"deprecated,omitempty"`
	ContainerName  string   `json:"container_name,omitempty"` // Parent class/module name

	// Hierarchy support (for nested symbols)
	Children []APIChunk `json:"children,omitempty"`
}

// FieldInfo describes a struct/record field (LEGACY, used by APIChunk).
type FieldInfo struct {
	Name        string `json:"name"`
	Type        string `json:"type"`
	Tag         string `json:"tag,omitempty"`
	Description string `json:"description"`
}

// ParamInfo describes a function parameter (LEGACY, used by APIChunk).
type ParamInfo struct {
	Name string `json:"name"`
	Type string `json:"type"`
}

// ReturnInfo describes a function return value (LEGACY, used by APIChunk).
type ReturnInfo struct {
	Type        string `json:"type"`
	Description string `json:"description"`
}

// MethodInfo describes a method signature (LEGACY, used in APIChunk).
type MethodInfo struct {
	Name         string       `json:"name"`
	Signature    string       `json:"signature"`
	Description  string       `json:"description"`
	Parameters   []ParamInfo  `json:"parameters"`
	Returns      []ReturnInfo `json:"returns"`
	ReceiverType string       `json:"receiver_type,omitempty"`
	IsExported   bool         `json:"is_exported"`
	FilePath     string       `json:"file_path,omitempty"`
	StartLine    int          `json:"start_line,omitempty"`
	EndLine      int          `json:"end_line,omitempty"`
	Code         string       `json:"code,omitempty"`
}

// PathAnalyzer is any analyzer that can return CodeChunks for given paths.
type PathAnalyzer interface {
	AnalyzePaths(paths []string) ([]CodeChunk, error)
}

// APIAnalyzer is any analyzer that can return APIChunks for given paths.
// LEGACY: prefer PathAnalyzer + Descriptor schema instead.
type APIAnalyzer interface {
	AnalyzeAPIPaths(paths []string) ([]APIChunk, error)
}

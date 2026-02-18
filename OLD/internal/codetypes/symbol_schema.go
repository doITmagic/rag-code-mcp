package codetypes

// Canonical v2 schema for tool outputs (JSON) used by all code-understanding
// tools. Any MCP tool that supports output_format=json MUST serialize one of
// these descriptors.
// SymbolLocation describes where a symbol is defined in source code.
type SymbolLocation struct {
	FilePath  string `json:"file_path,omitempty"`
	URI       string `json:"uri,omitempty"`
	StartLine int    `json:"start_line,omitempty"`
	EndLine   int    `json:"end_line,omitempty"`
}

// FieldDescriptor describes a field/property within a type.
type FieldDescriptor struct {
	Name        string `json:"name"`
	Type        string `json:"type,omitempty"`
	Visibility  string `json:"visibility,omitempty"`
	Tag         string `json:"tag,omitempty"`
	Description string `json:"description,omitempty"`
}

// ParamDescriptor describes a function or method parameter.
type ParamDescriptor struct {
	Name        string `json:"name"`
	Type        string `json:"type,omitempty"`
	Description string `json:"description,omitempty"`
}

// ReturnDescriptor describes a function or method return value.
type ReturnDescriptor struct {
	Type        string `json:"type,omitempty"`
	Description string `json:"description,omitempty"`
	SourceHint  string `json:"source_hint,omitempty"` // phpdoc | type_hint | inferred | unknown
}

// RelationDescriptor describes a relationship between two symbols (e.g., ORM relations).
type RelationDescriptor struct {
	Name          string `json:"name"`
	RelationKind  string `json:"relation_kind"`            // hasOne, hasMany, belongsTo, implements, embeds, etc.
	RelatedSymbol string `json:"related_symbol,omitempty"` // Fully-qualified related symbol (e.g., App\\Organ)
	ForeignKey    string `json:"foreign_key,omitempty"`
	LocalKey      string `json:"local_key,omitempty"`
	Description   string `json:"description,omitempty"`
}

// FunctionDescriptor represents a function-like symbol (function, method, scope, accessor, etc.).
type FunctionDescriptor struct {
	Language    string         `json:"language"`
	Kind        string         `json:"kind"` // function | method | scope | accessor | mutator | constructor
	Name        string         `json:"name"`
	Namespace   string         `json:"namespace,omitempty"`
	Receiver    string         `json:"receiver,omitempty"` // Parent type for methods
	Signature   string         `json:"signature,omitempty"`
	Description string         `json:"description,omitempty"`
	Location    SymbolLocation `json:"location,omitempty"`

	Parameters []ParamDescriptor  `json:"parameters,omitempty"`
	Returns    []ReturnDescriptor `json:"returns,omitempty"`

	Visibility string `json:"visibility,omitempty"` // public | protected | private | exported (Go)
	IsStatic   bool   `json:"is_static,omitempty"`
	IsAbstract bool   `json:"is_abstract,omitempty"`
	IsFinal    bool   `json:"is_final,omitempty"`

	Code     string         `json:"code,omitempty"`
	Tags     []string       `json:"tags,omitempty"`
	Metadata map[string]any `json:"metadata,omitempty"`
}

// ClassDescriptor represents a type-like symbol (class, interface, trait, struct, model, etc.).
type ClassDescriptor struct {
	Language string `json:"language"`
	Kind     string `json:"kind"` // class | interface | trait | struct | type | model
	Name     string `json:"name"`

	Namespace string `json:"namespace,omitempty"`
	Package   string `json:"package,omitempty"`
	FullName  string `json:"full_name,omitempty"`

	Signature   string         `json:"signature,omitempty"`
	Description string         `json:"description,omitempty"`
	Location    SymbolLocation `json:"location,omitempty"`

	Fields    []FieldDescriptor    `json:"fields,omitempty"`
	Methods   []FunctionDescriptor `json:"methods,omitempty"`
	Relations []RelationDescriptor `json:"relations,omitempty"`

	// Data-model specific (used by ORMs like Eloquent, but generic enough for others)
	Table      string            `json:"table,omitempty"`
	Fillable   []string          `json:"fillable,omitempty"`
	Hidden     []string          `json:"hidden,omitempty"`
	Visible    []string          `json:"visible,omitempty"`
	Appends    []string          `json:"appends,omitempty"`
	Casts      map[string]string `json:"casts,omitempty"`
	Scopes     []string          `json:"scopes,omitempty"`
	Attributes []string          `json:"attributes,omitempty"`

	Tags     []string       `json:"tags,omitempty"`
	Metadata map[string]any `json:"metadata,omitempty"`
}

// SymbolDescriptor is a lightweight summary of any symbol, useful for listings.
type SymbolDescriptor struct {
	Language  string `json:"language"`
	Kind      string `json:"kind"` // class | interface | trait | function | method | constant | enum | file
	Name      string `json:"name"`
	Namespace string `json:"namespace,omitempty"`
	Package   string `json:"package,omitempty"`

	Signature   string         `json:"signature,omitempty"`
	Description string         `json:"description,omitempty"`
	Location    SymbolLocation `json:"location,omitempty"`

	Tags     []string       `json:"tags,omitempty"`
	Metadata map[string]any `json:"metadata,omitempty"`
}

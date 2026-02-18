package golang

import "github.com/doITmagic/rag-code-mcp/internal/codetypes"

// PackageInfo contains comprehensive information about a Go package
type PackageInfo struct {
	Name        string         `json:"name"`
	Path        string         `json:"path"`
	Description string         `json:"description"`
	Functions   []FunctionInfo `json:"functions"`
	Types       []TypeInfo     `json:"types"`
	Constants   []ConstantInfo `json:"constants"`
	Variables   []VariableInfo `json:"variables"`
	Examples    []ExampleInfo  `json:"examples"`
	Imports     []string       `json:"imports"`
}

// FunctionInfo describes a function or method
type FunctionInfo struct {
	Name        string                 `json:"name"`
	Signature   string                 `json:"signature"`
	Description string                 `json:"description"`
	Parameters  []codetypes.ParamInfo  `json:"parameters"`
	Returns     []codetypes.ReturnInfo `json:"returns"`
	Examples    []string               `json:"examples"`
	IsExported  bool                   `json:"is_exported"`
	IsMethod    bool                   `json:"is_method"`
	Receiver    string                 `json:"receiver,omitempty"`
	FilePath    string                 `json:"file_path,omitempty"`
	StartLine   int                    `json:"start_line,omitempty"`
	EndLine     int                    `json:"end_line,omitempty"`
	Code        string                 `json:"code,omitempty"`
}

// TypeInfo describes a type declaration (struct, interface, alias, etc.)
type TypeInfo struct {
	Name        string                 `json:"name"`
	Kind        string                 `json:"kind"` // struct, interface, alias, etc.
	Description string                 `json:"description"`
	Fields      []codetypes.FieldInfo  `json:"fields,omitempty"`
	Methods     []codetypes.MethodInfo `json:"methods,omitempty"`
	IsExported  bool                   `json:"is_exported"`
	FilePath    string                 `json:"file_path,omitempty"`
	StartLine   int                    `json:"start_line,omitempty"`
	EndLine     int                    `json:"end_line,omitempty"`
	Code        string                 `json:"code,omitempty"`
}

// ConstantInfo describes a constant declaration
type ConstantInfo struct {
	Name        string `json:"name"`
	Type        string `json:"type"`
	Value       string `json:"value"`
	Description string `json:"description"`
	IsExported  bool   `json:"is_exported"`
	FilePath    string `json:"file_path,omitempty"`
	StartLine   int    `json:"start_line,omitempty"`
	EndLine     int    `json:"end_line,omitempty"`
}

// VariableInfo describes a variable declaration
type VariableInfo struct {
	Name        string `json:"name"`
	Type        string `json:"type"`
	Description string `json:"description"`
	IsExported  bool   `json:"is_exported"`
	FilePath    string `json:"file_path,omitempty"`
	StartLine   int    `json:"start_line,omitempty"`
	EndLine     int    `json:"end_line,omitempty"`
}

// ExampleInfo describes a code example
type ExampleInfo struct {
	Name string `json:"name"`
	Code string `json:"code"`
	Doc  string `json:"doc"`
}
